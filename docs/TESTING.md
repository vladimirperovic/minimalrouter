# Testing Strategy

## 1. Principle

Router configuration tests must prove failure behavior, not only the happy
path. Every mutation must end in exactly one of two states:

- The complete new configuration is active and recorded.
- The complete previous known-good configuration is restored.

Partial success is a defect.

## 2. Test layers

### Go unit tests

Run without root or Linux networking dependencies.

- Domain and API validation
- Cross-field invariants
- Authorization decisions
- Transaction state machine
- Revision and idempotency behavior
- Deterministic configuration generation
- Redaction
- Snapshot retention and compatibility
- Recovery credential/session behavior
- WAN/LAN recommendation scoring and exclusions
- Device-profile schedule validation and nftables rendering
- Signed-manifest verification and A/B slot transitions
- Migration logic

Run:

```sh
go test -race ./...
go vet ./...
```

### Dashboard unit tests

Vitest covers pure frontend behavior such as profile construction, validation,
and human-readable schedule descriptions.

```sh
pnpm --dir web install --frozen-lockfile
pnpm --dir web test
```

A unit test must not make a real router API request or depend on the developer's
browser state.

### Dashboard browser E2E tests

Playwright builds and serves the production dashboard, mocks the versioned API,
and drives critical user workflows in Chromium. The minimum required flows are:

- first-run interface discovery and explicit role confirmation;
- authentication/session restoration;
- DNS Filter navigation;
- opening the Kids profile editor;
- the default `19:00`-`23:59` weekday window;
- YouTube, Steam, and Wikipedia selected;
- full-day Saturday/Sunday access selected;
- successful profile serialization and error display.

Run:

```sh
pnpm --dir web build
pnpm --dir web exec playwright install chromium
pnpm --dir web test:e2e
```

E2E fixtures must contain only synthetic addresses, hostnames, devices, and
credentials.

### Component contract tests

Run in a pinned Alpine container or VM with the real binaries.

- `nft --check` and atomic load behavior
- `dnsmasq --test` behavior, including nftset directives when device profiles
  are enabled
- pppd configuration and permissions
- bounded dnsmasq lease parsing and authenticated live-device telemetry
- WireGuard argument/config mapping
- QoS qdisc installation and inspection
- Cloudflare DDNS `inadyn` validation, bounded update, service health, and
  rollback
- Wi-Fi AP capability checks, bridge membership, hostapd health,
  commit-confirm, and rollback
- unsupported Cloudflare Tunnel and DoH settings fail closed
- OpenRC service reload/restart behavior
- SQLite locking, corruption detection, and recovery
- recovery console commands operate only on the local store and create undo
  snapshots before disruptive changes
- release staging rejects untrusted signatures, symlinks, missing files, unsafe
  paths, and hash mismatches

Containers may validate generators, but they do not replace boot, kernel, and
network tests.

### Integration tests

Run in disposable network namespaces or VMs.

- WAN/LAN role assignment and selection of a distinct pair
- PPPoE establishment and reconnect
- DHCP allocation and static leases
- DNS forwarding
- Firewall allow/deny
- NAT forwarding from LAN to WAN; new WAN port forwards must remain rejected
- WireGuard connectivity
- QoS lifecycle using controlled test interfaces
- DNS Filter destination-set population and schedule transitions
- managed-device direct DNS and DNS-over-TLS bypass rejection
- schedule closure terminates existing matching flows
- Snapshot apply and restore
- Recovery password/TOTP reset and session revocation
- A/B update stage, explicit activation, health verification, and rollback
- Concurrent and repeated API mutations

### End-to-end appliance tests

Boot the actual image and drive the public HTTPS API/UI plus local recovery
console.

- Installation and first-run wizard
- Interface recommendation and manual confirmation
- Authentication, session rotation, timeout, and logout
- CSRF and same-origin rejection
- Complete configuration flows
- Commit-confirmed LAN and firewall changes
- Backup and restore
- Password/TOTP recovery, LAN recovery, snapshot restore, and factory reset
- Signed update stage/activate/rollback
- Reboot during every durable transaction stage

### Platform tests

For each claimed platform:

- Install and first boot
- Interface discovery and stable naming
- Reboot and shutdown
- Timekeeping and entropy availability
- Snapshot/restore and update rollback
- 1 GbE baseline throughput
- Optional higher-speed measurements where virtual/hardware support exists

## 3. Failure injection matrix

Inject at least:

- Invalid API payload
- Stale revision
- Generator failure
- Full disk
- Read-only filesystem
- Snapshot write failure
- Component preflight failure
- nftables apply failure
- dnsmasq nftset syntax failure
- Service reload timeout
- Service starts then exits
- Lost default route
- Lost management address
- Failed connectivity probe
- Missing administrator confirmation
- `routerd` crash
- `router-applyd` crash
- Recovery interruption after snapshot and before commit
- Update interruption while copying, before slot rename, after slot rename, and
  during pointer replacement
