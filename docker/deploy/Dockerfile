# Production image: CGO build on Debian, run on distroless (glibc, no shell).
FROM golang:1.26-bookworm AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o /out/rps-backend ./cmd/rps-backend \
    && mkdir -p /out/data && chown 65532:65532 /out/data

FROM gcr.io/distroless/base-debian12:nonroot

COPY --from=builder /out/rps-backend /rps-backend
COPY --from=builder --chown=65532:65532 /out/data /data

ENV RPS_DB_PATH=/data/rps.db \
    RPS_PORT=8080

VOLUME /data
EXPOSE 8080
USER nonroot:nonroot

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD ["/rps-backend", "healthcheck"]

ENTRYPOINT ["/rps-backend"]
