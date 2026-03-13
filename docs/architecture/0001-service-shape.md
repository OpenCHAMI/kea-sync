# 0001: Stateless Reconciler Service Shape

## Status

Accepted, 2026-03-13

## Context

The current SMD to Kea synchronization uses ad-hoc Python and SQL scripts. The
replacement service must:

- live in `services/kea-sync`
- be hosted as a normal OpenCHAMI Go service
- keep SMD as the source of truth
- avoid local durable state
- expose operational APIs for previewing and forcing reconciliation

The user also asked for docker-compose-first deployment while keeping the code
platform agnostic.

## Decision

`kea-sync` is implemented as a plain Go microservice with:

- a background periodic reconcile loop
- a synchronous preview endpoint
- a synchronous forced-sync endpoint
- in-memory health and last-run status only

It is not a Fabrica service in v1.

## Rationale

The service reconciles external systems rather than managing stored API
resources. Fabrica would add schema and storage overhead without improving the
core reconciliation path for the first version.

## Consequences

- The API is purpose-built for operations rather than resource CRUD.
- The service remains stateless in the durable-storage sense.
- A future Fabrica migration stays possible if OpenCHAMI later wants
  declarative sync policies or persisted run history.
