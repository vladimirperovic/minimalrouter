# Changelog

All notable user-visible changes are documented here. The project intends to use
semantic versioning when the first stable release is published. During early
alpha, compatibility may change between commits.

## [Unreleased]

### Added

- Static DNS records (`dns.records`): fixed name → IP entries served by the
  local resolver via `host-record=` regardless of DHCP, for hosts with static
  addressing (e.g. `immich.local` on the isolated media network).
- Isolated extra LAN segments (`firewall.extra_lans`): a second network behind a
  dedicated router interface with no WAN/LAN egress and no router services
  (no DHCP/DNS). Only the listed source networks may reach the single exposed
  service (`dst_ip:dst_port`); input is anti-spoofed and ICMP-only, everything
  else is dropped by the default policy. Used for the isolated Immich media
  network (`10.20.30.0/24`).

## [v0.1.0] — 2026-08-03

Beta milestone: the full system is validated and running in production on
Proxmox VM 108. See the GitHub release on `minimalrouterhome`.

### Added

- WireGuard peer provisioning by name only: the router auto-assigns the next
  free client IP in the subnet and defaults the server endpoint to the DDNS
  domain (`nextFreeWireGuardIP` with subnet-bound allocation).
- Central Security page: firewall posture, failed-login and CSRF/origin-reject
  counters, and a bounded live security-events feed; account controls moved into
  the VP profile dropdown.
- Overview security chips (firewall, rejected requests, previous login) and a
  router-icon sidebar with the revision pinned at the bottom.

### Fixed

- Production deploy procedure hardened: binary swap now stops services first
  (avoiding `Text file busy`) and gunzips on the guest before install.

## [v0.1.1] — 2026-08-03

### Added

- `trusted_networks` management access gate: only source networks listed in the
  config (plus loopback) can reach the administrative Web UI/API; enforced on the
  real TCP peer address before authentication, never via forwarded headers, with
  an operator-lockout guard on configuration changes and a Security-tab panel for
  adding/removing CIDR networks. Does not replace the administrator password.
- Bounded storage-pressure policy with explicit 80% warning and 90% critical
  thresholds, authenticated runtime telemetry, and HTTP 507 rejection of durable
  management mutations when safe persistence cannot be guaranteed.
- Periodic SQLite retention maintenance and passive WAL checkpoints without
  `VACUUM` or forwarding-plane interruption.
- Bounded `routerd` and `router-applyd` log rotation (1 MiB active logs, four
  compressed rotations) in Alpine installation and distribution paths.
- Central authenticated appliance-health model with Healthy, Warning, Degraded,
  Recovery required, and Unknown states covering recovery, storage, memory,
  conntrack, time, WAN/gateway, core supervision, DNS/DHCP, PPPoE, WireGuard,
  update state, and encrypted-backup age.
- Overview appliance-health banner with independent 15-second refresh and concise
  explanations for non-healthy signals.
- Durable update-operation journal for crash-safe A/B activation and rollback.
- Durable privileged-operation intent and completed-result records written around
  each `router-applyd` side-effect boundary.
- Recovery-safe bootstrap executables independent of the candidate firmware slot.
- Failure-injection coverage for interrupted activation, rollback, corrupt
  journals, state-write failures, and concurrent update operations.
- Deterministic router failure scenarios covering lost privileged responses,
  confirmation-response loss, simulated power loss during pending changes,
  WireGuard-only management changes, failed automatic rollback, SQLite commit
  failure, unverified helper results, boot reconciliation, incomplete helper
  intents, corrupt helper metadata, and two-phase confirmation ordering.
- Explicit `RecoveryRequired` transaction and RPC outcome for runtime states whose
  commit or rollback has not been verified.
- Allowlisted `RECONCILE` privileged operation that may supersede unresolved
  helper journal state only by applying and verifying SQLite canonical state.
