# Architecture

Minimal Router OS keeps the Linux data plane conventional and the management
control plane small. Changes to trust boundaries, privilege separation,
persistence, updates or transaction semantics should be recorded as an ADR.

## Overview

```mermaid
flowchart LR
    Browser[Dashboard / API client]
    Routerd[routerd — unprivileged]
    DB[(SQLite canonical state)]
    Applyd[router-applyd — privileged]
    Linux[Linux networking stack]
    LAN[LAN]
    WAN[WAN]

    Browser -->|HTTPS /api/v1| Routerd
    Routerd --> DB
    Routerd -->|typed local IPC| Applyd
    Applyd --> Linux
    LAN <--> Linux
    Linux <--> WAN
```

The control plane is implemented in the Rust workspace under `rust/`. Packet
forwarding remains in the Linux kernel and
standard services such as nftables, pppd, dnsmasq and WireGuard.

The workspace currently has broad library boundaries: `mr-config` includes
configuration, storage, authentication and HTTP/TLS; `mr-services` includes
generators, the apply engine, telemetry and recovery. `mr-applyd` depends on
both. These build dependencies are not evidence that the root helper serves
HTTP, but they make its trusted dependency graph harder to review. Extracting
pure configuration types/validation and generators into narrower crates is
follow-up architectural work, requiring its own compatibility and rollback
validation. The runtime privilege boundary remains the typed, allowlisted IPC
interface; changing crate layout must not expand it.

`routerd` remains the authority for which validated configuration is canonical;
`RECONCILE` is not an independent administrator-approval boundary. Treat a
compromised management process as able to change network policy within helper
validation. For WireGuard-only management, WAN changes also require confirmation
because the WAN carries the management tunnel. Disabled tunnel fields do not
select arbitrary interfaces for privileged cleanup, and generated WireGuard
text is checked for line injection even when the section is disabled.

Interface ownership is exclusive: `router-applyd` assigns the LAN address, pppd
owns the WAN, and wg(8) owns the tunnels. The installer therefore writes an
`/etc/network/interfaces` that declares every physical interface `manual`, and
`router-applyd` orders itself `after net` so the distribution network service can
never re-run `ifup` against an address the helper has already installed.

## Main components

### `routerd`

Unprivileged management service responsible for:

- HTTPS/API and static dashboard delivery;
- authentication, sessions, CSRF and rate limits;
- typed validation and canonical SQLite state;
- configuration transactions, snapshots and audit events;
- read-only telemetry;
- coordinating the privileged helper.

It must not execute arbitrary commands or directly modify privileged service
configuration.

### `router-applyd`

Root helper with a deliberately narrow local IPC interface. It performs fixed,
allowlisted networking/service operations, records privileged intent/results,
verifies applied state and restores approved snapshots when possible.

Unknown or contradictory privileged state is never reported as success.

### Dashboard

React/TypeScript SPA compiled to static assets and served by `routerd`. It is an
API client only and contains no privileged networking logic.

### Canonical state

SQLite is the source of truth. Generated nftables/dnsmasq/pppd/WireGuard/etc.
files are artifacts derived from validated state. Recovery metadata and
`last-good` state support rollback but do not replace the canonical database.

## Linux integrations

| Function | Component |
|---|---|
| Firewall / NAT | nftables |
| PPPoE | pppd |
| DHCP / DNS / basic filtering | dnsmasq |
| Remote access | WireGuard |
| Dynamic DNS | inadyn |
| QoS | `tc` |
| Optional proxy | Squid |
| Optional Wi-Fi AP | hostapd + Linux bridge |
| Optional per-device accounting | nftables dynamic sets (`acct_rx` / `acct_tx`) |

The project owns only its explicitly named files, interfaces/services and the
`inet minimalrouter` nftables table. It must not flush unrelated host state.

## Configuration transaction

Every mutation follows the same pipeline:

```text
request
  → strict validation
  → deterministic candidate generation
  → component preflight
  → snapshot
  → privileged apply
  → runtime verification
  → commit / confirmation / rollback
```

A transaction ends in exactly one meaningful state:

- `Committed` — candidate is verified and canonical;
- `RolledBack` — previous state is positively verified as restored;
- `RecoveryRequired` — outcome cannot be proven, so further mutation is blocked.

Lockout-prone changes use commit-confirm behavior with automatic rollback before
the candidate becomes canonical. Startup also reconciles canonical state before
normal management is considered ready.

## Security boundaries

Default appliance policy:

- WAN input is deny by default;
- dashboard management is not exposed directly to WAN;
- WireGuard is the intended remote-management path;
- arbitrary WAN port forwarding is outside the current secure profile. Port
  forwards exist, but every generated DNAT rule is bound to the WireGuard server
  interface, so a forward is reachable from the tunnel and never from WAN or
  `ppp0`. Validation refuses a forward when WireGuard is disabled, because such a
  rule could never take effect;
- DNS/DHCP bind only to intended LAN paths;
- unsupported functionality fails closed rather than appearing successful.

Secrets are excluded from normal API responses and logs, stored with restrictive
permissions and encrypted when exported in backups.

See [`SECURITY.md`](SECURITY.md) for the full threat model.

## Updates and recovery

Updates use A/B slots with signed-manifest support and a durable operation
journal. Recovery is available locally even when normal management cannot start.
A stable signed installation/recovery-media workflow remains a release gate.

## Source layout

```text
rust/src/bin/         Rust executable entry points
rust/crates/          control-plane workspace libraries
rust/tests/           integration and regression tests
web/                  React dashboard
api/openapi.yaml      REST API contract
rust/crates/mr-config/src/config/  configuration and SQLite storage
packaging/alpine/     installer + OpenRC integration
scripts/              CI/test helpers
docs/                 guides, evidence and ADRs
```

## Non-goals

The current architecture does not aim for pfSense feature parity, multi-WAN/HA,
IDS/IPS, captive portal, BGP/OSPF, IPsec/OpenVPN, arbitrary shell customization or
container hosting on the router.

Validation details belong in [`docs/TESTING.md`](docs/TESTING.md) and
[`docs/CURRENT_VALIDATION.md`](docs/CURRENT_VALIDATION.md), rather than being
repeated here.
