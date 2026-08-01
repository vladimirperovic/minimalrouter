# Testing strategy

## Principle

Router tests must prove failure behavior, not only the happy path. Every mutation
must end in exactly one of three states:

- the complete new configuration is active and recorded as `Committed`;
- the complete previous known-good configuration is positively verified as
  restored and recorded as `RolledBack`; or
- the outcome cannot be proven, the router reports `RecoveryRequired`, blocks
  further mutation, and requires canonical reconciliation or local recovery.

Partial success, inferred rollback, and unknown state reported as success are
defects.

The latest repository-wide result is summarized in
[`CURRENT_VALIDATION.md`](CURRENT_VALIDATION.md). Dated hardware evidence remains
in [`RESOURCE_AND_HARDWARE_TEST.md`](RESOURCE_AND_HARDWARE_TEST.md).

## Automated test layers

### Go correctness and race tests

```sh
go test -race ./...
go vet ./...
govulncheck ./...
```

Coverage includes validation, authorization, configuration generation,
transaction state, snapshots, recovery, migrations, interface selection,
device-profile schedules, signed manifests, A/B slot transitions, privileged RPC
outcome invariants, durable intent/result records, idempotent retries,
commit-confirm ordering, canonical reconciliation, bounded-storage thresholds,
durable-write classification, and central appliance-health severity ordering.

### Dashboard tests

```sh
pnpm --dir web install --frozen-lockfile
pnpm --dir web lint
pnpm --dir web test
pnpm --dir web build
pnpm --dir web exec playwright install chromium
pnpm --dir web test:e2e
```

The dashboard currently builds with TypeScript 6.0.3 and Node.js type definitions
26.1.2. Unit tests must not use a real router API or developer browser state.
Playwright fixtures must use synthetic addresses, devices, and credentials. The
Overview health banner must remain usable on desktop, Mobile Safari/WebKit, and
Android/Chromium layouts.

### Clean Alpine installation

The standard CI builds an AMD64 distribution and verifies in a clean Alpine
environment:

- archive checksum;
- installer and shell syntax;
- OpenRC services;
- first-run HTTPS wizard;
- nftables, dnsmasq, and dashboard availability;
- bounded router-service logrotate policy;
- signed update staging and explicit activation;
- service execution from the active slot;
- explicit rollback to the previous verified slot.

This is a packaging and lifecycle smoke test. It does not replace real Proxmox,
physical NIC, PPPoE, external scan, sustained load, destructive full-disk/inode
exhaustion, read-only-filesystem, process-kill, or abrupt host-power tests.

### Storage pressure and appliance health

Automated tests cover the deterministic policy layer:

- storage is Normal below 80%, Warning from 80% to below 90%, and Critical at
  90% or higher;
- Critical storage disables nonessential history and durable configuration writes;
- management mutations that require durable state are classified for HTTP 507
  rejection while read-only/export/preview paths remain available;
- recovery mutations are not exempt from durable-write protection;
- central health severity preserves `Recovery required` above Degraded, Warning,
  Unknown, and Healthy;
- critical storage, stale backup, unsynchronized time, memory pressure, and
  conntrack pressure feed the aggregate health model deterministically;
- Linux service facts use OpenRC started state, supervised child processes, the
  protected apply socket, configured PPPoE/dnsmasq services, WireGuard interface
  state, and update-slot metadata without reading secrets.

The detailed storage target-appliance checklist is
[`STORAGE_PRESSURE_TEST_PLAN.md`](STORAGE_PRESSURE_TEST_PLAN.md). Real filesystem
exhaustion must additionally prove that existing nftables, PPPoE, DHCP/DNS and
forwarding state stay active while durable management mutations fail closed.

### Configuration crash and IPC tests

Deterministic tests cover:

- lost apply and confirmation responses;
- identical-ID retry without duplicate privileged side effects;
- transaction-ID reuse with different content;
- durable intent written before side effects;
- incomplete intent after a simulated interruption;
- result-journal persistence failure;
- corrupt or structurally invalid transaction, pending, and last-good metadata;
- contradictory privileged RPC outcomes;
- blocking new mutations after `RecoveryRequired`;
- canonical `RECONCILE` as the only allowed override for unresolved helper state;
- WireGuard-only management changes that require confirmation;
- failed automatic rollback and fresh rollback IDs;
- SQLite commit failure with verified versus unverified restoration;
- two-phase confirmation ordering: runtime verification, SQLite commit, helper
  `last-good` commit, and pending cleanup;
