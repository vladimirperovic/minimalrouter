# Current validation status — 2026-08-01

This document is the current source of truth for automated validation, recorded
target-host evidence, and remaining manual release gates. Dated hardware reports
remain historical evidence; successful pilot results must not be generalized
beyond the environment and duration in which they were observed.

## Repository baseline

The current tree includes:

- crash-safe A/B activation and rollback with a durable update-operation journal;
- independent bootstrap executables for update and recovery;
- durable privileged-operation intent before network side effects;
- explicit `RecoveryRequired` behavior and typed canonical reconciliation;
- disruptive-change confirmation with automatic rollback;
- supervised `routerd` and `router-applyd`;
- default-deny WAN policy, nftables NAT/firewall, PPPoE, DHCP/DNS and WireGuard;
- provider-aware Dynamic DNS through Alpine `inadyn`, with **No-IP as the default
  for new configurations** and Cloudflare retained for backward compatibility;
- bounded gateway quality / PPPoE monitoring and seven-day history;
- bounded local storage and fail-closed behavior under disk pressure;
- central authenticated appliance health on the Overview dashboard;
- signed update manifests, SHA-256 verification, checksums, SBOM generation and
  release provenance support;
- TypeScript dashboard, Playwright browser coverage, Go race/vet/vulnerability
  checks, CodeQL, secret scanning, gosec, shellcheck, actionlint, ARM64 QEMU,
  network-namespace tests, fuzzing and performance benchmarks.

## Owner Proxmox pilot — 2026-08-01

A controlled owner-Proxmox run carried real Internet traffic through Minimal
Router for approximately **27 minutes**, compared it directly with the existing
pfSense fallback, exercised a CPU-load condition, validated external WireGuard
access, and then completed the planned fallback to pfSense.

| Test | Minimal Router VM 108 | pfSense VM 106 |
|---|---:|---:|
| Download | **570 Mbps** | 543 Mbps |
| Upload | **327 Mbps** | 318 Mbps |
| Packet loss, 600 packets | **0%** | **0%** |
| Ping to 1.1.1.1 | 2.77 ms | **1.94 ms** |
| Ping to 8.8.8.8 | 8.54 ms | **7.61 ms** |
| DNS, 200 queries | **12.65 ms, 200/200** | 13.00 ms, 200/200 |
| CPU 100% load test | **0% loss; dashboard 30/30** | Not recorded |
| RAM after test | **172 MB** | Guest agent unavailable |

The operational fallback also succeeded: pfSense returned with Internet
connectivity, Minimal Router VM 108 was shut down and isolated, and the measured
transition took approximately **93 seconds**.

### Alpine kernel / PPPoE finding

The initial Alpine guest used `linux-virt`. During the real PPPoE bring-up that
running kernel did not provide the PPPoE module required by the appliance. After
switching the guest to **Alpine `linux-lts`**, the required PPPoE support was
available and the real WAN test succeeded.

Observed RAM use was approximately **73 MB after a clean `linux-lts` boot** and
**172 MB after the exercised test workload**. The larger kernel package therefore
did not translate into proportional resident-memory use in this pilot.

The validated Proxmox path now recommends `linux-lts`, and both Alpine installers
also fail closed unless `modprobe pppoe` succeeds. This capability check is the
actual requirement; a future kernel can qualify if it provides the same required
module.

### WireGuard result — PASS for external handshake and dashboard access

WireGuard is no longer an unvalidated basic-connectivity item for this pilot.
After Dynamic DNS was provisioned manually on the Proxmox side, a phone on the
external network successfully established the WireGuard connection and opened
the Minimal Router dashboard through the tunnel.

This demonstrates, for this test session:

- Internet-origin reachability to the WireGuard UDP endpoint;
- successful peer authentication/handshake;
- WAN/firewall handling for the WireGuard entry path;
- tunnel routing sufficient to reach management;
- dashboard access over WireGuard without exposing the dashboard directly on
  WAN.

Repeated reconnect/reboot recovery, longer-term tunnel stability and broader
traffic/throughput cases remain separate gates.

### Dynamic DNS result — No-IP implemented; appliance-managed proof still open

The deployment uses **No-IP**. The earlier appliance implementation was
Cloudflare-only, so it did not match the actual deployment. The manually
provisioned Proxmox-side DDNS used during the successful WireGuard check proves
that the external hostname/endpoint concept works, but it does not prove the old
appliance-managed DDNS path.

The repository now supports provider-aware DDNS through `inadyn`:

