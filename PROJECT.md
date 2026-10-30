# Product direction

Minimal Router OS is a focused Alpine Linux router appliance for home and
small-office networks. It combines proven Linux networking components with a
small Rust control plane, transactional configuration and a clear web UI.

> **Status: Beta — v0.4.0.** The preferred AMD64/Proxmox install is the
> CI-built Golden Appliance ISO. Current evidence and remaining production gates
> are tracked in [`docs/CURRENT_VALIDATION.md`](docs/CURRENT_VALIDATION.md).

## Principles

- Keep the feature set small and understandable.
- Reuse mature Linux networking components.
- Keep packet forwarding out of the management process.
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
- device visibility, per-device Internet pause and DNS filtering;
- snapshots, encrypted backups and rollback;
- gateway quality, availability history and appliance-health monitoring;
- optional Squid, QoS and Wi-Fi AP support.

WAN web management and arbitrary WAN port forwarding are intentionally outside
the current secure appliance profile.

## Appliance installation model

v0.1.4 introduced the Golden Appliance ISO for AMD64. The user VM is not an
Alpine build host. CI constructs the target Alpine rootfs, matching `linux-lts`
kernel/modules/initramfs, MinimalRouter runtime and bootable disk image. The live
ISO verifies and raw-copies that disk image, then the installed appliance collects
WAN/LAN and credentials on first boot.

v0.4.0 keeps that architecture and the same release gate: installed cold boot,
firstboot non-reentry, service-supervision recovery, warm reboot and
destructive installer-safety checks.

This reduces the first-install path to a small, auditable flasher and avoids live
repository access, target chroots and kernel/module mismatches.

## Technology

- Alpine Linux + OpenRC
- Rust (`routerd`, `router-applyd`, recovery/update tools)
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

Current evidence is centered on x86-64 in Proxmox/KVM. The v0.4.0 Golden target
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