- fresh final-commit IDs for explicit retry after a helper storage failure.

These tests prove protocol behavior in-process. They do not prove filesystem,
kernel, service-manager, or power-loss behavior on the target appliance.

### Update crash recovery and fuzzing

The Deep validation workflow runs:

```sh
go test -race -count=25 ./internal/firmware \
  -run 'Test(Interrupted|CorruptOperationJournal|OperationJournal|SlotManagerStages)'

go test ./internal/api -run '^$' \
  -fuzz '^FuzzMalformedUnauthenticatedRequests$' -fuzztime=20s

go test ./internal/firmware -run '^$' \
  -fuzz '^FuzzOperationJournalParsing$' -fuzztime=20s
```

The update manager uses a durable operation journal so interrupted activation or
rollback can be reconciled. Fuzzing rejects malformed unauthenticated HTTP input
and corrupt or hostile journal data without panic or uncontrolled behavior.

### Security hardening

Automated security gates include:

- CodeQL and secret scanning;
- `govulncheck`;
- high-severity/high-confidence `gosec`;
- `shellcheck` and `actionlint`;
- binary architecture and module inspection;
- executable-stack rejection;
- dashboard dependency audit;
- default-deny WAN checks in the network laboratory;
- management and service port scanning on the synthetic WAN.

Every reported vulnerability requires a regression test.

### ARM64 smoke test

CI cross-builds ARM64 binaries and executes recovery-safe commands through QEMU.
This proves architecture and basic execution compatibility; it is not an ARM64
hardware, driver, thermal, or throughput qualification.

### Isolated WAN-router-LAN laboratory

`scripts/ci-network-namespace-lab.sh` builds disposable Linux WAN, router, and LAN
namespaces. It validates:

- DHCP;
- DNS;
- IPv4 forwarding and NAT;
- stateful firewall behavior;
- TCP and UDP;
- parallel flows;
- latency and packet loss;
- rejection of tested WAN management/service ports.

Very high same-kernel throughput is a regression ceiling, not a physical or
VirtIO performance claim.

### Performance baselines

The Performance workflow records `ns/op`, `B/op`, and `allocs/op` for concurrent
management API operations and update-state reads:

```sh
go test ./internal/api -run '^$' -bench '^BenchmarkAPI' \
  -benchmem -benchtime=1s -count=3

go test ./internal/firmware -run '^$' -bench '^BenchmarkSlotManager' \
  -benchmem -benchtime=1s -count=3
```

Recorded 2026-07-30 control-plane ranges on a GitHub-hosted AMD EPYC runner were:

| Operation | Approximate result |
|---|---:|
| Setup-status API | 4.8–5.4 microseconds |
| Normal update-state read | about 26 microseconds |
| Journal-recovery state read | 44.6–45.1 microseconds |
| Rejected protected request with durable audit write | 4.27–4.40 milliseconds |

Packet forwarding remains in the Linux kernel and must be benchmarked separately.

## Component contract tests

Component tests run in pinned Alpine containers or disposable VMs and cover:

- `nft --check` and atomic load behavior;
- `dnsmasq --test`, DHCP leases, DNS filtering, and nft sets;
- PPPoE configuration and permissions;
- WireGuard mapping, routes, MTU, and cleanup;
- QoS lifecycle and live qdisc inspection;
- DDNS and Wi-Fi capability/preflight/rollback behavior;
- OpenRC service lifecycle;
- SQLite locking, retention, passive WAL checkpoint, and corruption detection;
- bounded logrotate configuration;
- recovery commands and undo snapshots;
- signature, checksum, path, symlink, and manifest rejection.

Containers validate generators and lifecycle contracts but do not replace boot,
kernel, hypervisor, physical-network, or persistent-storage tests.

## Manual Proxmox and appliance tests

Run on the actual target VM with pfSense ready for rollback. Record the exact host
and guest environment, but redact real identifiers and secrets.

Required order:

1. Read-only VM, disk, and NIC/bridge inventory.
2. Known-good application backup and Proxmox snapshot.
3. Repeated graceful guest and Proxmox lifecycle reboots.
4. Stable WAN/LAN role reconciliation.
5. DHCP, DNS, NAT, and default-deny WAN validation.
6. HTTPS management boundary, login/logout, CSRF, and recovery-console access.
7. Disruptive change with successful confirmation, timeout rollback, and failed
   rollback recovery.
