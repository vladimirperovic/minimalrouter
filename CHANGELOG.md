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
- Private `PROXMOX_AI_HANDOFF.md` for another AI agent to identify, preserve,
  start, update, test, recover, and report on the existing owner-created VM
  without exposing real infrastructure data.
- Expanded Proxmox pilot, testing, recovery, and evidence documentation.
- Local recovery console for credential reset, LAN repair, snapshot restore, and
  factory reset with session revocation and undo snapshots.
- DNS Filter device profiles and scheduled Kids profile workflow.
- Frontend unit and Playwright E2E coverage.
- Ed25519 release manifests, SHA-256 checksums, SPDX SBOMs, provenance, and
  explicit A/B staging/activation/rollback.

### Changed

- CI and tagged-release `checkout`, `setup-go`, and `setup-node` actions use v7;
  evidence-upload workflows use `upload-artifact` v7.
- Dashboard development uses TypeScript 6.0.3 and Node.js type definitions 26.1.2.
- TypeScript 6 CSS side-effect import checks are satisfied with a Vite client
  declaration rather than disabling stricter compiler behavior.
- Active A/B slots supply `routerd`, `router-applyd`, and dashboard assets through
  stable dispatchers.
- Update and recovery commands execute from an independent bootstrap payload.
- Update staging re-verifies copied files and uses atomic synchronized filesystem
  operations.
- Recovery mutations use transactional SQLite operations.
- CLI help and read-only status commands avoid privileged side effects.
- Documentation now uses `docs/CURRENT_VALIDATION.md` for current repository state
  and preserves dated hardware/security reports as history.
- The private Proxmox procedure requires read-only discovery, isolated LAN,
  test/NAT WAN, known-good snapshots/backups, and pfSense rollback before changes.
- Node.js remains build-time only; the router runtime is Bash-free.

### Fixed

- Interrupted update pointer changes are reconciled through the durable journal.
- Corrupt state no longer leaves a blocking partially staged version.
- State-save failures remove incomplete final slots.
- Unsafe or broken update pointers are rejected.
- Recovery session-deletion failures roll back the full transaction.
- Dashboard TypeScript 6 builds accept reviewed CSS side effects.
- Clean Alpine CI exercises signed update activation and rollback.
- TOTP disable verifies request, password, code, and session revocation in order.

### Security

- Signed staging rejects untrusted keys, unsafe paths, symlinks, non-regular
  files, hash mismatches, duplicate versions, and package-supplied hooks.
- Staged files are re-verified after copy.
- Dispatchers use a fixed PATH and sanitized environment.
- Runtime data, credentials, keys, backups, packet captures, databases, VM images,
  and real Proxmox/network inventory are rejected by repository hygiene.
- Recovery has no network endpoint and credential changes revoke sessions.

### Known limitations

- Early alpha; not an unattended production firewall.
- The owner-created Proxmox VM still needs the dated private test report described
  in `docs/PROXMOX_AI_HANDOFF.md`.
- No stable signed ISO or owner-qualified recovery media.
- Real NIC, PPPoE, WireGuard, external scan, long-duration, and destructive
  recovery evidence is still required.
- Same-kernel namespace throughput is not a physical or VirtIO performance claim.
