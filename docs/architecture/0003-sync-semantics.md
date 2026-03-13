# Architecture Decision 0003: Sync Semantics

## Status

Accepted

## Context

The user asked to imitate the behavior currently implemented in CoreSMD where
the exact policy is not yet fully documented elsewhere.

## Decision

The desired reservation set is derived from SMD using CoreSMD-compatible DHCPv4
semantics:

- match hosts by MAC address
- require a matching SMD component for the Ethernet interface
- use the first `IPAddresses` entry only when it is IPv4
- reproduce CoreSMD hostname generation
- ignore interfaces that do not have a usable IPv4 address

Hostname generation follows the CoreSMD logic:

1. start with `hostname_by_type` or `hostname_default` policy when configured
2. overwrite that value for `Node` components using `node_pattern`
3. overwrite that value for `NodeBMC` components using `bmc_pattern`
4. append `domain` when configured for `Node` and `NodeBMC`

Defaults match CoreSMD:

- `node_pattern = nid{04d}`
- `bmc_pattern = bmc{04d}`
- `domain = ""`

Deletion semantics also imitate CoreSMD:

- if a previously known SMD interface/component pair no longer yields a desired
  reservation, the corresponding managed Kea reservation is deleted

## Rationale

- It preserves current OpenCHAMI behavior while moving implementation from an
  in-process DHCP plugin or ad-hoc script to a dedicated service.
- It makes v1 predictable for operators already relying on CoreSMD naming and
  address selection.

## Consequences

- A desired reservation exists only when both the Ethernet interface and the
  corresponding component are present in SMD.
- Multiple MAC addresses may legitimately converge on the same IP if SMD says
  so; `kea-sync` does not collapse that model on its own.