8. Process termination at durable-intent, runtime-confirmation, SQLite-commit,
   final-helper-commit, and reconcile boundaries.
9. Update activation, service restart/reboot, verification, and rollback.
10. Backup restore into a fresh VM.
11. Target-host CPU, RAM, disk, throughput, packet rate, latency, jitter, loss,
    and management responsiveness.
12. Fill a disposable filesystem beyond 80% and 90%; verify health transitions,
    HTTP 507 durable-write rejection, history shedding, bounded logs/WAL, recovery
    after freeing space, and uninterrupted existing forwarding state.
13. Full disk, inode exhaustion, read-only filesystem, corrupt-state, and abrupt
    power-loss tests on a disposable target.
14. Real PPPoE only during a maintenance window after rollback is proven.
15. External IPv4/IPv6 scan and WireGuard from an unrelated network.
16. At least seven days of continuous operation with bounded disk growth.

## Failure-injection matrix

Inject at least:

- invalid or oversized API and IPC input;
- stale revision;
- generator, preflight, apply, verification, and confirmation failure;
- failed durable-intent and completed-result writes;
- unreadable, corrupt, incomplete, or contradictory privileged journal records;
- lost IPC response after each privileged phase;
- explicit final helper commit failure followed by retry;
- SQLite commit failure before and after runtime confirmation;
- warning and critical storage pressure;
- full disk and inode exhaustion;
- read-only filesystem;
- snapshot write failure;
- nftables or dnsmasq validation failure;
- service timeout, crash, and process kill;
- lost route or management address;
- missing administrator confirmation;
- power loss before and after every durable configuration and update marker;
- corrupted current snapshot, database, manifest, pending record, or journal;
- unsupported backup, database, or release version.

For every case assert:

- active Linux state;
- SQLite canonical revision;
- helper `last-good`, pending, and transaction-journal state;
- service and aggregate appliance health;
- update slot and update-journal state;
- audit result;
- exact terminal state: `Committed`, verified `RolledBack`, or
  `RecoveryRequired`;
- whether new mutations are correctly accepted or blocked;
- recovery or canonical reconcile outcome.

## Security test matrix

Verify:

- WAN cannot reach management over IPv4 or IPv6;
- unauthenticated, expired, or cross-origin requests fail;
- session rotation, cookie flags, CSRF, Host, and DNS-rebinding defenses;
- login and expensive-operation rate limits;
- rejection of invalid UTF-8, unknown fields, traversal, metacharacters, and large
  bodies;
- privileged helper peer, operation, path, flag, message, and outcome bounds;
- incomplete or corrupt privileged state cannot be treated as fresh state;
- only the canonical `RECONCILE` operation may supersede unresolved helper
  journal state;
- critical storage cannot produce false-success durable mutations;
- health telemetry is read-only and contains no secret material;
- `router-applyd` no-new-privileges, fixed executable path, bounded resources, and
  sanitized loader environment;
- logs, diagnostics, backups, public files, and artifacts contain no unapproved
  secrets;
- recovery has no network endpoint and credential changes revoke sessions;
- update/restore reject tampering, unsafe files, wrong signatures, and incompatible
  versions.

## Performance measurements on the target

Record:

- Proxmox version and host kernel;
- guest Alpine and kernel versions;
- CPU model/type, vCPU, RAM, disk/storage;
- VirtIO queues, offloads, bridges, VLANs using synthetic labels;
- packet sizes, directions, duration, and traffic-generator placement;
- boot-to-forwarding-ready and management-ready;
- idle and loaded CPU/RAM;
- routing/NAT throughput and packets per second;
- PPPoE reconnect time and MTU;
- WireGuard throughput and CPU cost;
- QoS behavior under load;
- packet loss, latency, jitter, and management responsiveness;
- log, snapshot, helper-journal, gateway-history, SQLite WAL, and disk growth.

Do not use the Go control plane as the forwarding benchmark path.

## Evidence and privacy

A result is publishable only when it includes date, exact commit, environment,
commands, units, raw summary, pass/fail, failures, recovery steps, and limitations.

Never commit credentials, tokens, private keys, backups, databases, packet
captures, public addresses, hostnames, MAC addresses, VM inventory, or household
device information.

Release claims must be based on recorded results, not expected capability.
