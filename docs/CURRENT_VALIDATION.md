# Current validation status

This is the short source of truth for what is proven today. Automated CI
evidence, lab evidence and real owner-Proxmox/ISP evidence are kept separate.
Per-release history is in [`CHANGELOG.md`](../CHANGELOG.md); older dated reports
are in the git history.

## Current release: v0.3.1

**Minimal Router OS v0.3.1 — Beta**, published 2026-10-14
([release notes](releases/v0.3.1.md)). It fixed what a live test of v0.3.0
found, added the **Force all devices through router DNS** switch and the
narrow-screen dashboard layouts.

v0.3.1 changed `router-recovery` and `router-setup`, so v0.3.0 and earlier
appliances take it once through the signed full distribution installer; the
dashboard updater refuses that transition by design (see
[`WEB-UPDATE.md`](WEB-UPDATE.md)).

### CI evidence

Every required workflow passed for the release, including the signed release
workflow and **Build, flash and boot minimalrouter golden ISO**, whose blank-disk
flash uses an emulated e1000 LAN port. This is CI evidence, not lab validation.

### Hyper-V lab evidence (2026-10-14)

Windows 11 Hyper-V, Gen 1 (BIOS) VMs with 2 vCPU and 2 GiB, a simulated PPPoE
ISP VM and a LAN client VM. Hyper-V is **not** a qualified platform, and none of
this is physical-router, real-ISP or Proxmox evidence.

Release and upgrade:

- all published assets match `SHA256SUMS`; the tag carries a good signature
  from the release signing key;
- full signed installer on an existing v0.3.0-line appliance: preflight refuses a
  tampered web file, binary, manifest or key; the upgrade keeps the configuration
  byte-identical and all snapshots, and installs the firmware trust key;
- downgrade to v0.3.0 refused by preflight and by `install.sh`;
- the dashboard refuses downgrade, same-version, tampered-signature, wrong-
  architecture and corrupt uploads without leaving a staged slot.

Fresh install from the v0.3.1 ISO:

- serial `boot:` prompt and installer; invalid disk paths, the CD-ROM and a
  shell-injection attempt rejected; lowercase `erase` cancels with the disk
  untouched; the ISO refuses an occupied disk;
- Hyper-V NICs found without manual steps; PPPoE discovery picks the WAN; Enter
  alone does not accept a PPPoE-based suggestion; short or mismatched Dashboard
  passwords rejected;
- encrypted backup from the old appliance restored onto the fresh install.

Function, resilience and security:

- DNS filter with 516,295 domains applied in about 4 s; WAN MTU, upstream DNS,
  device-profile and domain changes all applied without rollback; DNS
  enforcement blocks other resolvers and DNS-over-TLS; device pause survives a
  power cut; DNS risk alerts raised for gambling and adult lookups;
- Internet-only outage recorded with cause `internet`, PPPoE loss with cause
  `pppoe_link`; auto-recovery after 3 minutes; a 2-hour clock step leaves
  snapshots and gateway history correctly ordered;
- unconfirmed trusted-network change rolled back after 90 s; self-lockout and
  WAN exposure changes refused; port forwards only through WireGuard;
- hard power-off: journal replay, healthy again in about 75 s with all state
  intact; `kill -9` of `routerd`/`router-applyd` recovered in about 1 s;
  conntrack table full (131,072) with the API still responsive; at 97 % and
  100 % disk usage routing, DHCP and DNS continue and configuration writes are
  refused;
- WAN scan: all 65,535 TCP ports filtered, no ICMP reply; LAN: TCP 22/53/8443
  and UDP 53/67 only; TLS 1.0/1.1 refused; sessions, CSRF, Origin and Host checks,
  login rate limiting, TOTP with replay protection, password change and the
  `router-recovery reset-auth` path all behave as documented;
- measured through PPPoE + NAT + firewall: about 0.78 Gbit/s upload and
  1.07 Gbit/s download; CAKE QoS held 96.5/19.3 Mbit/s at a 100/20 setting;
  24,000 DNS queries in 19 s without a timeout.

Findings from this test that are fixed on `main` and not yet released: sign-in
refused at ≥90 % disk, WireGuard peers unable to sign in with the default trusted
networks, an off-centre confirmation dialog, a stale `/etc/minimalrouter/VERSION`
after the full installer, and a misleading DNS risk message while lists download.
Still open and deferred to a release that needs the full installer: firstboot
accepts a very short recovery/SSH root password, the audit log is ordered by
wall-clock time, and killed service supervisors leave orphans that an OpenRC
restart cannot clear.

An earlier Hyper-V run on 2026-10-13 established the v0.3.0 findings that v0.3.1
fixed.

No v0.3.1 owner-Proxmox, ISP or physical-hardware evidence has been collected,
and no signed A/B dashboard update to a newer release has been performed on an
appliance yet.

