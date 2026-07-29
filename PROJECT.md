# Minimal Router OS product vision

## Purpose

Minimal Router OS is a focused Linux router appliance for home and small-office
networks. It reuses proven Linux networking components and adds a small Go
control plane, a transactional configuration model, and a clear web interface.

It is not a new packet-processing stack and it does not target pfSense or OpenWrt
feature parity. The project intentionally trades breadth for a smaller set of
understandable, integrated workflows.

The current project is **early alpha**. This document describes direction and
acceptance criteria; it is not a promise that every target is implemented or
validated today. Current evidence and limitations are documented in `README.md`,
`ROADMAP.md`, and the dated files under `docs/`.

## Core principles

1. Less is better. Every feature must justify its attack surface and maintenance
   cost.
2. Reuse Linux components that already solve networking problems well.
3. Safe configuration, recovery, and honest failure are more important than
   clever automation.
4. Defaults must be secure, understandable, and opt-in for optional exposure.
5. The UI, API, and local tools are clients of the same typed configuration
   model.
6. Packet forwarding stays out of the Go management process.
7. Hypervisor-specific optimizations remain optional.
8. Performance and security claims require reproducible evidence.

## Intended user experience

A future supported release should let an administrator:

1. install a verified appliance image or package on supported hardware;
2. identify WAN and LAN interfaces without guessing Linux device names;
3. create an administrator password during first run;
4. enter optional PPPoE credentials;
5. review the proposed LAN address and DHCP pool;
6. apply the configuration with automatic rollback protection;
7. reach a concise dashboard that accurately reports router and service state;
8. recover through documented console or recovery-media procedures.

The wizard should ask only for information that cannot be safely inferred. Any
time target for installation must be measured on documented reference hardware
before it becomes a release claim.

## Current technology direction

- Alpine Linux
- Go management services
- React + TypeScript + Vite dashboard compiled to static assets
- SQLite canonical configuration store
- nftables firewall and NAT
- pppd for PPPoE
- dnsmasq for DHCP, DNS, and a basic global DNS blocklist
- WireGuard for remote access
- OpenRC service management

Optional integrations currently include bounded paths for Squid, QoS,
Cloudflare DDNS, and a hardware-dependent Wi-Fi access point. Optional features
remain disabled until explicitly configured.

## Control-plane architecture

The control plane is split into:

- `routerd`: unprivileged HTTPS/API, authentication, canonical state,
  transactions, audit events, and dashboard delivery;
- `router-applyd`: local privileged helper for fixed, typed, allowlisted system
  operations;
- `minimalrouter-mcp`: optional local MCP bridge whose default mode is read-only.

Every configuration mutation follows the same invariant:

```text
input → validation → typed model → generation → preflight → snapshot
      → apply → verification → commit or rollback
```

Generated service files are disposable artifacts. Canonical validated state is
the source of truth.

## Focused router scope

The intended focused workflow includes:

- one WAN and one LAN role;
- optional PPPoE WAN;
- LAN addressing and DHCP;
- DNS forwarding and a basic global blocklist;
- default-deny firewall and LAN-to-WAN NAT;
- WireGuard-first remote administration;
- live lease/status information;
- encrypted backup export and configuration snapshots;
- commit-confirmed protection for lockout-prone changes;
- optional Wi-Fi AP only on verified compatible hardware.

WAN web management and arbitrary WAN port forwarding are intentionally outside
the current secure appliance profile.

## User interface direction

The dashboard should prioritize:

- internet and PPPoE status;
- CPU, memory, disk, and uptime;
- WAN and LAN traffic;
- currently leased devices;
- firewall and WireGuard state;
- service health and explicit unavailable states;
- snapshots, backup, restore, and audit events.

Controls must not imply that an unimplemented or failed backend operation
succeeded. Decorative graphs and configuration complexity should be avoided when
they do not help an operator make a decision.

## Platform policy

Initial evidence focuses on:

- x86-64 Alpine Linux in a Proxmox/KVM-style VM;
- ARM64 development VMs for selected integration and resource tests.

Bare metal, VMware, Hyper-V, VirtualBox, additional ARM devices, and other
hypervisors remain targets until each has a documented install, boot,
networking, rollback, recovery, and performance result. A platform is not
supported merely because the binaries compile for its CPU architecture.

## Performance policy

The project aims for a smaller idle resource footprint than broad firewall
platforms because it intentionally provides fewer services. Current VM
measurements are evidence for those exact environments only.

Before publishing a performance claim, record:

- exact CPU, NIC, memory, storage, and virtualization environment;
- software commit and configuration;
- test commands and traffic pattern;
- idle and sustained CPU/memory use;
- throughput, latency, packet loss, thermals, and management responsiveness;
- comparison limitations.

Near-term validation targets are reliable 1 GbE operation on reference hardware
and measured WireGuard/PPPoE cost. 2.5 GbE and 10 GbE are research targets, not
current support promises.

## Explicit exclusions for the current release line

- pfSense feature parity
- multi-WAN
- high availability/CARP
- IDS/IPS
- captive portal
- BGP or OSPF
- OpenVPN or IPsec
- arbitrary WAN port forwarding
- Docker or Kubernetes on the router
- a general third-party package platform
- full AdGuard Home feature parity

Adding an excluded feature requires a product decision, threat review,
maintenance plan, failure/rollback design, and evidence that it belongs in a
small appliance.

## Public alpha acceptance criteria

A public early-alpha repository must:

- contain only reviewed source in a clean one-commit repository boundary;
- expose no private development history, repository metadata, credentials,
  runtime state, or real network inventory;
- pass repository hygiene, Go race tests, vet, dashboard lint/build, CodeQL, a
  clean Alpine install, and current/full-history secret scans;
- clearly state that it is experimental and community-supported;
- provide accurate installation, contribution, support, security, and rollback
  documentation;
- avoid production, performance, or security claims that exceed its evidence.

## Production-readiness criteria

A future release may be recommended as a household production router only after
it has:

- a documented supported hardware/hypervisor matrix;
- reproducible signed installation and recovery artifacts;
- stable configuration migrations and upgrade/rollback procedures;
- real ISP PPPoE, DHCP, DNS, NAT, WireGuard, reboot, and backup/restore evidence;
- external IPv4/IPv6 scanning and fault-injection results;
- published sustained resource and performance measurements;
- bounded log and disk-pressure behavior;
- an independent focused security review;
- a documented support and security-update policy;
- no unresolved critical or high-severity security findings.

See `SECURITY.md` and `ROADMAP.md` for the detailed gates.
