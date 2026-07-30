# Changelog

All notable user-visible changes are documented here. The project intends to use
semantic versioning when the first stable release is published. During early
alpha, compatibility may change between commits.

## [Unreleased]

### Added

- Durable update-operation journal for crash-safe A/B activation and rollback.
- Recovery-safe bootstrap executables independent of the candidate firmware slot.
- Failure-injection coverage for interrupted activation, rollback, corrupt
  journals, state-write failures, and concurrent update operations.
- Deterministic router failure scenarios covering lost privileged responses,
  confirmation-response loss, simulated power loss during pending changes,
  WireGuard-only management changes, failed automatic rollback, SQLite commit
  failure, unverified helper results, and boot reconciliation.
- Explicit `RecoveryRequired` transaction and RPC outcome for runtime states whose
  rollback has not been verified.
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

- Ambiguous `routerd` to `router-applyd` transport failures retry the exact same
  transaction ID so the helper can return its persisted idempotent result.
- WireGuard key, port, address, peer, and route changes require commit-confirm
  while management is WireGuard-only.
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
- Privileged confirmation now recognizes WireGuard control-plane changes that can
  break a WireGuard-only management path.
- A rollback that fails verification is no longer falsely reported as
  `RolledBack`; it is reported as `RecoveryRequired`.
- SQLite commit failure reports `RolledBack` only after a successful, verified
  privileged restoration of the previous configuration.
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

- Signed staging rejects untrusted keys, unsafe paths, symlinks, non-regular
  files, hash mismatches, duplicate versions, and package-supplied hooks.
- The verify-to-copy window is closed by re-verification of staged files.
- Update/recovery dispatchers use a fixed PATH and remove loader/environment hooks.
- Runtime data, credentials, private keys, backups, packet captures, databases,
  VM images, and private network inventory are rejected by repository hygiene.
- Recovery has no network endpoint and credential changes revoke sessions.
- Device-profile rules are evaluated before established-connection acceptance so
  expired policy flows are not grandfathered.

### Known limitations

- Early alpha; not supported as an unattended production firewall.
- No stable signed ISO or owner-qualified recovery media.
- Real target Proxmox, NIC, PPPoE, external scan, long-duration, and destructive
  recovery evidence is still required.
- IPv6 parity, VLAN workflows, multi-WAN, HA, IDS/IPS, and a broad package
  ecosystem are not current stable features.
- Same-kernel namespace throughput is not a physical or VirtIO performance claim.
