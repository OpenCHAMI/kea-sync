# Kea Sync Service Plan

Date: 2026-03-13

## Goal

Replace the current ad-hoc SMD to Kea synchronization scripts with a production-oriented Go microservice in `services/kea-sync`.

## Scope For This Implementation

This implementation delivers:

- a stateless Go service
- SMD reads through the SMD REST API
- Kea reads and writes through the Control Agent API only
- CoreSMD-compatible reservation derivation for DHCPv4
- ownership tagging through Kea `user-context`
- periodic reconcile loop
- diff preview and forced sync API
- Prometheus metrics
- JWKS-protected admin API support
- Tokensmith-compatible outbound bearer-token acquisition
- docker-friendly packaging

## Existing Behavior Being Replaced

The current behavior is split across:

- `operations/ochami-from-scratch/ochami-helm/kea-sidecar/smd_sync.py`
- `operations/ochami-from-scratch/ochami/registry.py`

Those scripts:

- poll SMD periodically
- use the first IP in the interface list
- map MAC to reserved IPv4 host entries
- write directly to Kea's PostgreSQL tables

That direct database mutation is explicitly out of scope for the new service.

## Chosen Architecture

The service is a controller-style reconciler:

1. Read desired state from SMD.
2. Read actual Kea DHCPv4 host reservations from the Control Agent.
3. Filter managed reservations using `user-context`.
4. Compute create, update, delete, and conflict sets.
5. Apply the diff through `reservation-add`, `reservation-update`, and `reservation-del`.

## Desired State Rules

Reservation derivation follows CoreSMD semantics as closely as practical:

- use EthernetInterfaces plus Components
- match by MAC
- pick the first IPv4 address present in the ordered IP list
- generate hostnames with:
  - `hostname_by_type` first
  - `hostname_default` second
  - `node_pattern` override for `Node`
  - `bmc_pattern` override for `NodeBMC`
  - optional domain suffix

## Ownership Rules

`kea-sync` manages only reservations carrying its ownership tag in `user-context`.

This allows:

- safe deletion of stale managed reservations
- coexistence with manual or static Kea entries
- a stateless reconciler

## Kea Integration Rules

- use the Control Agent with `service: ["dhcp4"]`
- require the host commands hook library to be enabled in Kea
- derive subnet IDs from `config-get`
- page through reservations with `reservation-get-page`
- avoid `reservation-get-all` for operational safety on larger datasets

## Auth Rules

- inbound admin API optionally protected by JWKS JWT verification
- outbound upstream calls support:
  - static bearer tokens
  - Tokensmith `POST /service/token` bearer-token acquisition

## API Surface

- `GET /liveness`
- `GET /readiness`
- `GET /health`
- `GET /metrics`
- `GET /v1/diff`
- `POST /v1/sync`

## Implementation Steps

1. Create architecture docs and bootstrap a standalone Go module.
2. Implement typed environment configuration.
3. Implement outbound auth providers and a reusable JSON HTTP client.
4. Implement SMD and Kea clients.
5. Implement CoreSMD-compatible hostname expansion.
6. Implement reconcile planning and apply logic.
7. Implement HTTP API, health endpoints, and metrics.
8. Add unit tests for hostname and reconcile behavior.
9. Add Docker packaging for docker-compose deployments.

## Production Hardening Included In Code

- retries and timeouts for upstream HTTP calls
- conflict detection against unmanaged Kea reservations
- last-sync status kept in memory for health and observability
- Prometheus metrics
- graceful shutdown
- write-disable mode for safe bring-up

## Deferred Work

The following are intentionally left for later iterations:

- incremental SMD sync using `NewerThan`
- leader election for multi-replica deployments
- IPv6 reservation support
- OpenAPI generation or Fabrica migration
- audit/history persistence
- dedicated compose recipe in the operations repository
