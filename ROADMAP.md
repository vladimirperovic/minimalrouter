# Roadmap

The roadmap is outcome-based. Dates are intentionally omitted until the first
vertical slice establishes realistic delivery speed.

## Milestone 0 — Decisions and proof of concept

Goal: remove the highest architectural risks before building the full UI.

- Accept the product, architecture, and security baselines.
- Create ADRs for update rollback, certificate bootstrap, backup encryption,
  image building, and IPv6 scope.
- Pin Alpine and Go versions.
- Build a minimal Alpine image for `x86_64`.
- Prove PPPoE, DHCP/DNS, nftables, and WireGuard integration independently.
- Prove an atomic nftables apply and automatic rollback.
- Prove commit-confirmed LAN address changes.
- Measure boot, idle memory, and routing throughput on reference hardware.

Exit criteria: a console-driven prototype applies and rolls back one complete
WAN/LAN configuration safely.

## Milestone 1 — Configuration engine

Goal: establish the single safe path for every future feature.

- SQLite schema and migrations
- Typed configuration model
- Cross-field validation
- Deterministic generators
- Preflight adapters
- Immutable snapshots
- Apply state machine
- Crash recovery
- Privileged helper protocol
- Audit events

Exit criteria: integration tests inject failures at every apply stage and always
produce either the new valid state or the previous known-good state.

## Milestone 2 — Secure management plane

Goal: make the appliance safely configurable from a browser and API client.

- HTTPS and per-device certificate bootstrap
- First-run administrator creation
- Argon2id authentication
- Secure server-side sessions
- CSRF and same-origin controls
- Rate limiting and local-console recovery
- Versioned REST API and OpenAPI contract
- Svelte application shell and design system

Exit criteria: authentication and session security gates in `SECURITY.md` pass
end-to-end tests.

## Milestone 3 — Core router experience

Goal: deliver the smallest useful router.

- Installation flow
- WAN detection and confirmation
- PPPoE
- LAN addressing
- DHCP and static leases
- DNS forwarding
- Simple firewall, NAT, and port forwarding
- Minimal dashboard
- Connected-device view

Exit criteria: a non-networking user can install the image, get online, and
recover automatically from a lockout-prone change.

## Milestone 4 — Secure connectivity

Goal: add the remaining version 1 network integrations.

- WireGuard
- Cloudflare DDNS
- Cloudflare Tunnel
- Secret rotation and redacted status
- Integration-specific failure and rollback tests

Exit criteria: each integration can be configured, disabled, rotated, and
restored without leaking its secrets.

## Milestone 5 — Lifecycle management

Goal: make long-term operation safe.

- Encrypted backup export
- Validated restore
- Signed update channel
- Pre-update snapshot
- Boot health confirmation and update rollback
- Bounded logs and diagnostics export
- Factory reset and recovery console

Exit criteria: update, failed-update rollback, backup, cross-version restore,
and recovery are tested on every claimed platform.

## Milestone 6 — Version 1 release

Goal: meet the product, security, and performance promises.

- Bare-metal and hypervisor compatibility matrix
- 1/2.5/10 GbE measurements on documented hardware
- Boot under 10 seconds on reference hardware
- 150–250 MB normal memory use
- Reproducible release artifacts and SBOM
- Independent security review
- Installation and administrator documentation
- Support and security-update policy

Exit criteria: all version 1 success criteria in `PROJECT.md` and release gates
in `SECURITY.md` pass with recorded evidence.

## Explicitly deferred

IDS/IPS, captive portals, multi-WAN, BGP, OSPF, Docker, Kubernetes, enterprise
QoS, OpenVPN, and IPsec remain outside version 1.
