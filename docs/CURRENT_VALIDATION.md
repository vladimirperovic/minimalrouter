# Current validation status — 2026-08-01

This document is the private repository source of truth for imported public validation evidence, `minimalrouterhome` parity, and the remaining owner-Proxmox / hardware gates.

## Current synchronized public baseline

Shared application/runtime code is synchronized with:

`vladimirperovic/minimalrouter@df99909a7b161b1a0bcc7149b9dfeaf6a2a51796`

That baseline contains:

- PR #28 — bounded storage and disk-pressure safety;
- PR #29 — central appliance-health aggregation and Overview banner;
- docs-only PR #30 recording their final validation;
- all previously synchronized recovery, gateway-quality, security, update, OpenRC supervision, and appliance-hardening work.

`minimalrouterhome` remains a private production/deployment repository. Shared engine code follows public `minimalrouter`; live Proxmox/PPPoE/household values remain outside Git.

## PR #28 — bounded storage

Implemented behavior:

- storage `Warning` at 80% used;
- storage `Critical` at 90% used;
- durable API mutations rejected with HTTP 507 under Critical pressure;
- existing forwarding/runtime state is not deliberately torn down because storage is full;
- read-only status/preview/verification and encrypted backup export remain available where no new durable state is required;
- gateway probes continue while nonessential gateway history writes are shed;
- latest 100 canonical revisions retained;
- latest 20 configuration snapshots retained;
- latest 5,000 audit events retained;
- gateway samples bounded to seven days / 41,000 rows;
- gateway reconnect events bounded to seven days / 2,048 rows;
- passive SQLite WAL checkpoint at startup and every 15 minutes;
- routerd/router-applyd logs rotated at 1 MiB with four compressed rotations;
- logrotate packaged into direct and distributable Alpine installation paths.

Automated tests cover pressure thresholds and durable-write classification.

## PR #29 — central appliance health

Implemented authenticated read-only health states:

- `Healthy`;
- `Warning`;
- `Degraded`;
- `Recovery required`;
- `Unknown`.

The health model aggregates:

- recovery/transaction state;
- storage pressure;
- memory pressure;
- conntrack pressure;
- time synchronization;
- WAN/PPPoE and gateway quality;
- routerd/router-applyd supervision and protected apply socket;
- dnsmasq service state;
- PPPoE service state when configured;
- WireGuard interface state when configured;
- signed-update trust/pending update state;
- age of the latest successful encrypted backup export visible in retained audit state.

`Recovery required` has highest severity. Missing evidence remains `Unknown`. Health collection is observational only and performs no automatic remediation.

Overview contains a central health banner that refreshes every 15 seconds.

## Public automated evidence

Public `minimalrouter` PR #28 final head:

`e3f6b983b189e6418cbd1711abf32e4a29d98107`

Squash merge:

`818f8ed3b68a9d6edd8635b8729ddf36dba59c36`

Public `minimalrouter` PR #29 final head:

`f56807a4bbad09bc3565f66de2b3b18aeb5c87b4`

Squash merge:

`120c7b4704ba9de2505b8e043c2544ce2d2cd6db`

The final public #29 head passed the complete triggered workflow suite, including:

- Go formatting;
- `go test -race ./...`;
- `go vet ./...`;
- `govulncheck`;
- repository hygiene;
- frontend dependency audit;
- frontend lint/unit tests/TypeScript production build;
- Playwright browser E2E after the malformed-health-payload regression was fixed;
- clean Alpine install/setup/update/rollback;
- Deep validation;
- interrupted-update stress tests;
- both fuzz targets;
- coverage generation;
- isolated WAN-router-LAN namespace laboratory;
- ARM64/QEMU smoke;
- security/binary inspection;
- CodeQL for Go and JavaScript/TypeScript;
- Performance;
- Secret scan;
- OpenRC service supervision.

The docs-only public commit recording the merged state is:

`df99909a7b161b1a0bcc7149b9dfeaf6a2a51796`

## Private repository evidence boundary

The private repository imports the same shared runtime source and tests. Public CI evidence is valid evidence for bit-identical shared files, but it is not a substitute for target-Proxmox execution.

Do not claim a private CI pass unless GitHub Actions for the exact private head actually execute and report success.

Private deployment values, credentials, runtime databases, bridge assignments, backups, and household inventory are intentionally not stored in Git.

## What automated validation does not prove

The following still require the owner's real Proxmox environment or disposable target:

1. Exact VM and WAN/LAN bridge identity.
2. Stable guest WAN/LAN identity across repeated Proxmox/guest reboots.
3. Real ISP PPPoE authentication, MTU, disconnect/reconnect, and reboot recovery.
4. Real VirtIO/passthrough NIC throughput, packet rate, CPU, IRQ behavior, latency, jitter, and loss.
5. Real WireGuard throughput and recovery from an unrelated external network.
6. External IPv4 scan and IPv6 scan/fail-closed verification.
7. Encrypted backup restore into a fresh VM.
8. Full-disk and inode-exhaustion behavior on a disposable target.
9. Read-only-filesystem behavior on a disposable target.
10. Service-crash and abrupt power-loss exercises on persistent storage.
11. Seven-day continuous operation with bounded logs/WAL/history and stable memory.
12. Owner-qualified installation/recovery media and an independent focused security review.

## Current deployment recommendation

The current tree is suitable for a controlled Proxmox pilot with:

- console access;
- an isolated LAN;
- test/NAT WAN during first validation;
- existing pfSense/router fallback ready for immediate restoration;
- encrypted backup plus known-good Proxmox snapshot before destructive tests.

It is not yet documented as an unattended production replacement solely on GitHub Actions evidence.

Before touching the existing VM, follow:

- `PROXMOX_AI_HANDOFF.md`;
- `PROXMOX.md`;
- `STORAGE_PRESSURE.md`;
- `STORAGE_PRESSURE_TEST_PLAN.md`;
- `APPLIANCE_HEALTH.md`;
- `TESTING.md`;
- `RECOVERY.md`.

## Next target-host evidence

The next private test report should be created as:

```text
docs/PROXMOX_TEST_REPORT_YYYY-MM-DD.md
```

It should record sanitized evidence for VM inventory, boot/reboot behavior, DHCP/DNS/NAT/firewall, `/api/v1/health`, storage-pressure behavior, update/rollback, backup/restore, performance, PPPoE, WireGuard, external scans, failures, recovery actions, and remaining limitations.
