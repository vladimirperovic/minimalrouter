# Current validation status — 2026-07-31

This document is the private repository source of truth for imported automated
evidence, private-tree parity, and the remaining Proxmox/hardware release gates.
Dated hardware reports remain historical evidence.

## Validated public baseline

The recovery-hardening source baseline is:

```text
vladimirperovic/minimalrouter@1eda8073b6d005dfa5bdb5673c227a991442cdb6
```

Its final pull-request head completed successfully on 2026-07-31 for:

- standard CI, including Go race tests, formatting, `vet`, `govulncheck`,
  dashboard lint/unit/build/Playwright E2E, repository hygiene, public-root
  validation, and clean Alpine install/setup/signed update/rollback;
- Deep validation, including ARM64 QEMU, WAN-router-LAN namespace networking,
  actionlint, shellcheck, high-confidence `gosec`, binary inspection,
  interrupted-update stress, fuzzing, benchmarks, and coverage;
- CodeQL;
- secret scanning; and
- Performance.

This evidence belongs to the exact public baseline above. It is not a claim that
private GitHub Actions ran successfully.

## Private-tree parity

The private sync branch imports the public baseline's reviewed recovery files as
bit-identical Git blobs, including:

- `cmd/router-applyd/main.go`, startup reconciliation, confirmation, outcome, and
  persistence logic;
- `internal/apply/ipc.go` and `internal/apply/statemachine.go`;
- deterministic durable-intent, lost-response, corrupt-journal,
  `RecoveryRequired`, rollback, boot-reconcile, and two-phase confirmation tests;
- the OpenRC startup-readiness gate;
- the clean-Alpine install/update/rollback and destructive power-loss smoke test;
- architecture, security, recovery, testing, and failure-scenario documentation.

The imported implementation provides:

- durable privileged intent before side effects;
- validated, persisted, idempotent completed outcomes;
- exact-ID retry for ambiguous transport loss;
- fail-closed handling of incomplete, unreadable, corrupt, or contradictory
  privileged metadata and RPC outcomes;
- mutation blocking while `RecoveryRequired` is active;
- allowlisted canonical `RECONCILE` as the only override for unresolved helper
  journal state;
- startup restoration of SQLite canonical runtime before helper readiness;
- WireGuard-only management confirmation boundaries;
- disruptive confirmation ordered as runtime verification, SQLite canonical
  commit, helper `last-good` commit, and pending cleanup;
- fresh final-helper-commit IDs for later explicit retry after a recorded storage
  failure;
- rollback reported only when restoration is successful and verified.

Private-only `docs/PROXMOX_AI_HANDOFF.md`, private release workflow, and household
runtime/configuration material were preserved rather than replaced by public
files.

## Private GitHub Actions limitation

Private pull-request workflows currently terminate before the first job step and
return no executable job log. This has occurred for standard CI, Deep validation,
Performance, and the attempted one-time sync workflow.

Therefore:

- those private workflow results are **not** test failures in the imported code;
- they are also **not** evidence of a successful private build;
- no private CI pass is claimed;
- confidence in the imported implementation comes from exact Git-blob parity with
  the fully green public baseline;
- private-only documentation changes do not have public automated evidence and
  require normal review;
- target-Proxmox validation remains mandatory before deployment promotion.

The temporary one-time sync workflow was removed and is not part of the candidate.

## Automated behavior represented by the imported tests

The exact imported tests cover:

- ambiguous apply and confirmation response loss without duplicate side effects;
- durable intent persistence before privileged mutation;
- incomplete intent after interruption;
- result-journal write/read failure;
- corrupt or structurally invalid transaction, pending, and last-good state;
- contradictory helper outcomes;
- `RecoveryRequired` mutation blocking and canonical reconciliation;
- WireGuard-only management key, port, address, peer, and route changes;
- failed timeout rollback with fresh retry IDs;
- SQLite commit failure with verified versus unverified restoration;
- runtime confirmation before SQLite commit;
- SQLite commit before helper `last-good` advancement;
- failed final helper acknowledgement retried without repeating runtime
  confirmation or canonical commit;
- boot restoration of confirmed state and cleanup of stale unconfirmed state;
- clean Alpine install, first-run setup, signed A/B update activation, rollback,
  and simulated loss of volatile firewall/LAN/WireGuard runtime.

These are deterministic and virtualized tests. They do not replace destructive
persistent-storage, hypervisor, physical-NIC, ISP, or external-network testing.

## Release and signing state

No release tag was created during this sync. The private tagged-release workflow
remains private and unchanged. A real tag still requires the configured offline
release-signing secret and the repository's release gates.

## Recorded control-plane baseline

On the public GitHub-hosted AMD EPYC runner, the previously recorded range was
approximately:

| Operation | Result |
|---|---:|
| Setup-status API | 4.8–5.4 microseconds |
| Normal update-state read | about 26 microseconds |
| Update-state read while recovering a journal | 44.6–45.1 microseconds |
| Rejected protected request with durable audit write | 4.27–4.40 milliseconds |

These are control-plane measurements. Packet forwarding remains in the Linux
kernel and must be measured separately on the target Proxmox host and NIC
configuration.

## What remains unproven

The following remain private Proxmox or hardware gates:

1. Stable WAN/LAN identity across repeated Proxmox and guest reboots.
2. Real ISP PPPoE establishment, disconnect, reconnect, MTU, authentication, and
   LAN recovery during WAN loss.
3. Real VirtIO or passed-through NIC throughput, packet rate, CPU/IRQ load,
   latency, jitter, and packet loss.
4. Real WireGuard throughput, external reconnection after reboot, and guarded
   WireGuard-only management changes.
5. External IPv4 and IPv6 scanning from an unrelated host.
6. Backup export and restore into a fresh VM.
7. Full-disk, inode-exhaustion, read-only-filesystem, service-crash,
   helper-process-crash, corrupt-state, and persistent-storage recovery.
8. Controlled interruption after durable intent, runtime apply, result journal,
   runtime confirmation, SQLite commit, helper `last-good`, pending cleanup, and
   canonical reconcile.
9. Abrupt guest/host power-loss tests on a disposable clone.
10. At least seven days of sustained operation with bounded memory, logs,
    journals, snapshots, and disk growth.
11. Owner-signed installation/recovery media on the target platform.
12. An independent focused security review.

## Deployment recommendation

The candidate is appropriate only for a controlled Proxmox pilot with:

- local console access;
- isolated LAN and test/NAT WAN initially;
- a known-good application backup and Proxmox snapshot;
- pfSense or another established router ready for immediate rollback;
- execution of [`PROXMOX_AI_HANDOFF.md`](PROXMOX_AI_HANDOFF.md) and a private dated
  test report.

It is not yet documented as an unattended, drop-in pfSense replacement.

See also:

- [`PROXMOX.md`](PROXMOX.md)
- [`PROXMOX_AI_HANDOFF.md`](PROXMOX_AI_HANDOFF.md)
- [`TESTING.md`](TESTING.md)
- [`FAILURE_SCENARIOS.md`](FAILURE_SCENARIOS.md)
- [`RESOURCE_AND_HARDWARE_TEST.md`](RESOURCE_AND_HARDWARE_TEST.md)
- [`SECURITY_REVIEW.md`](SECURITY_REVIEW.md)
- [`RECOVERY.md`](RECOVERY.md)
