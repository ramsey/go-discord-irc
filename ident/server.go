package ident

import (
	"bufio"
	"fmt"
	"net"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/pkg/errors"
	irc "github.com/qaisjp/go-ircevent"
	log "github.com/sirupsen/logrus"
)

type PortmapEntry struct {
	DiscordUid string
	Username   string
	Nickname   string
	LocalPort  uint16
}

type Server struct {
	mutex    *sync.RWMutex
	portMap  map[string]PortmapEntry
	listener *net.TCPListener
}

func NewServer(identPort int) (*Server, error) {
	listener, err := net.ListenTCP("tcp", &net.TCPAddr{
		IP:   nil,
		Port: identPort,
	})

	if err != nil {
		return nil, errors.Wrap(err, fmt.Sprintf("Could not listen on port %d", identPort))
	}

	server := &Server{
		mutex:    &sync.RWMutex{},
		portMap:  make(map[string]PortmapEntry),
		listener: listener,
	}
	go server.run()

	log.Infof("ident: started ident server listening on port %d", identPort)

	return server, nil
}

func (server *Server) Bind(addr *irc.Connection, discordUid string) PortmapEntry {
	localPort := server.getLocalPort(addr)

	username := discordUid
	if len([]rune(username)) > 9 {
		username = username[:9]
	}

	existingEntry, ok := server.getPortmapEntryForLocalPort(localPort)

	if ok == true && existingEntry.DiscordUid != discordUid {
		// Someone else is already assigned this local port?
		log.WithFields(log.Fields{
			"existingEntry": existingEntry,
			"localPort":     localPort,
			"discordUid":    discordUid,
		}).Fatalln("ident: could not bind Discord username to local port already assigned")

		return existingEntry
	}

	server.mutex.Lock()
	defer server.mutex.Unlock()

	log.WithFields(log.Fields{
		"port":     localPort,
		"username": username,
		"nick":     addr.GetNick(),
	}).Infoln("ident: binding local port to Discord username")

	entry := server.portMap[discordUid]
	entry.DiscordUid = discordUid
	entry.Username = username
	entry.Nickname = addr.GetNick()
	entry.LocalPort = localPort
	server.portMap[discordUid] = entry

	return entry
}

func (server *Server) Unbind(discordUid string) {
	server.mutex.Lock()
	defer server.mutex.Unlock()

	delete(server.portMap, discordUid)
}

func (server *Server) processRequest(remote net.Conn) {
	defer remote.Close()
	remoteAddr := remote.RemoteAddr()
	remoteReader := bufio.NewReader(remote)

	re, _ := regexp.Compile(`\d+`)
	receivedLine, _ := remoteReader.ReadBytes('\n')
	ports := re.FindAll(receivedLine, -1)

	if len(ports) != 2 {
		log.WithFields(log.Fields{
			"requester": remoteAddr,
			"request":   receivedLine,
		}).Warnln("ident: failed to parse ident request")
		return
	}

	localPort, localPortErr := strconv.Atoi(string(ports[0]))
	remotePort, remotePortErr := strconv.Atoi(string(ports[1]))

	if localPortErr != nil {
		log.WithFields(log.Fields{
			"requester": remoteAddr,
			"error":     localPortErr,
		}).Warnln("ident: failed to parse ident request")
		return
	}

	if remotePortErr != nil {
		log.WithFields(log.Fields{
			"requester": remoteAddr,
			"error":     remotePortErr,
		}).Warnln("ident: failed to parse ident request")
		return
	}

	log.WithFields(log.Fields{
		"requester":  remoteAddr,
		"localPort":  localPort,
		"remotePort": remotePort,
	}).Infoln("ident: received request")

	// Wait a moment to make sure our portMap has the value.
	time.Sleep(2 * time.Second)

	entry, ok := server.getPortmapEntryForLocalPort(uint16(localPort))

	if !ok {
		fmt.Fprintf(remote, "%d, %d : ERROR : NO-USER\r\n", localPort, remotePort)
		return
	}

	fmt.Fprintf(remote, "%d, %d : USERID : LINUX,UTF-8 : %s\r\n", localPort, remotePort, entry.Username)
	return
}

func (server *Server) getLocalPort(conn *irc.Connection) uint16 {
	// Use reflection to get the private "socket" field from the IRC connection.
	reflectedObj := reflect.ValueOf(conn).Elem()
	reflectedField := reflectedObj.FieldByName("socket")
	reflectedField = reflect.NewAt(reflectedField.Type(), unsafe.Pointer(reflectedField.UnsafeAddr())).Elem()
	socket := reflectedField.Interface().(net.Conn)

	addr := strings.Split(socket.LocalAddr().String(), ":")

	value, _ := strconv.ParseUint(addr[len(addr)-1], 10, 16)
	return uint16(value)
}

func (server *Server) getPortmapEntryForLocalPort(localPort uint16) (entry PortmapEntry, ok bool) {
	server.mutex.RLock()
	defer server.mutex.RUnlock()

	for _, v := range server.portMap {
		if v.LocalPort == localPort {
			entry = v
			ok = true
		}
	}
	return
}

func (server *Server) run() {
	for {
		remote, err := server.listener.Accept()
		if err != nil {
			log.Fatalf("accept failed? %v", err)
		}

		go server.processRequest(remote)
	}
}