See the [roadmap](#roadmap), [`docs/GOLDEN-IMAGE.md`](docs/GOLDEN-IMAGE.md) and
[`SECURITY.md`](SECURITY.md).

## Roadmap

Minimal Router OS is currently **Beta (v0.4.0)**. The roadmap is organized around
evidence and release gates rather than dates.

Current evidence: [`docs/CURRENT_VALIDATION.md`](docs/CURRENT_VALIDATION.md).

### Implemented baseline

- [x] Alpine packaging and OpenRC services
- [x] `routerd` / `router-applyd` privilege separation
- [x] SQLite canonical config, validation and deterministic generation
- [x] transactional apply, confirmation, rollback and recovery
- [x] nftables firewall/NAT, PPPoE, DHCP/DNS and WireGuard
- [x] No-IP / Cloudflare DDNS, DNS Filter, QoS and optional Wi-Fi/Squid
- [x] secure sessions, CSRF, rate limits, TOTP and local recovery
- [x] encrypted backup and snapshots
- [x] crash-safe A/B update/rollback with signed-manifest support
- [x] bounded storage/logging and aggregate appliance health
- [x] gateway monitoring, live bandwidth and connected-device dashboard
- [x] per-device Internet pause with bounded runtime enforcement
- [x] sampled gateway availability/outage and public-IP history
- [x] fixed service-recovery actions through the privileged helper
- [x] production dashboard + GitHub Pages demo on the same visual system
- [x] pushed mobile navigation and horizontal startup timeline
- [x] Go/frontend/security/Alpine/QEMU/network-lab CI
- [x] AMD64 Golden Appliance ISO: CI-built Alpine + `linux-lts` + MinimalRouter
- [x] safe raw-image flasher with checksum verification and reinstall guard
- [x] VGA/noVNC install path plus persistent `ttyS0` 115200 recovery
- [x] blank-disk QEMU E2E: flash, reboot, firstboot, serial, SSH, firewall,
      router services and Dashboard verification
- [x] installed cold-boot, firstboot non-reentry, supervision recovery, warm
      reboot and destructive installer-safety regression coverage
- [x] release pipeline capable of publishing the tested Golden ISO from the
      signed AMD64 release payload with checksum and GitHub attestation

### Proven on the first real Proxmox pilot

- [x] real PPPoE and Internet forwarding
- [x] recorded 570/327 Mbps throughput sample
- [x] 0% loss in the recorded 600-packet test
- [x] external phone WireGuard handshake
- [x] dashboard access through WireGuard
- [x] successful pfSense fallback
- [x] `linux-lts` identified as the working PPPoE kernel path

### Next pilot gates

A Hyper-V lab test on 2026-10-14 (not a qualified platform) already exercised
encrypted-backup restore into a fresh VM, abrupt power loss, a full disk,
conntrack exhaustion, repeated PPPoE loss and WireGuard after reboot; see
[`docs/CURRENT_VALIDATION.md`](docs/CURRENT_VALIDATION.md). The gates below still
need owner Proxmox or real hardware.

- [ ] repeat the current Golden ISO on owner Proxmox from blank disk through real WAN cutover
- [ ] five repeated guest/host cold boots with stable WAN/LAN identity
- [ ] repeated real PPPoE disconnect/reconnect and reboot recovery
- [ ] MinimalRouter-managed No-IP update and later public-IP change
- [ ] WireGuard recovery after real PPPoE reconnect/reboot
- [ ] timed device-pause expiry/resume on a real LAN client
- [ ] encrypted backup restore into a fresh VM
- [ ] external IPv4/IPv6 scan
- [ ] sustained throughput, packet rate, latency/loss and thermal measurements
- [ ] full-disk, inode, read-only-filesystem and abrupt-power tests
- [ ] seven-day continuous pilot with bounded resource growth

### Platform / media gates

- [ ] full installed-disk UEFI qualification; the current E2E target is SeaBIOS/MBR
- [ ] owner-qualified recovery-media procedure
- [ ] supported Proxmox/NIC matrix
- [ ] ARM64 appliance-image/installer path if it becomes a supported target

### Production gates

- [ ] independent focused security review
- [ ] stable migration/update policy
- [ ] security-update/support policy
- [ ] no unresolved critical/high-severity findings

Until these pass, Minimal Router remains a controlled pilot rather than an
unattended pfSense/OpenWrt replacement.

### Deferred

Multi-WAN/HA, IDS/IPS, captive portal, BGP/OSPF, OpenVPN/IPsec, arbitrary WAN port
forwarding, container hosting and a general third-party package ecosystem are
outside the current small-router scope.

## Compared with pfSense and OpenWrt

This document is a scope and resource comparison, not a claim of security,
feature, or support parity.

Minimal Router OS is **Beta (v0.4.0)** software with limited deployment history.
pfSense and OpenWrt are mature projects used on a wide range of production
networks. A smaller code and service footprint can reduce complexity, but it
does not by itself prove that a system is safer, faster, or more reliable.

### Summary

| Area | Minimal Router OS | pfSense | OpenWrt |
|---|---|---|---|
| Current maturity | Beta / controlled pilot | Mature production firewall platform | Mature embedded router distribution |
| Primary goal | Focused home/small-office appliance with a narrow UI and safe transaction model | Broad firewall, routing, VPN, HA, and package capabilities | Flexible embedded routing across a large hardware ecosystem |
| Base system | Alpine Linux | FreeBSD | Linux-based embedded distribution |
| Management | React dashboard and Go REST API | Mature web configurator | LuCI and UCI |
| Extensibility | Deliberately limited | Optional package system | Large package ecosystem |
| Production support | Community best effort; no unattended-production claim | Community and commercial Netgate options | Community and vendor/device-dependent options |

### Resource guidance

The numbers below are not identical workload benchmarks.

| Metric | Minimal Router OS | pfSense |
|---|---|---|
| RAM | One ARM64 development VM measured about 140 MiB idle and about 203 MiB after setup/configuration work. 512 MiB is the tested development minimum; 1 GiB is recommended for comfortable headroom. | Netgate documents 1 GiB or more as the minimum. Packages, state count, VPN, IDS/IPS, ZFS, and traffic can require more. |
| Disk | The application payload is small, but 8 GiB is currently recommended for Alpine, logs, snapshots, packages, and upgrades. | Netgate documents 8 GB or more as the minimum. |
| CPU / throughput | A first owner-Proxmox comparison recorded 570/327 Mbps through Minimal Router and 543/318 Mbps through the pfSense fallback in one environment. This is one sample, not a general cross-platform benchmark. | Requirements depend on throughput, VPN cryptography, packages, state count, and traffic features. |
| Dashboard runtime | Static assets served by the Go process; Node.js is not installed on the router. | Integrated web configurator and the services required by the platform. |

A lower measured memory footprint is expected because Minimal Router OS supports
far fewer features. It should not be interpreted as equivalent functionality at
a lower cost.

Official pfSense references:

- https://docs.netgate.com/pfsense/en/latest/hardware/minimum-requirements.html
- https://docs.netgate.com/pfsense/en/latest/hardware/size.html

### DNS filtering and ad blocking

Minimal Router OS includes a basic global DNS sinkhole/blocklist plus scheduled
DNS Filter device profiles. This is useful for focused household policy without
installing a second application.

It is **not** a complete implementation of AdGuard Home. Current limitations
include the absence of its full query-log experience, client policies, extensive
filter management, and other advanced features.

On pfSense, administrators commonly add DNSBL capabilities with the optional
`pfBlockerNG` package or operate a separate DNS filtering service such as
AdGuard Home or Pi-hole. pfSense deliberately keeps many such capabilities in
its package ecosystem rather than its base installation.

Official pfSense package references:

- https://docs.netgate.com/pfsense/en/latest/packages/
- https://docs.netgate.com/pfsense/en/latest/packages/pfblocker.html

### Feature comparison

| Capability | Minimal Router OS — v0.4.0 Beta | pfSense |
|---|---|---|
| Stateful firewall and NAT | Focused generated nftables policy | Comprehensive and mature |
| PPPoE | Implemented; first real owner-Proxmox pilot passed, repeatability testing remains | Mature |
| DHCP and DNS | Implemented with dnsmasq | Mature resolver/forwarder and DHCP capabilities |
| WireGuard | Implemented for the focused remote-access workflow | Supported with broader configuration options |
| WAN web management | Intentionally unavailable | Configurable by the administrator, though secure deployment normally avoids direct exposure |
| Port forwarding | Supported only for the secure WireGuard-ingress profile; arbitrary WAN/PPPoE DNAT is not exposed | Fully supported |
| VLANs | Not yet a stable workflow | Mature |
| Multi-WAN | Not implemented | Mature |
| High availability | Not implemented | CARP and established HA workflows |
| IPv6 | Disabled and blocked until feature parity is complete | Mature IPv6 support |
| IDS/IPS | Not implemented | Available through packages such as Snort and Suricata |
| DNS Filter | Basic integrated sinkhole + scheduled device profiles | Commonly provided through pfBlockerNG or another service |
| Wi-Fi AP | Optional, hardware-dependent, disabled by default | Usually delegated to separate access points; platform/hardware dependent |
| Configuration rollback | Transaction, commit-confirm, snapshots and local recovery | Mature configuration backup and recovery workflows |
| Signed updates | Signed-manifest A/B staging/activation/rollback | Mature package/system update workflow |
| Package ecosystem | No general ecosystem | Extensive optional package repository |
| Hardware/deployment history | Limited CI/lab evidence plus one recorded owner-Proxmox pilot | Extensive real-world deployment history |

### Design differences

#### Minimal Router OS

- uses a narrow typed configuration model;
- separates unprivileged `routerd` from privileged `router-applyd`;
- generates owned service configuration deterministically;
- uses preflight, snapshots, verification, and rollback for changes;
- keeps WAN management closed and uses WireGuard for remote access;
- favors a small number of integrated workflows over flexibility.

#### pfSense

- covers substantially more firewall and routing scenarios;
- has a mature UI, documentation set, package system, and support ecosystem;
- supports production features such as VLANs, multi-WAN, IPsec, HA, advanced
  policy routing, extensive NAT, and larger deployment topologies;
- requires more resources partly because it provides a much broader platform.

#### OpenWrt

- targets a large range of embedded router hardware;
- offers a mature package ecosystem and flexible UCI configuration;
- can have an extremely small footprint depending on the device and image;
- is a more appropriate comparison when hardware constraints and embedded Wi-Fi
  support are primary requirements.

### Choosing a platform

Choose **pfSense** when production maturity, broad firewall features, HA,
multi-WAN, VLAN-heavy environments, commercial support, or a large package
ecosystem are required.

Choose **OpenWrt** when broad embedded-device support, integrated wireless
hardware, and a mature lightweight Linux router ecosystem are most important.

Experiment with **Minimal Router OS** when the goal is to help validate a
focused, resource-efficient home-router appliance and the network can tolerate
Beta software, console access, controlled pilots, and rollback to an established
router.

Minimal Router OS should earn production trust through reproducible tests,
external review, hardware evidence, recovery drills, and stable releases—not
through comparison-table claims. See [`CURRENT_VALIDATION.md`](docs/CURRENT_VALIDATION.md)
for the current evidence boundary.
