# Current validation status — 2026-08-01

This document is the current source of truth for automated validation, recorded
target-host evidence, and the remaining manual release gates. Dated hardware
reports remain historical evidence; this file records the latest repository and
owner-Proxmox results without turning a successful pilot into an unsupported
production claim.

## Repository baseline

The current public baseline includes:

- crash-safe A/B activation and rollback with a durable update-operation journal;
- independent bootstrap executables for update and recovery;
- durable privileged-operation intent before network side effects and validated
  completed-result persistence afterward;
- exact-ID idempotent transport retry and fail-closed handling of incomplete,
  corrupt, unreadable, or contradictory privileged outcomes;
- explicit `RecoveryRequired` state that blocks ordinary mutations until typed
  canonical `RECONCILE` succeeds;
- two-phase disruptive confirmation: runtime verification, SQLite canonical
  commit, helper `last-good` acknowledgement, and pending cleanup;
- WireGuard-only management confirmation coverage;
- supervised `routerd` and `router-applyd` with bounded crash respawn;
- bounded gateway quality / PPPoE monitoring and seven-day history;
- loose reverse-path filtering, bounded conntrack state, and client-only Chrony;
- signed update manifests, SHA-256 verification, checksums, SBOM generation, and
  release provenance support;
- bounded local storage and disk-pressure fail-closed behavior;
- central authenticated appliance health on the Overview dashboard;
- TypeScript 6 dashboard, Playwright browser coverage, Go race/vet/vulnerability
  checks, CodeQL, secret scanning, high-confidence gosec, shellcheck, actionlint,
  ARM64 QEMU, namespace networking, fuzzing, and performance benchmarks.

## Owner Proxmox pilot — 2026-08-01

A controlled owner-Proxmox run successfully carried real Internet traffic through
Minimal Router for approximately **27 minutes**, compared the candidate with the
existing pfSense fallback, exercised a CPU-load condition, and then completed the
planned operational rollback to pfSense.

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

The operational fallback succeeded: pfSense returned with Internet connectivity,
Minimal Router VM 108 was shut down and isolated, and the transition took
approximately **93 seconds**.

This closes part of the previous target-host uncertainty around basic Proxmox
routing, Internet forwarding, a first real throughput/latency/DNS comparison,
load-time management responsiveness, observed memory use, and fallback to the
known-good router.

It does **not** close all production gates. WireGuard was enabled but no phone
handshake was observed. Cloudflare DDNS was not confirmed working. The current
DDNS implementation is Cloudflare-only through Alpine `inadyn`; the dashboard
`Zone` field expects the DNS zone name such as `example.com`, not a Cloudflare
Zone ID. Repeated reboot/reconnect, external scanning, backup restore,
destructive storage/power tests, and long-duration soak testing remain open.

Full dated evidence and limitations are in
[`PROXMOX_TEST_REPORT_2026-08-01.md`](PROXMOX_TEST_REPORT_2026-08-01.md). DDNS
configuration and diagnostics are documented in
[`CLOUDFLARE_DDNS.md`](CLOUDFLARE_DDNS.md).

## Bounded storage — PR #28

PR #28, `Bound local storage and fail safely under disk pressure`, was validated
on head `e3f6b983b189e6418cbd1711abf32e4a29d98107` and squash-merged to `main` as
`818f8ed3b68a9d6edd8635b8729ddf36dba59c36` on 2026-08-01.

The final PR head passed all workflows triggered for the candidate:

- CI;
- Deep validation;
- Performance;
- CodeQL;
- Secret scan; and
- Service supervision on a clean Alpine 3.22 environment.

The merged behavior adds:

- Normal storage below 80%, Warning from 80% to below 90%, and Critical at 90%
  or higher;
- HTTP 507 rejection for management mutations that require durable persistence at
  Critical pressure, including recovery writes;
- no deliberate interruption of already-active forwarding, nftables, PPPoE, or
  DHCP/DNS state when the management plane enters pressure mode;
- nonessential gateway sample/reconnect history shedding while live probing and
  the in-memory gateway summary continue;
- latest 100 canonical revisions, 20 snapshots, 5,000 audit events, 41,000 /
  seven-day gateway samples, and 2,048 / seven-day reconnect events;
- passive SQLite WAL checkpoint and retention maintenance at startup and every
  15 minutes;
- Alpine log rotation for routerd/router-applyd at 1 MiB with four compressed
  rotations.

The automated threshold and policy tests do **not** replace destructive target
filesystem tests. Full disk, inode exhaustion, and read-only filesystem behavior
remain manual gates.

## Central appliance health — PR #29

PR #29, `Add central appliance health aggregation and overview status`, was
validated on final head `f56807a4bbad09bc3565f66de2b3b18aeb5c87b4` and
squash-merged to `main` as `120c7b4704ba9de2505b8e043c2544ce2d2cd6db`
on 2026-08-01.

The final PR head passed all workflows triggered for the candidate:

- CI, including Go race tests, vet, `govulncheck`, repository hygiene, dashboard
  dependency audit/lint/unit/type-build, Playwright E2E, and clean Alpine
  install/update/rollback;
- Deep validation, including crash recovery stress, both fuzz targets, coverage,
  ARM64/QEMU, isolated WAN-router-LAN networking, and security/binary inspection;
- Performance;
- CodeQL for Go and JavaScript/TypeScript;
- Secret scan; and
- Service supervision.

The merged health model provides these aggregate states:

- `Healthy`;
- `Warning`;
- `Degraded`;
- `Recovery required`; and
- `Unknown` when evidence cannot be measured reliably.

