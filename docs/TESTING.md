# Testing

Minimal Router tests focus on failure behavior as much as the happy path.
A configuration mutation must finish in one of three states:

- `Committed` — the new configuration is active and recorded;
- `RolledBack` — the previous known-good state is positively verified;
- `RecoveryRequired` — the result cannot be proven and further mutation is blocked.

Anything that reports success with an unknown or partial runtime state is a bug.

Current results: [`CURRENT_VALIDATION.md`](CURRENT_VALIDATION.md).

## Local test commands

### Go

```sh
go test -race ./...
go vet ./...
govulncheck ./...
```

### Dashboard

```sh
pnpm --dir web install --frozen-lockfile
pnpm --dir web lint
pnpm --dir web test
pnpm --dir web build
pnpm --dir web exec playwright install chromium
pnpm --dir web test:e2e
```

### Offline installer regression

```sh
sh packaging/alpine/install-dist_test.sh
```

## CI coverage

Repository workflows currently cover:

- Go race/vet/vulnerability checks;
- frontend lint, unit, build and Playwright E2E;
- clean Alpine packaging and first-run smoke tests;
- update activation/rollback and interrupted-update recovery;
- configuration transaction and recovery-state regressions;
- storage-pressure and appliance-health policies;
- API/update-journal fuzzing;
- CodeQL, secret scanning, `gosec`, `shellcheck` and `actionlint`;
- ARM64/QEMU smoke tests;
- isolated WAN-router-LAN DHCP/DNS/NAT/firewall tests;
- control-plane benchmarks.

Automated tests are regression evidence. They do not prove a specific ISP, NIC,
hypervisor, thermal profile or long-running deployment.

## Network laboratory

`scripts/ci-network-namespace-lab.sh` creates disposable WAN, router and LAN Linux
namespaces and checks:

- DHCP and DNS;
- IPv4 forwarding/NAT;
- stateful firewall behavior;
- TCP/UDP and parallel flows;
- latency and packet loss;
- rejection of tested WAN management/service ports.

Same-kernel throughput in this lab is not a physical/VirtIO performance claim.

## Important failure cases

Regression tests should cover at least:

- stale/invalid configuration revisions;
- generator, preflight, apply and verification failures;
- lost or duplicated privileged IPC responses;
- interrupted confirmation/commit/rollback;
- corrupt transaction or update journals;
- service crashes and timeouts;
- critical storage pressure and failed durable writes;
- failed snapshot/backup/update writes;
- missing management confirmation;
- power loss around durable state transitions;
- tampered or incompatible signed update artifacts.

For each case verify the runtime state, canonical SQLite state, helper journal,
audit result and final `Committed` / `RolledBack` / `RecoveryRequired` outcome.

## Manual Proxmox tests

Run disruptive tests only on the target VM with a known-good router ready for
rollback. Recommended order:

1. Record guest/kernel/NIC/bridge baseline.
2. Take application backup and Proxmox snapshot.
3. Repeat graceful guest/host reboots and confirm WAN/LAN identity.
4. Verify DHCP, DNS, NAT, firewall and HTTPS management boundaries.
5. Test confirmed and unconfirmed disruptive config changes.
6. Test service/helper crashes and canonical reconciliation.
7. Activate and roll back an update.
8. Restore an encrypted backup into a fresh VM.
9. Test real PPPoE reconnect/reboot behavior.
10. Verify MinimalRouter-managed No-IP update and IP-change propagation.
11. Verify external WireGuard and perform external IPv4/IPv6 scans.
12. Exercise full-disk, inode, read-only-filesystem and abrupt-power failures on a disposable clone.
13. Measure sustained throughput, packet rate, latency/loss, CPU/RAM and thermals.
14. Run a minimum seven-day soak with bounded disk growth.

## Performance evidence

Packet forwarding stays in the Linux kernel, so Go API benchmarks are only
control-plane measurements. Target-host performance reports should record the
exact commit, Proxmox/guest versions, CPU/RAM/disk/NIC setup, test duration,
traffic direction, packet size, raw result and limitations.

## Privacy

Use synthetic fixtures in CI and documentation. Never commit real credentials,
private keys, backups, packet captures, public addresses, hostnames, MAC
addresses, VM inventory or household device data.