- Separate `COMMIT_CONFIRMED` phase after runtime confirmation and SQLite commit.
- Failure-scenario matrix for IPC, power, process, storage, firewall, DHCP/DNS,
  PPPoE, WireGuard, update, backup, restore, and target-Proxmox testing.
- Fuzz targets for malformed unauthenticated API requests and update journals.
- Isolated WAN-router-LAN network namespace laboratory covering DHCP, DNS, NAT,
  firewall, TCP, UDP, parallel flows, packet loss, latency, and WAN port checks.
- API and update-state performance workflows with `ns/op`, `B/op`, and
  `allocs/op` artifacts.
- ARM64 QEMU smoke testing for recovery-safe commands.
- High-confidence `gosec`, `shellcheck`, `actionlint`, Linux binary inspection,
  and executable-stack rejection.
- Current validation document separating automated evidence from target-host
  Proxmox and hardware gates.
- Expanded Proxmox pilot, testing, recovery, and evidence documentation.
- Professional project README, documentation index, governance, privacy,
  contribution, support, release, and community documentation.
- Dashboard screenshot and synthetic documentation data.
- Local recovery console for credential reset, LAN repair, snapshot restore, and
  factory reset with session revocation and undo snapshots.
- DNS Filter device profiles and scheduled Kids profile workflow.
- Frontend unit and Playwright E2E coverage.
- Ed25519 release manifests, SHA-256 checksums, SPDX SBOMs, provenance, and
  explicit A/B staging/activation/rollback.

### Changed

- Nonessential gateway sample/reconnect history is shed under critical storage
  pressure while live probing and in-memory gateway health continue.
- Recovery and configuration writes remain fail-closed under critical storage
  pressure; read-only status, previews, verification and encrypted backup export
  stay available.
- Ambiguous `routerd` to `router-applyd` transport failures retry the exact same
  transaction ID so the helper can return its persisted idempotent result.
- Privileged mutations write an intent record before side effects and a validated
  final result afterward; an incomplete intent blocks ordinary mutation.
- Privileged transaction, pending-confirmation, and last-good records are
  structurally validated and fail closed when unreadable or inconsistent.
- WireGuard key, port, address, peer, and route changes require commit-confirm
  while management is WireGuard-only.
- Commit-confirm now follows a two-phase persistence order: verify candidate
  runtime, commit the exact candidate revision to SQLite, then record helper
  `last-good` and clear pending state.
- A failed explicit final helper commit retry uses a fresh transaction ID while
  transport retries inside the same attempt retain the same ID.
- A failed automatic commit-confirm rollback retains pending/candidate access,
  blocks overlapping configuration, schedules another attempt, and uses a fresh
  rollback transaction ID rather than replaying a cached failed response.
- Core GitHub CI actions and artifact upload use v7.
- Dashboard development uses TypeScript 6.0.3 and Node.js type definitions 26.1.2.
- TypeScript 6 CSS side-effect import checks are satisfied with the Vite client
  declaration rather than disabling the stricter compiler behavior.
- Active A/B slots now supply `routerd`, `router-applyd`, and dashboard assets
  through stable dispatchers.
- Update and recovery commands always execute from an independent bootstrap
  payload, preventing a candidate slot from replacing rollback tooling.
- Update staging now re-verifies copied files against the signed manifest and
  performs atomic, synchronized filesystem operations.
- Recovery authentication reset, LAN change, snapshot restore, and factory reset
  use transactional SQLite operations.
- CLI help and read-only status commands avoid privileged side effects.
- The release signer safely handles raw Ed25519 keys and can emit the public trust
  anchor covered by the signed manifest.
- Documentation now treats dated resource/security reports as historical evidence
  and uses `docs/CURRENT_VALIDATION.md` for current repository status.
- Proxmox guidance explicitly requires read-only VM discovery, isolated LAN,
  test/NAT WAN, graceful lifecycle handling, and pfSense rollback.
- The first-run wizard uses discovered interface choices and external CSS.
- `router-applyd` starts with no-new-privileges, disabled dumpability, bounded
  resources, fixed executable path, and sanitized loader environment.
