# Testing strategy

## Principle

Router tests must prove failure behavior, not only the happy path. Every mutation
must end in exactly one of two states:

- the complete new configuration is active and recorded; or
- the complete previous known-good configuration is restored.

Partial success is a defect.

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
device-profile schedules, signed manifests, and A/B slot transitions.

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
Playwright fixtures must use synthetic addresses, devices, and credentials.

### Clean Alpine installation

The standard CI builds an AMD64 distribution and verifies in a clean Alpine
environment:

- archive checksum;
- installer and shell syntax;
- OpenRC services;
- first-run HTTPS wizard;
- nftables, dnsmasq, and dashboard availability;
- signed update staging and explicit activation;
- service execution from the active slot;
- explicit rollback to the previous verified slot.

This is a packaging and lifecycle smoke test. It does not replace real Proxmox,
physical NIC, PPPoE, external scan, sustained load, or abrupt host-power tests.

### Crash recovery and fuzzing

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
- SQLite locking and corruption detection;
- recovery commands and undo snapshots;
- signature, checksum, path, symlink, and manifest rejection.

Containers validate generators and lifecycle contracts but do not replace boot,
kernel, hypervisor, or physical-network tests.

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
7. Update activation, service restart/reboot, verification, and rollback.
8. Backup restore into a fresh VM.
9. Target-host CPU, RAM, disk, throughput, packet rate, latency, jitter, loss, and
   management responsiveness.
10. Controlled service crash, disk pressure, read-only, corrupt-state, and abrupt
    power-loss tests on a disposable target.
11. Real PPPoE only during a maintenance window after rollback is proven.
12. External IPv4/IPv6 scan and WireGuard from an unrelated network.
13. At least seven days of continuous operation.

## Failure-injection matrix

Inject at least:

- invalid or oversized API input;
- stale revision;
- generator/preflight/apply/verification failure;
- full disk and inode exhaustion;
- read-only filesystem;
- snapshot write failure;
- nftables or dnsmasq validation failure;
- service timeout or crash;
- lost route or management address;
- missing administrator confirmation;
- interrupted update before/after every durable marker;
- corrupted current snapshot, database, manifest, or journal;
- unsupported backup, database, or release version.

For every case assert:

- active Linux state;
- database revision;
- service health;
- update slot and journal state;
- audit result;
- recovery or rollback outcome.

## Security test matrix

Verify:

- WAN cannot reach management over IPv4 or IPv6;
- unauthenticated, expired, or cross-origin requests fail;
- session rotation, cookie flags, CSRF, Host, and DNS-rebinding defenses;
- login and expensive-operation rate limits;
- rejection of invalid UTF-8, unknown fields, traversal, metacharacters, and large
  bodies;
- privileged helper peer, operation, path, flag, and message bounds;
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
- log, snapshot, and disk growth.

Do not use the Go control plane as the forwarding benchmark path.

## Evidence and privacy

A result is publishable only when it includes date, exact commit, environment,
commands, units, raw summary, pass/fail, failures, recovery steps, and limitations.

Never commit credentials, tokens, private keys, backups, databases, packet
captures, public addresses, hostnames, MAC addresses, VM inventory, or household
device information.

Release claims must be based on recorded results, not expected capability.
