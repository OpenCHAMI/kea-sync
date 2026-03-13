# Architecture Decision 0005: Auth And API

## Status

Accepted

## Context

The service needs:

- outbound auth toward protected SMD and Kea APIs
- inbound auth for operator-triggered sync and diff endpoints
- OpenCHAMI-compatible auth from day one

## Decision

`kea-sync` supports two outbound auth modes per upstream:

- static bearer token
- Tokensmith service-token acquisition using `POST /service/token`

Inbound admin routes use Tokensmith JWT/JWKS middleware when configured.

Public endpoints:

- `GET /liveness`
- `GET /readiness`
- `GET /health`
- `GET /metrics`

Protected management endpoints:

- `GET /v1/diff`
- `POST /v1/sync`

## Rationale

- Static bearer tokens keep local development and bootstrap simple.
- Tokensmith-backed service tokens align with the broader OpenCHAMI security
  model.
- Keeping liveness/readiness public matches common deployment expectations,
  especially for docker-compose and orchestrators.

## Consequences

- The service includes a small outbound token client rather than relying on the
  current incomplete Tokensmith helper client.
- API authorization is configuration-driven and can be disabled for bootstrap or
  local development.
