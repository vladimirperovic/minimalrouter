# Product direction

Minimal Router OS is a focused Alpine Linux router appliance for home and
small-office networks. It combines proven Linux networking components with a
small Go control plane, transactional configuration and a clear web UI.

> **Status: early alpha.** Current evidence is tracked in
> [`docs/CURRENT_VALIDATION.md`](docs/CURRENT_VALIDATION.md).

## Principles

- Keep the feature set small and understandable.
- Reuse mature Linux networking components.
- Keep packet forwarding out of the Go management process.
- Prefer safe, reversible configuration over clever automation.
- Fail closed when runtime state cannot be proven.
- Make optional exposure explicit and opt-in.
- Require reproducible evidence for performance and security claims.

## Core scope

The current product focuses on:

- one WAN and one LAN role;
- PPPoE WAN;
- DHCP and DNS;
- default-deny firewall and LAN-to-WAN NAT;
- WireGuard remote access;
- No-IP / Cloudflare Dynamic DNS;
- device visibility and basic DNS filtering;
- snapshots, encrypted backups and rollback;
- gateway quality and appliance-health monitoring;
- optional Squid, QoS and Wi-Fi AP support.

WAN web management and arbitrary WAN port forwarding are intentionally outside
the current secure appliance profile.

## Technology

- Alpine Linux + OpenRC
- Go (`routerd`, `router-applyd`, recovery/update tools)
- React + TypeScript + Vite dashboard
- SQLite canonical configuration state
- nftables, pppd, dnsmasq, WireGuard and inadyn

Configuration follows one invariant:

```text
input → validate → generate → preflight → snapshot → apply → verify
      → commit or rollback/recovery
```

Generated service files are disposable. Validated canonical state is the source
of truth.

## User experience

The router should behave like an appliance rather than an enterprise cockpit.
The dashboard should make these answers obvious:

1. Is the Internet working?
2. Is the gateway healthy?
3. How many devices are connected?
4. Is remote access working?
5. Does the router itself need attention?

Advanced details remain available without dominating the default view.

## Platform direction

Current evidence is centered on x86-64 Alpine in Proxmox/KVM. ARM64 is covered by
build/QEMU smoke tests. Other hardware and hypervisors are not considered
supported until install, networking, recovery and performance are tested there.

## Production boundary

A future production recommendation requires, at minimum:

- repeated real PPPoE/reboot recovery;
- supported hardware/virtualization matrix;
- signed install and recovery media;
- stable migrations and update/rollback policy;
- backup restore evidence;
- external IPv4/IPv6 scanning and fault injection;
- sustained resource/performance measurements;
- independent security review;
- no unresolved critical/high-severity findings.

See [`ROADMAP.md`](ROADMAP.md) for the active gates and [`SECURITY.md`](SECURITY.md)
for the security model.