The aggregate includes configuration recovery, storage, memory, conntrack, kernel
time synchronization, WAN/gateway quality, supervised core processes, the
protected apply socket, configured DNS/DHCP and PPPoE services, configured
WireGuard interface state, signed-update state, and encrypted-backup age.

Health collection is observational only. It does not restart services, reconnect
PPPoE, change firewall state, apply WireGuard changes, or read/return passwords,
private keys, provider tokens, backup payloads, or other secrets.

The final Playwright run also covers the regression found during development:
a malformed or partial `/api/v1/health` response is validated and degrades to an
unavailable health banner instead of crashing the React dashboard.

## Previously demonstrated automated behavior

The broader automated suite has demonstrated:

- deterministic failure behavior for lost privileged responses, durable-intent
  interruption, corrupt helper records, contradictory RPC outcomes, failed
  rollback, SQLite commit failure, and canonical reconciliation;
- ordered disruptive confirmation where helper `last-good` cannot advance before
  SQLite canonical commit;
- retry of a failed final helper acknowledgement without repeating runtime
  confirmation or canonical commit;
- DHCP, DNS, NAT, stateful firewall, TCP, UDP, and parallel-flow operation in an
  isolated Linux namespace laboratory;
- no response on the tested synthetic WAN management and service ports;
- successful ARM64 execution of recovery-safe commands;
- successful update crash recovery, fuzz targets, coverage generation, binary
  inspection, and high-confidence static security analysis;
- successful TypeScript production builds, browser E2E execution, clean Alpine
  installation, first-run setup, update activation, rollback, and supervised
  process recovery.

The virtual network result is a same-kernel regression test, not a physical-router
throughput claim. It must not be presented as VirtIO, physical NIC, PPPoE,
WireGuard, thermal, or ISP performance evidence. The dated owner-Proxmox pilot
above is separate target-host evidence and must likewise retain its stated scope.

## Recorded control-plane baseline

On a GitHub-hosted AMD EPYC runner, the historical recorded range was approximately:

| Operation | Result |
|---|---:|
| Setup-status API | 4.8–5.4 microseconds |
| Normal update-state read | about 26 microseconds |
| Update-state read while recovering a journal | 44.6–45.1 microseconds |
| Rejected protected request with durable audit write | 4.27–4.40 milliseconds |

These are control-plane measurements. Packet forwarding stays in the Linux kernel
and must be measured separately on the target Proxmox host and NIC configuration.
The 2026-08-01 target-host report is the first such recorded owner-environment
sample, not a replacement for repeated or sustained measurements.

## What remains unproven after the target-host pilot

The 2026-08-01 pilot closed some earlier manual gates, but the following remain
required before an unattended production recommendation:

1. Stable WAN/LAN identity across repeated Proxmox and guest reboots.
2. Explicit real ISP PPPoE disconnect/reconnect, MTU, authentication, and reboot
   recovery evidence over repeated cycles.
3. Repeated target-host VirtIO or passed-through NIC throughput, packet rate, CPU
   use, IRQ load, latency, jitter, packet loss, and thermal behavior over longer
   runs.
4. Successful WireGuard handshake, traffic, throughput, and recovery from an
   unrelated external network.
5. Successful Cloudflare DDNS one-shot update, service health, external DNS
   resolution, and later public-IP-change propagation.
6. External IPv4 and IPv6 scanning from a host outside the test network.
7. Backup export and restore into a fresh VM.
8. Full-disk, inode-exhaustion, read-only-filesystem, service-crash,
   helper-process-crash, corrupt-state, and abrupt host power-loss exercises on
   persistent storage.
9. Verification that >90% real filesystem pressure returns HTTP 507 while the
   already-active forwarding plane remains functional, followed by recovery after
   freeing storage without a reboot.
10. Controlled interruption specifically between privileged intent, runtime
    apply, runtime confirmation, SQLite commit, helper `last-good` commit, and
    pending cleanup.
11. Sustained operation with bounded logs, WAL, history and snapshots plus stable
    memory/thermal behavior over at least seven days.
12. Owner-signed installation and recovery media booted on the target platform.
13. An independent focused security review before an unattended production claim.

## Current deployment recommendation

The current tree is suitable for continued controlled Proxmox pilot use with
console access and the existing pfSense VM or appliance ready for immediate
rollback. The 2026-08-01 run provides real target-host evidence that Internet
forwarding, the recorded performance/load checks, and operational fallback can
work in the owner's environment.

It is not yet documented as a drop-in unattended production replacement for
pfSense. Promotion to production still depends on closing the remaining
WireGuard, Cloudflare DDNS, recovery, external-scan, destructive-storage,
reboot/reconnect, soak, signed-media, and independent-review gates.

See also:

- [`PROXMOX_TEST_REPORT_2026-08-01.md`](PROXMOX_TEST_REPORT_2026-08-01.md)
- [`CLOUDFLARE_DDNS.md`](CLOUDFLARE_DDNS.md)
- [`PROXMOX.md`](PROXMOX.md)
- [`TESTING.md`](TESTING.md)
- [`FAILURE_SCENARIOS.md`](FAILURE_SCENARIOS.md)
- [`STORAGE_PRESSURE.md`](STORAGE_PRESSURE.md)
- [`STORAGE_PRESSURE_TEST_PLAN.md`](STORAGE_PRESSURE_TEST_PLAN.md)
- [`APPLIANCE_HEALTH.md`](APPLIANCE_HEALTH.md)
- [`RESOURCE_AND_HARDWARE_TEST.md`](RESOURCE_AND_HARDWARE_TEST.md)
- [`SECURITY_REVIEW.md`](SECURITY_REVIEW.md)
- [`RECOVERY.md`](RECOVERY.md)
