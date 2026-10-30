# Testing

Minimal Router tests focus on failure behavior as much as the happy path.
A configuration mutation must finish in one of three states:

- `Committed` — the new configuration is active and recorded;
- `RolledBack` — the previous known-good state is positively verified;
- `RecoveryRequired` — the result cannot be proven and further mutation is blocked.

Anything that reports success with an unknown or partial runtime state is a bug.

Current results: [`CURRENT_VALIDATION.md`](CURRENT_VALIDATION.md).

## Local test commands

### Rust

```sh
make fmt-check
make test
make vet
cargo audit --deny warnings   # from rust/
```

The Rust workspace replaced the Go implementation in v0.4.0. Its behaviour is
held to the last Go release by frozen fixtures (`rust/tests/fixtures`): 502
mutated configurations, 1,401 generated service files, 500 signed firmware
manifests and 2,000 version comparisons, all taken from Go v0.3.3. The Go unit
suite (727 tests) and its race detector, fuzzers and live DNS-filter tests were
not carried over; see `CURRENT_VALIDATION.md` for what is and is not covered.

### 3,000 deterministic API contracts

`scripts/gh-lab/contracts.py` builds six groups of 500 unique named requests:

| Category | Assertion |
|---|---|
| Authentication | Every protected route rejects absent, empty or forged sessions with 401 |
| Read-only sessions | Mutating routes reject a read-only session before decoding or applying a body |
| CSRF | Mutating routes reject absent, malformed or incorrect CSRF tokens |
| Origin | Mutating routes reject foreign, malformed or disallowed-scheme origins |
| Cross-site requests | Fetch Metadata blocks mutations even with a valid session and CSRF token |
| Configuration types | Preview rejects wrong JSON types across the synthetic full configuration |

The catalogue includes all 87 currently registered protected routes. Route
coverage is checked against Rust's route registration, so a newly added route
cannot silently lose its authentication tests. The observer has its own tests:

```sh
python3 -I -m unittest discover -s scripts/gh-lab -p 'test_contracts.py' -v
python3 -I scripts/gh-lab/contracts.py --catalog
cargo test --manifest-path rust/Cargo.toml --locked --test api_contract_matrix -- --nocapture
```

The Rust integration test runs the catalogue through real HTTPS, authentication
and SQLite, with a privileged-helper tripwire that must receive zero requests.
The same catalogue also runs against the installed router in the GitHub QEMU
lab. Positive read/preview controls and canonical-state/health checkpoints every
100 requests are additional checks, not counted as cases. A matching status
from the wrong protection layer is a failure. The first failure stops the matrix;
unexecuted cases are explicitly recorded, never counted as passes. Case JSONL,
JUnit, counts and a replayable catalogue digest are uploaded as artifacts;
session cookies, CSRF tokens and response bodies are not recorded.

These are 3,000 request contracts, not 3,000 independent features or complete
hardware validation. Successful configuration changes, confirmation/rollback,
real networking, firmware installation, supervision and chaos need the existing
Rust tests, packaging checks and installed lab scenarios as well.

### Complete GitHub run on a candidate branch

The workflows can be dispatched without opening a PR:

```sh
gh workflow run ci.yml --ref BRANCH
gh workflow run deep-validation.yml --ref BRANCH
gh workflow run service-supervision.yml --ref BRANCH
gh workflow run performance.yml --ref BRANCH
gh workflow run secret-scan.yml --ref BRANCH
gh workflow run codeql.yml --ref BRANCH
gh workflow run iso.yml --ref BRANCH
# Wait for this exact commit's Appliance ISO run to succeed, then:
gh workflow run lab.yml --ref BRANCH -f iso_run_id=SUCCESSFUL_ISO_RUN_ID
```

The lab refuses an ISO whose successful workflow head differs from its own
checkout. A complete run leaves `only` empty and requires all 3,000 contract
cases to execute and pass, plus the existing network, service-crash, PPPoE,
reboot/power-cut, firewall, CPU/memory/load and soak checks. A filtered run is
useful for diagnosis but is not a complete run. All credentials and state on
these disposable GitHub runners are synthetic; no owner router is contacted.
The catalogue refuses a non-loopback target unless the GitHub lab's QEMU PID
and disposable disk match. Its loopback fixture mode is only for integration
tests, and cannot be used against a LAN router.

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

- Rust formatting, builds, tests, clippy and dependency advisory checks;
- frontend lint, unit, build and Playwright E2E;
- clean Alpine packaging and first-run smoke tests;
- update activation/rollback and interrupted-update recovery;
- configuration transaction and recovery-state regressions;
- storage-pressure and appliance-health policies;
- installed API mutation/HTTP-abuse scenarios and update-journal regressions;
- CodeQL, secret scanning, Rust dependency auditing and shell/workflow checks;
- ARM64/QEMU smoke tests;
- isolated WAN-router-LAN DHCP/DNS/NAT/firewall tests;
- control-plane benchmarks.

Automated tests are regression evidence. They do not prove a specific ISP, NIC,
hypervisor, thermal profile or long-running deployment.

