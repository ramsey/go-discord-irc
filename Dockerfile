ARG GOLANG_VERSION=1.23
FROM golang:${GOLANG_VERSION}-alpine

WORKDIR /bot
COPY . .
RUN go build

CMD ["./go-discord-irc", "--config", "config.yml"]
