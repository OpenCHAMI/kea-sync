# Architecture Decision 0002: Authority And Ownership

## Status

Accepted

## Context

SMD is authoritative for reservations derived from inventory. Kea must still
support manual or static reservations that are not controlled by `kea-sync`.
The service itself must remain stateless.

## Decision

`kea-sync` manages only reservations it can identify as its own by stamping
Kea host reservations with `user-context`.

The ownership marker is:

```json
{
  "openchami": {
    "managed-by": "kea-sync",
    "source": "smd"
  }
}
```

`kea-sync` will:

- reconcile only reservations carrying this ownership marker
- delete managed reservations when the backing SMD data disappears
- leave unmarked reservations untouched
- report conflicts when desired SMD-derived reservations collide with unmanaged
  Kea entries

## Rationale

- This preserves operator-created Kea reservations without requiring local
  state.
- It makes ownership explicit inside Kea itself.
- It allows deletion of stale managed reservations while remaining safe around
  manual entries.

## Consequences

- `kea-sync` must read current Kea reservations before applying changes.
- Ownership is defined by data in Kea, not by process memory.
- Reservations created outside `kea-sync` are not mutated unless they are later
  explicitly tagged as managed.
