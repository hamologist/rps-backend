# Hot-reload development image. The repo is bind-mounted at /app by the root
# compose.yaml; air rebuilds and restarts the server on .go / .sql changes.
FROM golang:1.26-bookworm

ENV CGO_ENABLED=1

RUN go install github.com/air-verse/air@v1.62.0

WORKDIR /app

EXPOSE 8080

CMD ["air", "-c", "docker/dev/.air.toml"]
