# Testing Strategy

## 1. Principle

Router configuration tests must prove failure behavior, not only the happy
path. Every mutation must end in exactly one of two states:

- The complete new configuration is active and recorded.
- The complete previous known-good configuration is restored.

Partial success is a defect.

## 2. Test layers

### Unit tests

Run without root or Linux networking dependencies.

- Domain and API validation
- Cross-field invariants
- Authorization decisions
- Transaction state machine
- Revision and idempotency behavior
- Deterministic configuration generation
- Redaction
- Snapshot retention and compatibility
- Migration logic

### Component contract tests

Run in a pinned Alpine container or VM with the real binaries.

- `nft --check` and atomic load behavior
- `dnsmasq --test` behavior
- pppd configuration and permissions
- bounded dnsmasq lease parsing and authenticated live-device telemetry
- WireGuard argument/config mapping
- QoS qdisc installation and inspection
- Cloudflare DDNS `inadyn` validation, bounded update, service health, and
  rollback
- Wi-Fi AP capability checks, bridge membership, hostapd health,
  commit-confirm, and rollback
- unsupported Cloudflare Tunnel/DoH/per-device DNS settings fail closed
- OpenRC service reload/restart behavior
- SQLite locking, corruption detection, and recovery

Containers may validate generators, but they do not replace boot, kernel, and
network tests.

### Integration tests

Run in disposable network namespaces or VMs.

- WAN/LAN role assignment
- PPPoE establishment and reconnect
- DHCP allocation and static leases
- DNS forwarding
- Firewall allow/deny
- NAT forwarding from LAN to WAN; new WAN port forwards must remain rejected
- WireGuard connectivity
- QoS lifecycle using controlled test interfaces
- Snapshot apply and restore
- Concurrent and repeated API mutations

### End-to-end tests

Boot the actual image and drive the public HTTPS API/UI.

- Installation and first-run wizard
- Authentication, session rotation, timeout, and logout
- CSRF and same-origin rejection
- Complete configuration flows
- Commit-confirmed LAN and firewall changes
- Backup and restore. Automatic update and factory-reset tests become required
  only when those routes are implemented.
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
- Service reload timeout
- Service starts then exits
- Lost default route
- Lost management address
- Failed connectivity probe
- Missing administrator confirmation
- `routerd` crash
- `router-applyd` crash
- Power loss/reboot before and after each durable transaction marker
- Corrupted current snapshot
- Unsupported database or backup version

Each case asserts active Linux state, database revision, service health, and
audit result after recovery.

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
- API, logs, audit events, process listings, generated public files, backups,
  and diagnostic bundles contain no unapproved secret values.
- Update and restore reject tampered signatures, checksums, and incompatible
  versions.

Maintain regression tests for every reported vulnerability.

## 4.1 Current pilot evidence (2026-07-28)

The macOS host suite passes `go test ./...`, `go test -race ./...`,
`go vet ./...`, frontend lint/type-check/build, and the dependency audit.
The current change set was then rebuilt and exercised in an Alpine 3.22.5
ARM64 VM: authentication, process separation, management-boundary checks,
global DNS filtering, CAKE, fq_codel, QoS removal, nftables, unsupported-feature
rejection, and commit-confirm revision consistency all produced the expected
result. A subsequent clean 512 MiB VM proved that Bash and `wg-quick` are absent
and ran the opt-in production WireGuard lifecycle integration test: preflight,
real handshake, five encrypted packets, PPPoE-aware MTU, peer route, and cleanup
passed. See [the dated security review](SECURITY_REVIEW.md) and
[resource/network report](RESOURCE_AND_HARDWARE_TEST.md) for exact evidence.
Real PPPoE, physical NIC/radio behavior, signed recovery media, and an external
WAN scan remain manual release gates.

The root Linux test is gated so an ordinary host test never alters interfaces:

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
  go test -c -o bin/router-applyd-integration-test ./cmd/router-applyd
MINIMALROUTER_WIREGUARD_INTEGRATION=1 \
  ./bin/router-applyd-integration-test \
  -test.run '^TestBashlessWireGuardLifecycleIntegration$' -test.v
```

Run the resulting binary only inside a disposable root VM/network namespace.

## 4.2 IoT and device-schedule validation

Unit and generator tests must cover:

- dedicated and VLAN IoT model validation, non-overlapping subnets, interface
  ownership, VLAN ID bounds, and cross-zone MAC reservation conflicts;
- separate tagged LAN/IoT dnsmasq pools and static reservations;
- LAN↔IoT drops before `ct state established,related accept`;
- management absence on the IoT input path and IoT-to-WAN-only forwarding;
- exact weekday/weekend and time expressions before generic forwarding accepts;
- paused policies producing no active DNS sets or device rules;
- frontend construction of the weekday 19:00 YouTube/Steam and weekend all-day
  template.

An isolated hardware or namespace test must additionally verify real DHCP
renewal, a schedule transition with an existing connection, DNS-cache behavior,
reboot/rollback, a dedicated NIC, a managed-switch VLAN trunk, and the selected
timezone across a daylight-saving transition. Capture only synthetic addresses
and redacted evidence.

## 5. Performance tests

Record the exact hardware, NICs, hypervisor, vCPU count, RAM, Alpine/kernel
version, offload settings, packet sizes, directions, and test duration.

Measure:

- Boot to forwarding-ready and management-ready
- Idle and normal-operation memory
- Configuration apply latency
- PPPoE reconnect time
- Routing/NAT throughput and packets per second
- CPU use and packet loss under load
- Management responsiveness during traffic load

The Go control plane must not be used as the packet-forwarding benchmark path.

## 6. Test environments

- Tests never alter a developer's active host firewall or routes.
- Real network tests run in disposable VMs/namespaces.
- External services use dedicated test accounts and least-privilege tokens.
- CI secrets are masked, short-lived where possible, and unavailable to
  untrusted contributions.
- Test logs and artifacts pass secret scanning before upload.

## 7. Release evidence

A release candidate includes machine-readable results for:

- Unit and integration suites
- Platform matrix
- Security release gates
- Update/rollback and backup/restore
- Boot and memory targets
- Throughput on reference hardware
- SBOM and vulnerability scan

Release claims are based on recorded results, not expected capability.
