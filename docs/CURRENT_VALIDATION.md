# Current validation status — 2026-07-30

This document is the current source of truth for automated validation and the
remaining manual release gates. Dated hardware reports remain historical evidence;
this file records the latest repository state.

## Repository baseline

The current `main` branch includes:

- crash-safe A/B activation and rollback with a durable operation journal;
- independent bootstrap executables for update and recovery;
- transactional recovery changes and session revocation;
- signed update manifests, SHA-256 verification, checksums, SBOM generation, and
  release provenance support;
- CI and tagged-release `checkout`, `setup-go`, and `setup-node` actions on v7,
  with `upload-artifact` v7 in workflows that upload evidence;
- TypeScript 6.0.3 and Node.js type definitions 26.1.2 for the dashboard build;
- Go race tests, `vet`, `govulncheck`, secret scanning, `gosec`, `shellcheck`, and
  `actionlint`;
- frontend lint, unit tests, production build, dependency audit, and Playwright
  browser E2E tests;
- AMD64 clean-Alpine installation, first-run wizard, update activation, and
  rollback smoke tests;
- ARM64 build and QEMU smoke tests;
- fuzzing for malformed unauthenticated API requests and update-operation
  journals;
- repeated interrupted-update recovery tests;
- isolated WAN-router-LAN network namespace tests;
- API and update-state performance benchmarks with allocation measurements.

No release tag was created as part of the 2026-07-30 validation. The tagged-release
workflow is aligned with the public repository and still requires the configured
offline release-signing secret before a real release tag can succeed.

## Latest automated result

The final pull-request validation on 2026-07-30 completed successfully for the
standard CI, Deep validation, and Performance workflows. The automated suite
demonstrated:

- DHCP, DNS, NAT, stateful firewall, TCP, UDP, and parallel-flow operation in an
  isolated Linux namespace laboratory;
- zero packet loss in the recorded virtual network run;
- no response on the tested WAN management and service ports;
- successful ARM64 execution of recovery-safe commands;
- successful crash recovery, both fuzz targets, coverage generation, binary
  inspection, and high-confidence static security analysis;
- successful dashboard TypeScript 6 build and browser E2E execution;
- successful clean Alpine installation, setup, update activation, and rollback.

The virtual network result is a same-kernel regression test, not a physical-router
throughput claim. Very high virtual throughput numbers must not be used as a claim
for VirtIO, physical NIC, PPPoE, WireGuard, thermal, or ISP performance.

## Recorded control-plane baseline

On a GitHub-hosted AMD EPYC runner, the recorded benchmark range was approximately:

| Operation | Result |
|---|---:|
| Setup-status API | 4.8–5.4 microseconds |
| Normal update-state read | about 26 microseconds |
| Update-state read while recovering a journal | 44.6–45.1 microseconds |
| Rejected protected request with durable audit write | 4.27–4.40 milliseconds |

These are control-plane measurements. Packet forwarding stays in the Linux kernel
and must be measured separately on the target Proxmox host and NIC configuration.

## What automated validation does not prove

The following remain manual Proxmox or hardware gates:

1. Stable WAN/LAN identity across repeated Proxmox and guest reboots.
2. Real ISP PPPoE establishment, disconnect, reconnect, MTU, and authentication.
3. Real VirtIO or passed-through NIC throughput, packet rate, CPU use, IRQ load,
   latency, jitter, and packet loss.
4. Real WireGuard throughput and recovery from an unrelated external network.
5. External IPv4 and IPv6 scanning from a host outside the test network.
6. Backup export and restore into a fresh VM.
7. Full-disk, read-only-filesystem, service-crash, corrupt-state, and abrupt host
   power-loss exercises on persistent storage.
8. Sustained operation, bounded logs, disk growth, memory stability, and thermal
   behavior over at least seven days.
9. Owner-signed installation and recovery media booted on the target platform.
10. An independent focused security review before an unattended production claim.

## Current deployment recommendation

The current tree is suitable for a controlled Proxmox pilot with console access,
an isolated LAN, a non-production WAN during initial testing, and the existing
pfSense VM or appliance ready for immediate rollback.

It is not yet documented as a drop-in, unattended production replacement for
pfSense. Promotion to production must be based on recorded target-host evidence,
not only GitHub Actions results.

The owner-created VM must be continued through
[`PROXMOX_AI_HANDOFF.md`](PROXMOX_AI_HANDOFF.md), which requires read-only
inventory, safe boot, rollback preservation, ordered testing, private evidence,
and stop conditions.

See also:

- [`PROXMOX.md`](PROXMOX.md)
- [`PROXMOX_AI_HANDOFF.md`](PROXMOX_AI_HANDOFF.md)
- [`TESTING.md`](TESTING.md)
- [`RESOURCE_AND_HARDWARE_TEST.md`](RESOURCE_AND_HARDWARE_TEST.md)
- [`SECURITY_REVIEW.md`](SECURITY_REVIEW.md)
- [`RECOVERY.md`](RECOVERY.md)