## Golden Appliance ISO: automated evidence

The `Appliance ISO` workflow boots the production ISO and proves, on every
candidate:

- the Golden image checksum and gzip integrity; the flasher starts by itself;
- one clearly virtual QEMU disk is auto-selected and raw-copied (8 GiB VirtIO);
- the VM boots the installed `linux-lts` appliance and loads non-VirtIO NIC
  drivers (e1000 LAN);
- firstboot completes over `ttyS0`; serial root login and a real
  password-authenticated SSH login work;
- LAN `192.168.1.1/24`, the trusted-LAN SSH rule, `routerd` readiness and the
  Dashboard listener on TCP/8443;
- installed cold boot without the ISO, no firstboot re-entry, supervised
  recovery of a forced `routerd` crash, warm reboot to the same ready state;
- an existing installation is refused, and an undersized 4 GiB target is rejected
  before any destructive write.

The fully exercised installed-disk target is AMD64, QEMU/KVM, SeaBIOS, MBR +
ExtLinux, an 8 GiB VirtIO disk and a serial-driven E2E. The ISO carries UEFI boot
metadata, but the installed Golden disk is **not** qualified for UEFI.

## Real Proxmox evidence (2026-08-01)

A controlled owner-Proxmox pilot carried real Internet traffic through Minimal
Router for about 27 minutes and then returned to pfSense.

| Test | Minimal Router | pfSense |
|---|---:|---:|
| Download | **570 Mbps** | 543 Mbps |
| Upload | **327 Mbps** | 318 Mbps |
| Packet loss (600 packets) | **0%** | **0%** |
| Ping 1.1.1.1 | 2.77 ms | **1.94 ms** |
| Ping 8.8.8.8 | 8.54 ms | **7.61 ms** |
| DNS (200 queries) | **12.65 ms, 200/200** | 13.00 ms, 200/200 |
| RAM after test | **172 MB** | — |

Real PPPoE and forwarding, an external phone WireGuard handshake, Dashboard
access through WireGuard and the pfSense fallback (about 93 s) all passed. The
`linux-virt` guest lacked the PPPoE module; `linux-lts` provided it, which is why
the appliance standardises on `linux-lts`. MinimalRouter-managed No-IP and later
public-IP propagation still need a real-provider rerun.

## Isolated Proxmox lab evidence (2026-08-06 and 2026-08-08)

- PPPoE CHAP and PAP negotiation, private/CGNAT WAN address with safe local
  egress, and reboot with PPPoE session recovery: **PASS**.
- Torture scenarios 18–25: WireGuard recovery after an endpoint blackhole,
  extra-LAN isolation, full reboot recovery, `routerd` + `router-applyd` crash
  recovery, transaction fault-hook power-loss phases, signed update with
  rollback, and an interrupted activation cold-booting to last-good: **PASS**.

## Automated validation outside the ISO path

Repository workflows cover `go test -race`, `go vet`, vulnerability and secret
scans, CodeQL, frontend lint/unit/build/Playwright, clean Alpine install and
update/rollback, transaction crash/recovery, ARM64 QEMU smoke tests, an isolated
WAN-router-LAN network lab, storage pressure, appliance health, service
supervision and control-plane benchmarks. They do not replace real ISP, NIC,
thermal, power-loss or endurance validation.

## Remaining gates before unattended production use

1. install the current Golden ISO from blank disk on owner Proxmox and repeat the
   real WAN cutover;
2. repeated guest/host cold boots with stable WAN/LAN mapping;
3. repeated real PPPoE disconnect/reconnect and reboot recovery;
4. MinimalRouter-managed No-IP update and a later public-IP change;
5. WireGuard recovery after real PPPoE reconnect and reboot;
6. timed device-pause expiry on a real LAN client;
7. encrypted backup restore into a fresh VM on the target platform;
8. external IPv4/IPv6 scanning from the real Internet;
9. full-disk, inode, read-only-filesystem and abrupt-power tests on the target
   platform;
10. sustained throughput, packet rate, latency/loss and thermal measurements;
11. installed-disk UEFI qualification if UEFI is to be supported;
12. at least seven days of stable unattended operation;
13. an independent focused security review.

Items 7–9 have Hyper-V lab evidence (above), which does not substitute for the
target platform.

## Recommendation

v0.3.1 is suitable for a **controlled Proxmox pilot** with noVNC or serial
recovery and a known-good router ready for rollback. It is not an unattended
pfSense/OpenWrt replacement until the gates above pass.

Detailed procedures: [`GOLDEN-IMAGE.md`](GOLDEN-IMAGE.md),
[`INSTALLATION.md`](INSTALLATION.md), [`FAILURE_SCENARIOS.md`](FAILURE_SCENARIOS.md),
[`RECOVERY.md`](RECOVERY.md).
