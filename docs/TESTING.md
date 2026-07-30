# Testing strategy

## Principle

Router tests must prove failure behavior, not only the happy path. Every mutation
must end in exactly one of two states:

- the complete new configuration is active and recorded; or
- the complete previous known-good configuration is restored.

Partial success is a defect.

The latest repository-wide result is summarized in
[`CURRENT_VALIDATION.md`](CURRENT_VALIDATION.md). The exact safe continuation path
for the existing owner-created Proxmox VM is
[`PROXMOX_AI_HANDOFF.md`](PROXMOX_AI_HANDOFF.md).

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

The dashboard builds with TypeScript 6.0.3 and Node.js type definitions 26.1.2.
Node.js is build-time only. Tests use synthetic fixtures and must not depend on a
real router API, owner browser state, or private network data.

### Clean Alpine installation

The standard CI builds an AMD64 distribution and verifies:

- archive checksum;
- installer and shell syntax;
- OpenRC services;
- first-run HTTPS wizard;
- nftables, dnsmasq, and dashboard availability;
- signed update staging and explicit activation;
- service execution from the active slot;
- explicit rollback to the previous verified slot.

This does not replace real Proxmox, NIC, PPPoE, external scan, sustained load, or
host-power testing.

### Crash recovery and fuzzing

```sh
go test -race -count=25 ./internal/firmware \
  -run 'Test(Interrupted|CorruptOperationJournal|OperationJournal|SlotManagerStages)'

go test ./internal/api -run '^$' \
  -fuzz '^FuzzMalformedUnauthenticatedRequests$' -fuzztime=20s

go test ./internal/firmware -run '^$' \
  -fuzz '^FuzzOperationJournalParsing$' -fuzztime=20s
```

The durable operation journal reconciles interrupted activation and rollback.
Fuzz targets cover hostile unauthenticated HTTP input and corrupt journal data.

### Security hardening

Automated gates include:

- secret scanning and available code scanning;
- `govulncheck`;
- high-severity/high-confidence `gosec`;
- `shellcheck` and `actionlint`;
- binary architecture/module inspection and executable-stack rejection;
- dashboard dependency audit;
- synthetic WAN port and default-deny checks.

Every reported vulnerability requires a regression test.

### ARM64 smoke test

CI cross-builds ARM64 binaries and executes recovery-safe commands through QEMU.
This is not hardware, driver, thermal, or throughput qualification.

### Isolated WAN-router-LAN laboratory

`scripts/ci-network-namespace-lab.sh` validates DHCP, DNS, IPv4 forwarding, NAT,
stateful firewalling, TCP, UDP, parallel flows, latency, packet loss, and rejection
of tested WAN management/service ports.

Very high same-kernel throughput is only a regression ceiling.

### Performance baselines

```sh
go test ./internal/api -run '^$' -bench '^BenchmarkAPI' \
  -benchmem -benchtime=1s -count=3

go test ./internal/firmware -run '^$' -bench '^BenchmarkSlotManager' \
  -benchmem -benchtime=1s -count=3
```

Recorded 2026-07-30 ranges on a GitHub-hosted AMD EPYC runner:

| Operation | Approximate result |
|---|---:|
| Setup-status API | 4.8–5.4 microseconds |
| Normal update-state read | about 26 microseconds |
| Journal-recovery state read | 44.6–45.1 microseconds |
| Rejected protected request with durable audit write | 4.27–4.40 milliseconds |

These are control-plane results. Forwarding must be measured on the owner’s
Proxmox topology.

## Component contract tests

Pinned Alpine containers or disposable VMs cover:

- `nft --check` and atomic loads;
- `dnsmasq --test`, DHCP leases, DNS filtering, and nft sets;
- PPPoE configuration and permissions;
- WireGuard mapping, routes, MTU, and cleanup;
- QoS lifecycle and live qdisc inspection;
- DDNS and Wi-Fi preflight/rollback;
- OpenRC lifecycle;
- SQLite locking and corruption detection;
- recovery commands and undo snapshots;
- signature, checksum, path, symlink, and manifest rejection.

Containers do not replace boot, kernel, hypervisor, or physical-network tests.

## Existing Proxmox VM test order

Another AI or engineer must not recreate, start, or rewire the VM until it follows
`PROXMOX_AI_HANDOFF.md` and identifies one candidate plus its WAN/LAN bridges
read-only.

Required order:

1. Read-only Proxmox inventory and unambiguous candidate selection.
2. Confirm isolated LAN and test/NAT WAN; prevent dual DHCP on production LAN.
3. Confirm pfSense rollback independent of the candidate.
4. Export an encrypted application backup and take a known-good Proxmox snapshot.
5. Record current slot, commit, guest versions, services, storage, and topology
   privately.
6. Repeated graceful guest and Proxmox lifecycle reboots.
7. DHCP, DNS, NAT, HTTPS management, and default-deny WAN validation.
8. Unconfirmed LAN-change rollback and recovery-console access.
9. Signed update activation, restart/reboot, verification, and explicit rollback.
10. Backup restore into a fresh VM.
11. Target-host CPU, RAM, disk, throughput, packet rate, latency, jitter, loss, and
    management responsiveness.
12. Controlled service, disk-pressure, read-only, corrupt-state, and power-loss
    tests on a disposable clone/target.
13. Real PPPoE only during a maintenance window after rollback is proven.
14. External IPv4/IPv6 scan and WireGuard from an unrelated network.
15. At least seven days of continuous operation.

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
- missing confirmation;
- interruption before/after every durable update marker;
- corrupted snapshot, database, manifest, or journal;
- unsupported backup, database, or release version.

For every case assert active Linux state, database revision, service health,
update/journal state, audit result, and rollback outcome.

## Security test matrix

Verify:

- WAN cannot reach management over IPv4 or IPv6;
- unauthenticated, expired, and cross-origin requests fail;
- session rotation, cookie flags, CSRF, Host, and DNS-rebinding defenses;
- rate limits;
- invalid UTF-8, unknown fields, traversal, metacharacters, and large-body
  rejection;
- privileged helper peer/operation/path/message bounds;
- `router-applyd` no-new-privileges, fixed executable path, bounded resources, and
  sanitized loader environment;
- logs, diagnostics, backups, public files, and artifacts contain no unapproved
  secrets;
- recovery has no network endpoint and credential changes revoke sessions;
- update/restore reject tampering and incompatible versions.

## Target performance report

Record privately:

- exact repository commit;
- Proxmox version and host kernel;
- guest Alpine/kernel;
- CPU model/type, vCPU, RAM, disk/storage;
- VirtIO queues, offloads, bridges, and VLANs using synthetic labels;
- traffic-generator placement, packet sizes, directions, and duration;
- boot-to-ready times;
- idle/loaded CPU and RAM;
- routing/NAT throughput and packet rate;
- PPPoE reconnect and MTU;
- WireGuard throughput and CPU cost;
- QoS under load;
- loss, latency, jitter, and management responsiveness;
- disk/log/snapshot growth.

Do not use the Go control plane as the forwarding benchmark path.

## Evidence and privacy

Write new owner-host evidence to a private dated file such as
`docs/PROXMOX_TEST_REPORT_YYYY-MM-DD.md`.

Never commit Proxmox hostnames, node names, VM IDs, raw VM configs, bridge
inventory, credentials, tokens, private keys, backups, databases, packet captures,
real addresses, MAC addresses, or household devices.

Release claims must be based on recorded results, not expected capability.