- Power loss/reboot before and after each durable transaction marker
- Corrupted current snapshot
- Unsupported database, backup, manifest, or release version

Each case asserts active Linux state, database revision, service health, update
slot state, and audit result after recovery.

## 4. Security tests

- WAN cannot reach management ports over IPv4 or IPv6.
- Unauthenticated and expired sessions cannot read sensitive state or mutate.
- Cookies have all required attributes.
- Login rotates the session ID.
- CSRF token absence/mismatch and cross-origin requests fail.
- Host-header and DNS-rebinding defenses fail closed.
- Login and expensive operations are rate-limited.
- Unknown fields, oversized bodies, invalid UTF-8, path traversal, and command
  metacharacters are rejected.
- The privileged helper rejects unknown peers, operations, paths, flags, and
  oversized messages.
- `router-applyd` has no-new-privileges, no core dumps, bounded descriptors and
  processes, a fixed executable path, and no inherited dynamic-loader hooks.
- API, logs, audit events, process listings, generated public files, backups,
  and diagnostic bundles contain no unapproved secret values.
- Recovery has no network endpoint and credential changes revoke all sessions.
- Update and restore reject tampered signatures, checksums, unsafe files, and
  incompatible versions.
- The release workflow refuses an absent signing key and lightweight tags.
- Dashboard production assets contain no inline script blocks. Newly refactored
  components use external stylesheets; any temporary legacy style-attribute
  allowance remains explicitly bounded by CSP and repository checks.

Maintain regression tests for every reported vulnerability.

## 4.1 Current pilot evidence (2026-07-28)

The macOS host suite passed `go test ./...`, `go test -race ./...`,
`go vet ./...`, frontend lint/type-check/build, and the dependency audit.
The then-current change set was rebuilt and exercised in an Alpine 3.22.5
ARM64 VM: authentication, process separation, management-boundary checks,
global DNS filtering, CAKE, fq_codel, QoS removal, nftables, unsupported-feature
rejection, and commit-confirm revision consistency produced the expected result.
A subsequent clean 512 MiB VM proved that Bash and `wg-quick` are absent and ran
the opt-in production WireGuard lifecycle integration test: preflight, real
handshake, five encrypted packets, PPPoE-aware MTU, peer route, and cleanup
passed. See [the dated security review](SECURITY_REVIEW.md) and
[resource/network report](RESOURCE_AND_HARDWARE_TEST.md) for exact evidence.

The recovery, device-profile, browser E2E, and signed-update additions in the
current unreleased branch require new dated appliance evidence before their
results may be added to this pilot section. Real PPPoE, physical NIC/radio
behavior, signed recovery media, and an external WAN scan remain manual release
gates.

The root Linux test is gated so an ordinary host test never alters interfaces:

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
  go test -c -o bin/router-applyd-integration-test ./cmd/router-applyd
MINIMALROUTER_WIREGUARD_INTEGRATION=1 \
  ./bin/router-applyd-integration-test \
  -test.run '^TestBashlessWireGuardLifecycleIntegration$' -test.v
```

Run the resulting binary only inside a disposable root VM/network namespace.

## 5. Performance tests

Record the exact hardware, NICs, hypervisor, vCPU count, RAM, Alpine/kernel
version, offload settings, packet sizes, directions, and test duration.

Measure:

- Boot to forwarding-ready and management-ready
- Idle and normal-operation memory
- Configuration apply latency
- PPPoE reconnect time
- Routing/NAT throughput and packets per second
- Device-profile rule and set scaling
- CPU use and packet loss under load
- Management responsiveness during traffic load

The Go control plane must not be used as the packet-forwarding benchmark path.

## 6. Test environments

- Tests never alter a developer's active host firewall or routes.
- Real network tests run in disposable VMs/namespaces.
- External services use dedicated test accounts and least-privilege tokens.
- CI secrets are masked, short-lived where possible, and unavailable to
  untrusted contributions.
- The release signing key is available only to protected tag workflows and is
  never exposed to pull-request jobs.
- Test logs and artifacts pass secret scanning before upload.

## 7. Release evidence

A release candidate includes machine-readable results for:

- Go unit/race/vet and vulnerability checks
- Dashboard lint, unit, production build, and browser E2E suites
- Platform matrix
- Security release gates
- Recovery and update/rollback tests
- Backup/restore tests
- Boot and memory targets
- Throughput on reference hardware
- SHA-256 checksums, signed manifests, SPDX SBOMs, and provenance attestations

Release claims are based on recorded results, not expected capability.
