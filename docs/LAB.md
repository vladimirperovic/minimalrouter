# Minimal Router — isolated Proxmox lab evidence (sanitized, 2026-08)

> **Historical lab evidence.** This file intentionally contains no live host
> addresses, VM IDs, bridge names, credentials, MAC addresses, private hostnames,
> local filesystem paths, or household inventory. Use
> [`CURRENT_VALIDATION.md`](CURRENT_VALIDATION.md) for the current v0.1.5 evidence
> boundary and [`PROXMOX_ISOLATED_LAB.md`](PROXMOX_ISOLATED_LAB.md) for the
> reproducible test matrix.

## Purpose

The isolated lab was created to exercise Minimal Router against realistic ISP
and LAN behavior without placing a test router on the production Layer-2
network. It uses disposable virtual networks and synthetic/test-only identities.

Generic topology:

```text
Internet / isolated upstream NAT
            |
      ISP simulator VM
      PPPoE AC + netem
            |
      isolated WAN bridge
            |
     Minimal Router VM
            |
      isolated LAN bridge
            |
       LAN client VM

Optional isolated bridges may provide a remote WireGuard peer or ExtraLAN test
segment. They must never reuse the production LAN as a test segment.
```

## Evidence preserved from the lab

The August 2026 lab established useful regression evidence for the project,
including:

- PPPoE establishment against an isolated access concentrator;
- PAP/CHAP interoperability in the generated PPP configuration;
- DHCP, DNS, IPv4 forwarding and NAT through the candidate router;
- default-deny firewall behavior on the isolated WAN;
- WireGuard lifecycle and route cleanup tests;
- transaction confirmation/rollback behavior;
- service readiness and canonical-state reconciliation;
- signed A/B update staging, activation and rollback exercises;
- forced service/process failures and recovery-state handling;
- host-driven hard-power interruption windows for persistence/recovery tests;
- storage-pressure/fault-injection scenarios on disposable state;
- bounded lab monitoring without putting test interfaces on the production LAN.

These lab results are regression evidence, not a production-readiness claim.
Real ISP, owner-Proxmox, external scan, backup restore, hardware and soak evidence
are tracked separately in `CURRENT_VALIDATION.md` and dated sanitized reports.

## v0.1.5 deployment rule

For a fresh v0.1.5 appliance test, prefer the same Golden Appliance ISO path used
by the release gate:

1. create a blank isolated VM with two deliberate test NIC roles;
2. boot the verified `minimalrouter-0.1.5-amd64.iso`;
3. let the Golden-image flasher install to the blank disk;
4. complete installed firstboot on the selected console;
5. verify LAN management and core services before attaching the isolated WAN;
6. run the relevant scenarios from `PROXMOX_ISOLATED_LAB.md`;
7. use only signed update payloads for A/B update/rollback testing.

The clean-Alpine/archive installation path remains useful in CI and development,
but it is not the preferred end-user v0.1.5 Proxmox install path.

## Lab safety rules

- Keep every test WAN/LAN/ExtraLAN interface on explicitly isolated virtual
  networks.
- Keep the known-good router and recovery console independent from the candidate.
- Use synthetic PPPoE, WireGuard, DDNS, device and host identities in committed
  files and screenshots.
- Never commit real PPPoE credentials, provider tokens, root/admin passwords,
  WireGuard private material, public IP addresses, private hostnames, VM
  inventory, bridge mappings, MAC addresses, packet captures, runtime databases,
  backups, or generated secret-bearing configs.
- Capture only redacted evidence needed to prove a scenario.
- Trigger hard-power scenarios from the hypervisor/test harness, not by granting
  an unprivileged router process extra shutdown authority.
- Stop at the first unexplained fail-open, recovery-state ambiguity or management
  lockout and restore the isolated known-good state before continuing.

## Useful generic checks

After a scenario, collect a redacted baseline such as:

```sh
ip -br link
ip -4 addr
ip -4 route
nft list table inet minimalrouter
rc-service router-applyd status || true
rc-service routerd status || true
rc-service dnsmasq status || true
rc-service pppoe-wan status || true
wg show || true
router-update status || true
```

Do not paste the raw output into the public repository. Replace identifying
addresses/names with documentation examples and remove all secret material first.

## Current source of truth

The detailed operator matrix lives in
[`PROXMOX_ISOLATED_LAB.md`](PROXMOX_ISOLATED_LAB.md). The release-facing status,
including which automated and owner-Proxmox gates have actually passed, lives in
[`CURRENT_VALIDATION.md`](CURRENT_VALIDATION.md). This historical lab summary
must not be used to claim broader v0.1.5 production support.