- Node.js remains build-time only; the router runtime is Bash-free.

### Fixed

- Lost IPC responses after a completed apply or confirmation no longer force an
  unnecessary unknown outcome when the helper can return the saved result.
- A helper restart after an incomplete privileged operation can no longer treat
  the same request as fresh and repeat side effects.
- A different transaction can no longer bypass an unresolved helper intent or
  `RecoveryRequired` result.
- Corrupt or valid-but-empty helper metadata is no longer treated as absent state.
- Privileged RPC responses with contradictory success, verification, rollback, or
  recovery flags are rejected.
- Privileged confirmation now recognizes WireGuard control-plane changes that can
  break a WireGuard-only management path.
- A rollback that fails verification is no longer falsely reported as
  `RolledBack`; it is reported as `RecoveryRequired`.
- SQLite commit failure reports `RolledBack` only after a successful, verified
  privileged restoration of the previous configuration.
- Helper `last-good` no longer advances before the corresponding SQLite canonical
  commit during disruptive confirmation.
- Failure to acknowledge helper `last-good` after SQLite commit no longer enables
  timeout rollback of the now-canonical candidate.
- A crash between update pointer changes can no longer silently lose the intended
  activation/rollback transition; journal reconciliation restores consistency.
- Corrupt state no longer leaves a blocking partially staged version.
- State-save failures remove incomplete final slots.
- Unsafe, broken, absolute, or traversal update pointers are rejected.
- Recovery session-deletion failures roll back the entire credential/configuration
  transaction.
- Raw 64-byte signing keys no longer risk scanner-reported ignored close errors.
- Dashboard TypeScript 6 builds accept reviewed CSS module side effects.
- Clean Alpine CI exercises the signed update, active slot, dashboard marker, and
  rollback path.
- TOTP disable validates the request, current password, code, and session
  revocation in the correct order.
- Distribution builds create staging directories before copying binaries and
  dashboard assets.

### Security

- Critical disk pressure blocks durable management mutations rather than allowing
  runtime changes to be reported successful without persistent evidence.
- Health collection is read-only and does not inspect or return PPPoE passwords,
  WireGuard private keys, provider tokens, administrator credentials, backups, or
  other secret material.
- Signed staging rejects untrusted keys, unsafe paths, symlinks, non-regular
  files, hash mismatches, duplicate versions, and package-supplied hooks.
- The verify-to-copy window is closed by re-verification of staged files.
- Update/recovery dispatchers use a fixed PATH and remove loader/environment hooks.
- Privileged side effects are never started without a durable intent marker.
- Unknown privileged outcomes block future mutation until canonical
  reconciliation succeeds.
- Only the typed `RECONCILE` path may override unresolved helper journal state;
  ordinary apply and confirmation operations remain blocked.
- Runtime data, credentials, private keys, backups, packet captures, databases,
  VM images, and private network inventory are rejected by repository hygiene.
- Recovery has no network endpoint and credential changes revoke sessions.
- Device-profile rules are evaluated before established-connection acceptance so
  expired policy flows are not grandfathered.
- QoS activation is non-fatal everywhere: a missing or inactive qdisc no longer
  aborts apply, verify, or boot reconciliation; it is logged and retried on the
  next apply cycle.
- Squid shuts down cleanly on service stop via a bounded `shutdown_lifetime`.
- Minimum administrator password length is 12 characters (was 15) to match the
  deployed first-run credentials.

### Known limitations

- Early alpha; not supported as an unattended production firewall.
- No stable signed ISO or owner-qualified recovery media.
- Real target Proxmox, NIC, PPPoE, external scan, long-duration, full-disk,
  inode-exhaustion, read-only-filesystem, process-kill, and destructive power-loss
  evidence is still required.
- IPv6 parity, VLAN workflows, multi-WAN, HA, IDS/IPS, and a broad package
  ecosystem are not current stable features.
- Same-kernel namespace throughput is not a physical or VirtIO performance claim.
