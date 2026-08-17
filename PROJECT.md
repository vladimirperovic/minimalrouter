# Product direction

Minimal Router OS is a focused Alpine Linux router appliance for home and
small-office networks. It combines proven Linux networking components with a
small Go control plane, transactional configuration and a clear web UI.

> **Status: Beta — v0.1.4.** The preferred AMD64/Proxmox install is the
> CI-built Golden Appliance ISO. Current evidence and remaining production gates
> are tracked in [`docs/CURRENT_VALIDATION.md`](docs/CURRENT_VALIDATION.md).

## Principles

- Keep the feature set small and understandable.
- Reuse mature Linux networking components.
- Keep packet forwarding out of the Go management process.
- Prefer safe, reversible configuration over clever automation.
- Fail closed when runtime state cannot be proven.
- Make optional exposure explicit and opt-in.
- Require reproducible evidence for performance and security claims.
- Treat installation and recovery as part of the security boundary.

## Core scope

The current product focuses on:

- one WAN and one LAN role;
- PPPoE WAN;
- DHCP and DNS;
- default-deny firewall and LAN-to-WAN NAT;
- WireGuard remote access;
- No-IP / Cloudflare Dynamic DNS;
- device visibility and DNS filtering;
- snapshots, encrypted backups and rollback;
- gateway quality and appliance-health monitoring;
- optional Squid, QoS and Wi-Fi AP support.

WAN web management and arbitrary WAN port forwarding are intentionally outside
the current secure appliance profile.

## Appliance installation model

v0.1.4 introduced the Golden Appliance ISO for AMD64. The user VM is not an
Alpine build host. CI constructs the target Alpine rootfs, matching `linux-lts`
kernel/modules/initramfs, MinimalRouter runtime and bootable disk image. The live
ISO verifies and raw-copies that disk image, then the installed appliance collects
WAN/LAN and credentials on first boot.

This reduces the first-install path to a small, auditable flasher and avoids live
repository access, target chroots and kernel/module mismatches.

## Technology

- Alpine Linux + OpenRC
- Go (`routerd`, `router-applyd`, recovery/update tools)
- React + TypeScript + Vite dashboard
- SQLite canonical configuration state
- nftables, pppd, dnsmasq, WireGuard and inadyn
- ExtLinux/MBR for the currently qualified Golden installed-disk path

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

Current evidence is centered on x86-64 in Proxmox/KVM. The v0.1.4 Golden target
is fully exercised end-to-end on the SeaBIOS/MBR + VirtIO path. The installer
media retains BIOS/UEFI boot metadata, but UEFI installed-disk qualification is a
separate future gate. ARM64 distribution builds and QEMU smoke tests remain
available; there is no ARM64 Golden ISO yet.

## Production boundary

A production recommendation still requires, at minimum:

- repeated real PPPoE/reboot recovery;
- supported hardware/virtualization matrix;
- owner-qualified recovery media and UEFI target qualification where claimed;
- stable migrations and update/rollback policy;
- backup restore evidence;
- external IPv4/IPv6 scanning and fault injection;
- sustained resource/performance measurements;
- independent security review;
- no unresolved critical/high-severity findings.

See [`ROADMAP.md`](ROADMAP.md), [`docs/GOLDEN-IMAGE.md`](docs/GOLDEN-IMAGE.md) and
[`SECURITY.md`](SECURITY.md).
