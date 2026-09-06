# Configuration admission and offline migration

## Live changes require a valid rollback baseline

[`ValidateLiveCandidate`](../internal/config/validation_policy.go) requires the
complete candidate **and the existing rollback configuration** to pass structural
and scenario-safety validation. Preview, apply, and the privileged helper share
this policy. A valid replacement alone cannot make rollback to an invalid legacy
configuration safe. First-run setup, without a previous configuration, remains a
separate case. Delta validation does not waive faults in unchanged fields.

[`MigrateLegacyFields`](../internal/config/legacy_migration.go) supplies only the
documented deterministic default: a missing enabled ExtraLAN `router_address`
becomes the first usable IPv4 address of its subnet. It leaves unusable subnets
for validation and does not guess secrets or disable security controls. Other
legacy faults require an explicitly corrected configuration.

Normal `router-recovery restore-snapshot`, `restore-last-good`, `set-lan`,
`set-wan`, and `factory-reset` read the current canonical configuration through
`GetLatestConfig`, which rejects invalid configurations. They cannot repair that
case. `restore-last-good` selects a SQLite snapshot, not the helper's
`last-good.json`. `router-setup apply --offline` is first-run provisioning and
refuses an already configured administrator; it is not a migration command.

## Supported offline replacement

Use `router-recovery migrate-config` from a **local root console on Linux** with
the migration-capable versions of both daemons installed. Prepare a complete,
unredacted `SystemConfig` JSON file from a trusted configuration backup, correcting
the reported validation faults. A provisioning JSON document or redacted API
response is not a replacement configuration. Protect this file as a secret; the
command does not export or infer missing credentials. It rejects unknown fields,
multiple JSON documents, oversized input, and invalid structural/scenario state.
Every secret field also rejects `[REDACTED]`, including disabled features; these
placeholders are neither imported as passwords nor restored from legacy state.

For the installed OpenRC appliance, with the prepared file at
`/root/router-config.json`:

```sh
chmod 600 /root/router-config.json
rc-service routerd stop
rc-service router-applyd stop
router-recovery migrate-config --file /root/router-config.json --confirm MIGRATE-CONFIG
```

The default database directory is `/var/lib/minimalrouter`. If the services use
`MINIMALROUTER_DATA_DIR`, set the same value for recovery. The migration takes an
exclusive lock, checks for legacy daemon processes and started OpenRC services,
and refuses to run while either daemon is active. Both new daemons hold the
matching shared lock for their lifetime.

All SQLite access runs in a separate worker permanently using the configured
`routerd` UID/GID, with supplementary groups and capabilities cleared. The root
coordinator sends data through inherited anonymous pipes; it never opens SQLite
as root. A database-path substitution therefore cannot borrow root filesystem
permissions. The worker receives the private candidate through its pipe and
cannot read the root-only input file or migration backups. This requires an
existing, non-root `routerd` account and database access for that account.

Before replacing configuration, it durably records a root-owned startup fence
at `/var/lib/minimalrouter-migration/pending.json`. A private `attempt-*`
directory contains the exact original SQLite configuration bytes and revision,
the original helper last-good, pending-confirmation and transaction bytes
(including absence), and the validated replacement. Directories containing
backups are mode `0700`; backup files are `0600`. They contain secrets and must
remain private, including when retained after success.

The replacement receives the next SQLite canonical revision and a fresh
timestamp; input revision/timestamp values are not used. One SQLite transaction
compares the original bytes/revision, inserts the replacement, and revokes
sessions. Recovery then writes the identical helper baseline, removes the
backed-up stale helper pending/transaction records, and verifies both persistent
copies. It synchronizes state before removing the startup fence. Administrator
password and TOTP settings are retained. No networking is activated by migration.
Observed concurrent canonical changes fail the compare-and-swap. This protocol
does not exclude arbitrary hostile processes already running under the database
owner's UID or make that owner's database tamper-proof.

**If interrupted or unsuccessful, keep both daemons stopped and rerun the exact
command with the unchanged input file and data directory.** The journal permits
forward completion after a committed SQLite write without advancing the revision
twice. While its fence remains, both daemons refuse startup before runtime work.
Do not delete the fence, helper journals, or edit SQLite to bypass admission. A
missing/corrupt backup or conflicting canonical state requires local diagnosis;
recovery does not silently restore the invalid original configuration.

After the migration command reports success:

```sh
rc-service router-applyd start
rc-service routerd start
```

Check both service results, local LAN/firewall connectivity, and Dashboard access
before leaving the console. Helper startup still performs runtime preflight and
verification; valid JSON cannot prove interface availability or working hardware.
The retained raw backup is diagnostic/recovery material, not an approved live
rollback target. A known-good complete appliance backup remains an external
recovery option if coordinated state cannot be repaired. See
[Recovery](RECOVERY.md) for ordinary snapshot and console operations.

## TOTP replay protection is process-local

[`TOTPReplayKey`](../internal/auth/totp.go) hashes the same canonical secret/code
inputs used by validation, so accepted whitespace and secret-encoding aliases
cannot produce distinct replay entries. The API consumes entries under a mutex
and retains them for two minutes in each server's memory.

This replay history is not persisted or shared between server instances. A
`routerd` restart loses it: an already consumed code can be accepted again while
it still satisfies the normal TOTP window (30-second steps, previous/current/next
step tolerance) and the endpoint's other authentication checks. Persistent
session revocation does not provide persistent TOTP replay protection. Durable
consumed-step tracking remains a separate security improvement.
