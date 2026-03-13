# kea-sync

`kea-sync` is a stateless Go microservice that reconciles SMD inventory into
Kea DHCPv4 host reservations through APIs only.

It does not write Kea's database directly. SMD remains the source of truth.

## Behavior

- reads SMD `EthernetInterfaces` and `State/Components`
- derives desired DHCPv4 reservations using CoreSMD-compatible rules
- discovers Kea subnet IDs from live `config-get`
- reads Kea reservations through paged Control Agent host commands
- manages only reservations tagged with `user-context.openchami.managed-by=kea-sync`
- leaves untagged/manual Kea reservations untouched
- exposes `GET /v1/diff` and `POST /v1/sync`

## Kea Prerequisites

Kea must:

- expose the Control Agent
- load `libdhcp_host_cmds.so`
- have a writable hosts backend

`kea-sync` currently targets DHCPv4 reservations only.

## Configuration

Core service settings:

- `KEA_SYNC_LISTEN_ADDR`
- `KEA_SYNC_LOG_LEVEL`
- `KEA_SYNC_SYNC_INTERVAL`
- `KEA_SYNC_PAGE_LIMIT`
- `KEA_SYNC_HTTP_READ_TIMEOUT`
- `KEA_SYNC_HTTP_WRITE_TIMEOUT`
- `KEA_SYNC_HTTP_SHUTDOWN_TIMEOUT`

Upstreams:

- `KEA_SYNC_SMD_URL`
- `KEA_SYNC_SMD_TIMEOUT`
- `KEA_SYNC_SMD_RETRIES`
- `KEA_SYNC_SMD_BEARER_TOKEN`
- `KEA_SYNC_SMD_TARGET_SERVICE`
- `KEA_SYNC_SMD_SCOPES`
- `KEA_SYNC_KEA_URL`
- `KEA_SYNC_KEA_TIMEOUT`
- `KEA_SYNC_KEA_RETRIES`
- `KEA_SYNC_KEA_BEARER_TOKEN`
- `KEA_SYNC_KEA_TARGET_SERVICE`
- `KEA_SYNC_KEA_SCOPES`
- `KEA_SYNC_KEA_SERVICES`

Tokensmith:

- `KEA_SYNC_TOKENSMITH_URL`
- `KEA_SYNC_TOKENSMITH_SERVICE_API_KEY`
- `KEA_SYNC_TOKENSMITH_TIMEOUT`
- `KEA_SYNC_TOKENSMITH_REFRESH_SKEW`

Inbound admin auth:

- `KEA_SYNC_AUTH_JWKS_URL`
- `KEA_SYNC_AUTH_JWKS_REFRESH_INTERVAL`
- `KEA_SYNC_AUTH_READ_SCOPES`
- `KEA_SYNC_AUTH_WRITE_SCOPES`

Hostname behavior:

- `KEA_SYNC_NODE_PATTERN`
- `KEA_SYNC_BMC_PATTERN`
- `KEA_SYNC_DOMAIN`
- `KEA_SYNC_HOSTNAME_DEFAULT`
- `KEA_SYNC_HOSTNAME_BY_TYPE`

## API

Public endpoints:

- `GET /liveness`
- `GET /readiness`
- `GET /health`
- `GET /metrics`

Protected endpoints when JWKS auth is configured:

- `GET /v1/diff`
- `POST /v1/sync`

## Local Build

```bash
go test ./...
go build ./cmd/kea-sync
```

## Docker Compose

See [examples/docker-compose.yaml](/home/escoubas/git_repos/github/aescoubas/ochami-dev/services/kea-sync/examples/docker-compose.yaml).
