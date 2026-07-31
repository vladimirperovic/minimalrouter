# Current validation status — 2026-07-31

This document is the current source of truth for automated validation and the
remaining manual release gates. Dated hardware reports remain historical evidence;
this file records the latest repository candidate.

## Repository baseline

The current validated candidate includes:

- crash-safe A/B activation and rollback with a durable update-operation journal;
- independent bootstrap executables for update and recovery;
- durable privileged-operation intent before network side effects and validated
  completed-result persistence afterward;
- exact-ID idempotent transport retry and fail-closed handling of incomplete,
  corrupt, unreadable, or contradictory privileged outcomes;
- explicit `RecoveryRequired` state that blocks ordinary mutations until typed
  canonical `RECONCILE` succeeds;
- two-phase disruptive confirmation: runtime verification, SQLite canonical
  commit, then helper `last-good` acknowledgement and pending cleanup;
- fresh final-helper-commit IDs for explicit retry after a recorded storage
  failure, while ambiguous transport retry retains the same ID;
- WireGuard-only management confirmation coverage for key, port, address, peer,
  and allowed-route changes;
- transactional recovery changes and session revocation;
- signed update manifests, SHA-256 verification, checksums, SBOM generation, and
  release provenance support;
- GitHub Actions `checkout`, `setup-go`, `setup-node`, and `upload-artifact` v7;
- TypeScript 6.0.3 and Node.js type definitions 26.1.2 for the dashboard build;
- Go race tests, `vet`, `govulncheck`, CodeQL, secret scanning, `gosec`,
  `shellcheck`, and `actionlint`;
- frontend lint, unit tests, production build, dependency audit, and Playwright
  browser E2E tests;
- AMD64 clean-Alpine installation, first-run wizard, signed update, activation,
  and rollback smoke tests;
- ARM64 build and QEMU smoke tests;
- fuzzing for malformed unauthenticated API requests and update-operation
  journals;
- repeated interrupted-update recovery tests;
- isolated WAN-router-LAN network namespace tests;
- API and update-state performance benchmarks with allocation measurements.

## Latest automated result

Pull-request candidate commit
`2770703f831872f41fb8840c262a9d19c6031ab6` completed successfully on
2026-07-31 for:

- standard CI, including Go race/vet/vulnerability checks, dashboard lint/unit/
  build/E2E, repository hygiene, public-root validation, and clean Alpine install,
  setup, signed update activation, and rollback;
- Deep validation, including ARM64 QEMU execution, WAN-router-LAN namespace
  networking, actionlint, shellcheck, high-confidence gosec, Linux binary
  inspection, interrupted-update stress, both fuzz targets, benchmarks, and
  coverage generation;
- CodeQL;
- secret scanning; and
- Performance benchmarks.

The automated suite demonstrated:

- deterministic failure behavior for lost privileged responses, durable-intent
  interruption, corrupt helper records, contradictory RPC outcomes, failed
  rollback, SQLite commit failure, and canonical reconciliation;
- ordered disruptive confirmation where helper `last-good` cannot advance before
  SQLite canonical commit;
- retry of a failed final helper acknowledgement without repeating runtime
  confirmation or canonical commit;
- DHCP, DNS, NAT, stateful firewall, TCP, UDP, and parallel-flow operation in an
  isolated Linux namespace laboratory;
- no response on the tested WAN management and service ports;
- successful ARM64 execution of recovery-safe commands;
- successful update crash recovery, both fuzz targets, coverage generation,
  binary inspection, and high-confidence static security analysis;
- successful dashboard TypeScript 6 build and browser E2E execution;
- successful clean Alpine installation, setup, update activation, and rollback.

The final documentation-only head after this record must pass the same workflows
before merge; the merge decision must be based on that final head rather than the
intermediate candidate named above.

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
7. Full-disk, inode-exhaustion, read-only-filesystem, service-crash,
   helper-process-crash, corrupt-state, and abrupt host power-loss exercises on
   persistent storage.
8. Controlled interruption specifically between privileged intent, runtime
   apply, runtime confirmation, SQLite commit, helper `last-good` commit, and
   pending cleanup.
9. Sustained operation, bounded logs and journals, disk growth, memory stability,
   and thermal behavior over at least seven days.
10. Owner-signed installation and recovery media booted on the target platform.
11. An independent focused security review before an unattended production claim.

## Current deployment recommendation

The current tree is suitable for a controlled Proxmox pilot with console access,
an isolated LAN, a non-production WAN during initial testing, and the existing
pfSense VM or appliance ready for immediate rollback.

It is not yet documented as a drop-in, unattended production replacement for
pfSense. Promotion to production must be based on recorded target-host evidence,
not only GitHub Actions results.

See also:

- [`PROXMOX.md`](PROXMOX.md)
- [`TESTING.md`](TESTING.md)
- [`FAILURE_SCENARIOS.md`](FAILURE_SCENARIOS.md)
- [`RESOURCE_AND_HARDWARE_TEST.md`](RESOURCE_AND_HARDWARE_TEST.md)
- [`SECURITY_REVIEW.md`](SECURITY_REVIEW.md)
- [`RECOVERY.md`](RECOVERY.md)