The Golden kernel-security regression rejects pre-fix, unknown-branch and
malformed kernel releases. Both ISO construction and the installed boot test
must pass the same baseline check; a matching `/lib/modules` directory alone
does not establish that the kernel contains the security fix.

## Network laboratory

The GitHub QEMU chaos run retains complete observer output in
`chaos-details.txt`, including transport exceptions. Its frozen-helper scenario
waits for the existing 120-second privileged IPC deadline with a 150-second
HTTP observer budget. It requires HTTP 422 and `RecoveryRequired`, unchanged
canonical configuration and refusal of another mutation. SIGCONT and a reboot
then reconcile the queued ambiguous request from durable canonical state;
the next scenario starts only after reconciliation and pending-state checks.
An empty HTTP status, a socket timeout or an unrelated rejection fails the test.

The DNS flood sends unique queries without pacing for twelve seconds, then
keeps the original five-second recovery interval. A provider-side resolver
answers the reserved `flood.lab.invalid` zone with zero TTL, so the flood
exercises uncached forwarding without sending the attack to public DNS.
Normal public names still use the real upstream. Before and after the flood,
exact local and forwarded A records and public resolution are required over
both UDP and TCP. A resolver restart, invalid/truncated response, wrong address,
timeout or routerd growth of at least 30 MiB fails. Fixture configuration is
applied through the normal privileged API and restored on exit; existing
firewall, DNS readiness and rollback verification remain enabled.

The first full run on `81a8588` passed all 3,000 contracts but failed the old
DNS flood observer. Its `dig +short` output did not preserve whether the
failure was local, upstream or transport related; that run does not establish
a DNS daemon crash. Its empty-status frozen-helper PASS was invalid because
the HTTP client timed out after 60 seconds, ahead of the 120-second IPC deadline.
The helper log later recorded the queued apply and a broken response pipe.
The stricter scenarios require a fresh full Lab run before claiming resolution.

The next full run, [40851414199](https://github.com/vladimirperovic/minimalrouter/actions/runs/40851414199)
on `71a5a2a`, again passed all 3,000 contracts and all 39 basic scenarios,
but failed two chaos scenarios. The DNS observer assumed an empty `records`
field was present; the API intentionally omits it, so that run never sent the
DNS flood. The helper observer reached the post-reboot comparison and found a
configuration difference. A local regression reproduced a canonical timestamp
mismatch: the engine assigned `updated_at`, then `save_config` replaced it in
its private copy. Persistence now retains the engine-assigned timestamp;
regular and confirmed commits are compared in full against durable state and
a reloaded engine. The lab comparison still includes timestamp and revision.
Differences report field paths without secret values.

That run also lost its artifact because the provider DNS log was not readable
by the upload action. The provider log now grants the runner access, and an
always-run step prepares the dedicated synthetic log directory for upload.
Complete scenario output also goes to the workflow console. These fixes still
require a new successful full Lab run; `71a5a2a` is not complete validation.

The full [Lab run 40862236667](https://github.com/vladimirperovic/minimalrouter/actions/runs/40862236667)
on `b8dceca` passed all 3,000 contracts, 39 basic scenarios, the strict frozen
helper scenario and a 518,525-query DNS flood. Chaos finished with 16 passes
and one failure: the text firewall fingerprint changed after killing all
daemons. That test did not save its two immediate firewall snapshots, so the
historical difference cannot be reconstructed exactly. The saved basic-suite
and final-chaos snapshots have the same policy, apart from the revision marker
advanced by intervening configuration changes.

The helper recreates the main and pause tables independently. A table can
move in the listing when recreated; nftables evaluates base chains by hook
priority, not table listing order ([nft manual](https://netfilter.org/projects/nftables/manpage.html)).
The lab now compares complete `nft -s -j list ruleset` snapshots, grouping only
independent tables in a stable order, removing allocation handles and runtime
packet/byte counter readings (nft 1.0.9 retains these in JSON even with `-s`). It keeps
object, rule and expression order, hooks, priorities, policies, revision names
and all set contents. Equal-priority base-chain registrations are rejected as
ambiguous. Missing/extra tables, unsupported objects, failed captures and any
policy difference fail. This replaces the old `sed` filter that discarded all
set contents. Raw and canonical snapshots, including the two mass-kill
snapshots, are retained in the artifact; a policy mismatch prints its diff.

CI exercises the observer in a new isolated Linux network namespace: recreate
an unchanged main table after the pause table, demonstrate the old text-hash
failure, then require semantic equality. Changed default policies, reordered
rules and deleted rules must still fail. No owner router or host firewall is
used for this regression. A new full Lab run remains necessary.

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

Packet forwarding stays in the Linux kernel, so API measurements are only
control-plane measurements. Target-host performance reports should record the
exact commit, Proxmox/guest versions, CPU/RAM/disk/NIC setup, test duration,
traffic direction, packet size, raw result and limitations.

## Privacy

Use synthetic fixtures in CI and documentation. Never commit real credentials,
private keys, backups, packet captures, public addresses, hostnames, MAC
addresses, VM inventory or household device data.
