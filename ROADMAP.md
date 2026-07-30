# Roadmap

Minimal Router OS is an early-alpha project. This roadmap is organized around
recorded evidence and safety gates, not promised dates.

A checked item means the implementation exists and passed the listed development
tests. It does not imply production readiness on every host, NIC, ISP, or network.

Current evidence: [`docs/CURRENT_VALIDATION.md`](docs/CURRENT_VALIDATION.md).

## Current automated baseline

- [x] Alpine Linux packaging and OpenRC services
- [x] split unprivileged `routerd` and privileged `router-applyd`
- [x] SQLite canonical store, migrations, typed validation, and deterministic
      generators
- [x] snapshot/apply/verify/commit-or-rollback configuration lifecycle
- [x] default-deny WAN firewall and LAN-to-WAN NAT
- [x] PPPoE, DHCP, DNS, WireGuard, QoS, DDNS, Wi-Fi, and DNS Filter paths
- [x] first-run HTTPS wizard and interface confirmation
- [x] Argon2id, secure sessions, CSRF, rate limiting, TOTP, and local recovery
- [x] encrypted backup export and restore validation
- [x] crash-safe A/B update activation and rollback with durable journal
- [x] signed manifests, SHA-256 checks, SPDX SBOMs, and provenance support
- [x] Go race/vet/vulnerability tests, CodeQL, secret scan, and repository hygiene
- [x] frontend lint, unit, TypeScript 6 build, dependency audit, and Playwright E2E
- [x] clean Alpine install, wizard, update activation, and rollback CI
- [x] repeated interrupted-update recovery tests
- [x] API and update-journal fuzzing
- [x] `gosec`, `shellcheck`, `actionlint`, and Linux binary inspection
- [x] ARM64 build and QEMU smoke test
- [x] isolated WAN-router-LAN DHCP/DNS/NAT/firewall/traffic laboratory
- [x] control-plane benchmark and allocation artifacts
- [x] GitHub core CI actions and artifact upload moved to v7
- [x] dashboard TypeScript 6.0.3 and Node type definitions 26.1.2

## Alpha pilot — actual Proxmox VM

Goal: prove the minimum router workflow on the target Proxmox host without risking
the production network.

- [ ] identify and document the candidate VM and NIC/bridge roles read-only
- [ ] confirm isolated LAN and test/NAT WAN topology
- [ ] preserve pfSense rollback independent of the candidate
- [ ] create known-good application backup and Proxmox snapshot
- [ ] record exact host/guest versions and installed commit
- [ ] complete five graceful guest reboot cycles
- [ ] complete repeated Proxmox shutdown/start cycles
- [ ] prove stable WAN/LAN role reconciliation
- [ ] prove DHCP, DNS, NAT, HTTPS management, and default-deny WAN behavior
- [ ] prove unconfirmed disruptive-change rollback and recovery-console access
- [ ] activate a verified update, reboot, validate, and explicitly roll back
- [ ] restore an encrypted backup into a fresh VM

Exit criterion: the VM survives repeated boot, configuration, update, rollback,
and restore tests without manual file repair or loss of management access.

## Alpha performance — target-host evidence

Goal: replace same-kernel CI ceilings with reproducible Proxmox and NIC results.

- [ ] record Proxmox version, host kernel, guest kernel, vCPU, RAM, disk, and NIC
      model
- [ ] document VirtIO queues, offloads, bridges, and traffic-generator placement
- [ ] measure boot-to-forwarding-ready and management-ready
- [ ] measure idle and loaded CPU/RAM
- [ ] measure routing/NAT throughput and packets per second
- [ ] measure latency, jitter, retransmits, and packet loss under load
- [ ] verify management responsiveness during traffic
- [ ] measure real PPPoE connect/reconnect, MTU, and CPU cost
- [ ] measure real WireGuard throughput and CPU cost
- [ ] measure QoS behavior under load
- [ ] measure 1 GbE and, where available, 2.5 GbE
- [ ] investigate 10 GbE only after lower-speed evidence is stable
- [ ] record log, snapshot, and disk growth

Exit criterion: every published number links to exact hardware, commands,
configuration, duration, raw summary, and limitations.

## Alpha recovery and fault injection

- [x] automated interrupted update/rollback journal reconciliation
- [x] automated corrupt journal and malformed API fuzzing
- [x] automated signed package and clean-install rollback tests
- [ ] service-crash tests on the target VM
- [ ] full-disk and inode-exhaustion test on a disposable clone
- [ ] read-only-filesystem test on a disposable clone
- [ ] corrupt state/snapshot restore rehearsal
- [ ] abrupt Proxmox host/guest power-loss test after graceful tests pass
- [ ] backup restore into a completely new VM
- [ ] seven-day continuous pilot with bounded logs and stable memory

Exit criterion: every failure returns to a documented known-good state and pfSense
can be restored immediately.

## External and production gates

- [ ] real ISP PPPoE during a maintenance window
- [ ] WireGuard from an unrelated external network
- [ ] external IPv4 scan from an unrelated host
- [ ] external IPv6 scan or documented fail-closed result
- [ ] owner-signed install and recovery media
- [ ] independent focused security review
- [ ] supported Proxmox/NIC matrix
- [ ] stable migration and upgrade policy
- [ ] security-update and support policy
- [ ] no unresolved critical or high-severity findings

Until these gates pass, the project remains a controlled pilot rather than an
unattended pfSense replacement.

## Deferred or outside current scope

- pfSense feature parity
- multi-WAN and high availability
- IDS/IPS
- captive portal
- BGP or OSPF
- OpenVPN or IPsec
- arbitrary WAN port forwarding
- Docker or Kubernetes on the router
- general third-party package ecosystem
- full AdGuard Home feature parity

Adding any deferred feature requires a product decision, threat review,
maintenance plan, and recorded evidence that it belongs in a small home-router
appliance.
