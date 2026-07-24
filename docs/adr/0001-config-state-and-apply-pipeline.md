# 0001 — Canonical configuration and apply pipeline

- Status: Accepted
- Date: 2026-07-24
- Owners: project maintainers

## Context

The appliance coordinates several Linux components with different
configuration formats and reload behavior. Version 1 requires automatic
snapshots, validation, rollback, crash recovery, and a REST API. Treating
generated files as independent sources of truth would make cross-component
validation and atomic recovery unreliable.

## Decision

SQLite is the canonical configuration and state store. Service files are
deterministic derived artifacts.

All mutations use one serialized transaction pipeline:

`input -> validation -> model -> generation -> preflight -> snapshot -> apply -> verify -> commit or rollback`

`routerd` plans transactions without root privileges. A separate
`router-applyd` process performs only typed, allowlisted privileged operations.
Disruptive changes use commit-confirmed rollback.

## Consequences

Benefits:

- Transactional state and schema migrations
- One validation path for UI and API
- Deterministic snapshots and generated artifacts
- Clear crash-recovery markers
- Smaller privileged interface

Costs:

- Generators and adapters must be maintained for each Linux component.
- Operators cannot treat hand-edited service files as persistent.
- SQLite migration compatibility becomes a release responsibility.
- Cross-component apply cannot be truly atomic, so compensation and rollback
  must be tested aggressively.

## Alternatives considered

### Single configuration file

Simple and inspectable, but less suitable for concurrent revisions,
idempotency, audit metadata, migrations, and snapshot bookkeeping.

### Directly edit native service files

Rejected because native formats cannot provide one cross-component source of
truth or reliable transactional rollback.

### Run the entire backend as root

Rejected for production because compromise of the web/API process would grant
unrestricted system control.

## Validation

Milestone 0 must demonstrate a WAN/LAN configuration that:

- Generates nftables, dnsmasq, and PPPoE artifacts deterministically.
- Passes real component preflight.
- Applies successfully from an empty state.
- Rolls back after injected failure at every stage.
- Recovers correctly after reboot at each durable transaction marker.
