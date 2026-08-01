# Changelog

All notable user-visible changes are documented here. The project intends to use
semantic versioning when the first stable release is published. During early
alpha, compatibility may change between commits.

## [Unreleased]

### Added

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
- Durable privileged-operation intent before network side effects and a validated
  completed-result journal for idempotent response replay.
- Explicit `RecoveryRequired` outcome that blocks ordinary configuration until
  canonical reconciliation succeeds.
- Allowlisted `RECONCILE` operation that may supersede unresolved helper state
  only by applying and verifying SQLite canonical configuration.
- Separate `COMMIT_CONFIRMED` phase after runtime confirmation and SQLite commit.
- Recovery-safe bootstrap executables independent of the candidate firmware slot.
- Deterministic failure coverage for lost apply/confirmation responses,
  incomplete intents, corrupt helper metadata, contradictory RPC outcomes,
  WireGuard-only management changes, failed rollback, SQLite commit failure,
  final helper acknowledgement failure, boot reconciliation, and two-phase
  confirmation ordering.
- Clean-Alpine destructive smoke coverage that removes volatile nftables, LAN,
  and WireGuard runtime and verifies reconstruction from confirmed state.
- Gateway-quality/PPPoE monitoring with bounded history, latency, jitter, packet
  loss, reconnect history, and flapping detection.
- Fuzz targets for malformed unauthenticated API requests and update journals.
- Isolated WAN-router-LAN network namespace laboratory covering DHCP, DNS, NAT,
  firewall, TCP, UDP, parallel flows, packet loss, latency, and WAN port checks.
- API and update-state performance workflows with `ns/op`, `B/op`, and
  `allocs/op` artifacts.
- ARM64 QEMU smoke testing for recovery-safe commands.
- High-confidence `gosec`, `shellcheck`, `actionlint`, Linux binary inspection,
  and executable-stack rejection.
- Private `PROXMOX_AI_HANDOFF.md` with current storage/health behavior, durable
  interruption points, stop conditions, evidence format, and rollback rules for
  the owner-created VM.
- Local recovery console for credential reset, LAN repair, snapshot restore, and
  factory reset with session revocation and undo snapshots.
- DNS Filter device profiles and scheduled Kids profile workflow.
- Frontend unit and Playwright E2E coverage.
- Ed25519 release manifests, SHA-256 checksums, SPDX SBOMs, provenance, and
  explicit A/B staging/activation/rollback.

### Changed

- Shared application/runtime code is synchronized with public baseline
  `vladimirperovic/minimalrouter@df99909a7b161b1a0bcc7149b9dfeaf6a2a51796`,
  including public PR #28, PR #29, and their final validation documentation.
- Nonessential gateway sample/reconnect history is shed under critical storage
  pressure while live probing and in-memory gateway health continue.
- Recovery and configuration writes remain fail-closed under critical storage
  pressure; read-only status, previews, verification and encrypted backup export
  stay available where no durable mutation is required.
- Ambiguous transport failures retry the exact same transaction ID so the helper
  can return a persisted idempotent result without repeating side effects.
- Disruptive confirmation now proceeds in order: verify candidate runtime,
  commit the exact candidate revision to SQLite, then verify runtime again,
  record helper `last-good`, and clear pending state.
- Explicit final helper-commit retry uses a fresh transaction ID after a recorded
  storage failure, while transport retry within one attempt retains the same ID.
- WireGuard key, port, address, peer, and allowed-route changes require
  commit-confirm while management is WireGuard-only.
- Failed automatic rollback retains pending/candidate access, blocks overlapping
  mutation, and retries with a fresh rollback transaction ID.
- `router-applyd` exposes its Unix socket only after startup reconciliation has
  completed successfully.
- Private documentation now distinguishes fully green public automated evidence,
  synchronized shared-source parity, and remaining target-Proxmox gates.
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
- Node.js remains build-time only; the router runtime is Bash-free.

### Fixed

- Malformed/partial `/api/v1/health` payloads no longer crash the dashboard; the
  Overview banner fails closed to an unavailable/unknown display.
- A helper restart after an incomplete privileged operation can no longer treat
  the request as fresh and repeat side effects.
- A different transaction can no longer bypass unresolved helper intent or
  `RecoveryRequired` state.
- Corrupt, unreadable, valid-but-empty, or structurally inconsistent transaction,
  pending-confirmation, and last-good metadata now fails closed.
- Contradictory privileged RPC outcomes are rejected instead of being interpreted
  as proof of commit or rollback.
- Lost IPC responses after completed apply or confirmation return the persisted
  result when available.
- Rollback is reported only after successful, verified restoration; otherwise the
  result remains `RecoveryRequired`.
- Helper `last-good` no longer advances before SQLite canonical commit during a
  disruptive change.
- Failure after SQLite commit but before helper acknowledgement no longer enables
  timeout rollback to the older helper state.
- Unconfirmed power-loss state is reconciled from SQLite before management
  readiness.
- Interrupted update pointer changes are reconciled through the durable journal.
- Corrupt state no longer leaves a blocking partially staged version.
- State-save failures remove incomplete final slots.
- Unsafe or broken update pointers are rejected.
- Recovery session-deletion failures roll back the full transaction.
- Dashboard TypeScript 6 builds accept reviewed CSS side effects.
- Clean Alpine CI exercises signed update activation and rollback.
- TOTP disable verifies request, password, code, and session revocation in order.

### Security

- Critical disk pressure blocks durable management mutations rather than allowing
  runtime changes to be reported successful without persistent evidence.
- Health collection is read-only and does not inspect or return PPPoE passwords,
  WireGuard private keys, provider tokens, administrator credentials, backups, or
  other secret material.
- Privileged side effects never begin without a durable intent marker.
- Unknown privileged outcomes block future mutation until canonical
  reconciliation succeeds.
- Only typed `RECONCILE` may override unresolved helper journal state.
- Signed staging rejects untrusted keys, unsafe paths, symlinks, non-regular
  files, hash mismatches, duplicate versions, and package-supplied hooks.
- Staged files are re-verified after copy.
- Dispatchers use a fixed PATH and sanitized environment.
- Runtime data, credentials, keys, backups, packet captures, databases, VM images,
  and real Proxmox/network inventory are rejected by repository hygiene.
- Recovery has no network endpoint and credential changes revoke sessions.

### Validation note

- Public PR #28 final head `e3f6b983b189e6418cbd1711abf32e4a29d98107`
  and PR #29 final head `f56807a4bbad09bc3565f66de2b3b18aeb5c87b4`
  passed their full triggered workflow suites.
- The final public documentation baseline is
  `df99909a7b161b1a0bcc7149b9dfeaf6a2a51796`.
- Public automated evidence is not a substitute for the owner's real Proxmox,
  PPPoE, NIC, destructive storage, external scan, restore, or long-duration tests.
- A private CI pass must not be claimed unless Actions for the exact private head
  actually execute and report success.

### Known limitations

- Early alpha; not an unattended production firewall.
- The owner-created Proxmox VM still needs the dated private test report described
  in `docs/PROXMOX_AI_HANDOFF.md`.
- No stable signed ISO or owner-qualified recovery media.
- Real NIC, PPPoE, WireGuard, external scan, full-disk/inode/read-only/process-kill,
  abrupt power-loss, backup-restore, and long-duration evidence is still required.
- Same-kernel namespace throughput is not a physical or VirtIO performance claim.
