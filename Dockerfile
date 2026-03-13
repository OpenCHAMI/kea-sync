# syntax=docker/dockerfile:1.7

FROM golang:1.24 AS builder
ARG CGO_ENABLED=0

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN go build -trimpath -ldflags="-s -w" -o /out/kea-sync ./cmd/kea-sync

FROM cgr.dev/chainguard/wolfi-base:latest

RUN apk add --no-cache ca-certificates tini

ENV KEA_SYNC_LISTEN_ADDR=:8080 \
    KEA_SYNC_LOG_LEVEL=info \
    KEA_SYNC_SYNC_INTERVAL=30s \
    KEA_SYNC_PAGE_LIMIT=100 \
    KEA_SYNC_HTTP_READ_TIMEOUT=15s \
    KEA_SYNC_HTTP_WRITE_TIMEOUT=30s \
    KEA_SYNC_HTTP_SHUTDOWN_TIMEOUT=15s \
    KEA_SYNC_SMD_TIMEOUT=10s \
    KEA_SYNC_SMD_RETRIES=1 \
    KEA_SYNC_KEA_TIMEOUT=10s \
    KEA_SYNC_KEA_RETRIES=1 \
    KEA_SYNC_KEA_SERVICES=dhcp4

COPY --from=builder /out/kea-sync /usr/local/bin/kea-sync

USER 65534:65534

ENTRYPOINT ["/sbin/tini", "--"]
CMD ["/usr/local/bin/kea-sync"]
