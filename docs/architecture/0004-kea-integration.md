# Architecture Decision 0004: Kea Integration

## Status

Accepted

## Context

The existing sync scripts write directly to Kea's PostgreSQL schema. The new
service must talk to Kea only through its API.

The user also wants subnet IDs derived automatically from live Kea
configuration.

## Decision

`kea-sync` interacts with Kea exclusively through the Control Agent.

It uses:

- `config-get` to discover active DHCPv4 subnets
- `reservation-get-page` to enumerate current reservations by subnet
- `reservation-add`, `reservation-update`, and `reservation-del` to apply
  changes

Subnet selection is derived automatically from live Kea config using longest
prefix match on the selected IPv4 address.

Only DHCPv4 reservations are in scope for v1.

## Rationale

- This removes schema coupling to the hosts database.
- It works regardless of the underlying Kea host backend as long as host
  commands are enabled.
- Live subnet discovery avoids duplicating subnet configuration inside
  `kea-sync`.

## Consequences

- Kea must load `libdhcp_host_cmds.so` and have a writable hosts backend.
- `kea-sync` must tolerate paged host enumeration and Control Agent response
  formats.
- Overlapping subnets resolve to the most specific CIDR. Equal-prefix ties are
  broken by lower subnet ID.