- No-IP through the native `no-ip.com` provider;
- Cloudflare through the existing `cloudflare.com` provider;
- No-IP is the default for new configurations;
- configurations created before provider support, where the provider value is
  absent, retain legacy Cloudflare semantics;
- provider-specific validation prevents a No-IP credential from being treated as
  a Cloudflare token and vice versa;
- switching providers requires a new secret;
- the existing transactional apply flow still performs config validation,
  bounded one-shot update, service restart/health verification and rollback.

The remaining DDNS target-host gate is to deploy this build and prove that
**MinimalRouter itself**, not a host-side workaround, updates No-IP, stays
healthy, resolves externally to the current public IPv4, and later follows a
real public-IP change.

See [`DYNAMIC_DNS.md`](DYNAMIC_DNS.md) and
[`PROXMOX_TEST_REPORT_2026-08-01.md`](PROXMOX_TEST_REPORT_2026-08-01.md).

## Automated validation baseline

Recent merged hardening work has passed the repository's CI, Deep validation,
Performance, CodeQL, Secret scan and service-supervision workflows. The automated
suite covers, among other things:

- Go race tests, vet, `govulncheck`, fuzz targets and coverage;
- TypeScript lint/unit/build and Playwright E2E;
- clean Alpine installation, update activation and rollback;
- crash recovery and privileged-operation interruption cases;
- ARM64/QEMU execution and isolated WAN-router-LAN namespace networking;
- DHCP, DNS, NAT, stateful firewall, TCP, UDP and parallel-flow regression tests;
- no response on tested synthetic WAN management/service ports;
- signed update handling and binary/security inspection;
- bounded storage thresholds, history retention, WAL maintenance and rotated
  service logs;
- appliance-health aggregation and malformed-response dashboard handling.

These automated tests are regression and control-plane evidence. They do not
replace target-host PPPoE, NIC, thermal, ISP, power-loss or long-duration tests.

## What remains unproven after the target-host pilot

Before an unattended production recommendation, recorded evidence is still
required for:

1. stable WAN/LAN identity across repeated Proxmox and guest reboots;
2. repeated real ISP PPPoE disconnect/reconnect, authentication, MTU and reboot
   recovery;
3. repeated/sustained VirtIO or passed-through NIC throughput, packet rate, CPU,
   IRQ, latency, jitter, loss and thermal behavior;
4. MinimalRouter-managed No-IP one-shot update, daemon health, external
   resolution and later public-IP-change propagation;
5. WireGuard recovery after PPPoE reconnect/reboot plus any broader traffic or
   throughput cases required by the deployment;
6. independent external IPv4 and IPv6 scanning;
7. encrypted backup export and restore into a fresh VM;
8. full-disk, inode-exhaustion, read-only-filesystem, service/helper crash,
   corrupt-state and abrupt host power-loss exercises on persistent storage;
9. controlled interruption between privileged intent, runtime apply, runtime
   confirmation, SQLite commit, helper `last-good` commit and pending cleanup;
10. sustained operation with bounded logs, WAL, history and snapshots plus stable
    memory/thermal behavior for at least seven days;
11. owner-qualified signed installation/recovery media;
12. an independent focused security review before an unattended production
    claim.

## Current deployment recommendation

The current tree is suitable for continued **controlled Proxmox pilot use** with
console access and the known-good pfSense router ready for rollback. The dated
pilot provides real evidence for PPPoE/Internet forwarding on `linux-lts`, the
recorded performance/load sample, an external WireGuard phone handshake with
dashboard access, and operational fallback.

It is not yet documented as a drop-in unattended production replacement for
pfSense. The next highest-value target test is the newly implemented
MinimalRouter-managed No-IP path, followed by repeated PPPoE/reboot recovery and
longer soak/fault/security validation.

See also:

- [`PROXMOX_TEST_REPORT_2026-08-01.md`](PROXMOX_TEST_REPORT_2026-08-01.md)
- [`DYNAMIC_DNS.md`](DYNAMIC_DNS.md)
- [`PROXMOX.md`](PROXMOX.md)
- [`INSTALLATION.md`](INSTALLATION.md)
- [`TESTING.md`](TESTING.md)
- [`FAILURE_SCENARIOS.md`](FAILURE_SCENARIOS.md)
- [`STORAGE_PRESSURE.md`](STORAGE_PRESSURE.md)
- [`APPLIANCE_HEALTH.md`](APPLIANCE_HEALTH.md)
- [`RESOURCE_AND_HARDWARE_TEST.md`](RESOURCE_AND_HARDWARE_TEST.md)
- [`SECURITY_REVIEW.md`](SECURITY_REVIEW.md)
- [`RECOVERY.md`](RECOVERY.md)
