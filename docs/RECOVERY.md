# Local recovery console

## Dashboard backups

The Recovery page separates portable backups, validated restore, pfSense
migration, redacted diagnostics and local configuration snapshots.

New `.mrbak` backups use the **current dashboard password** (minimum 12
characters). Enter it once to authorize and encrypt the export; there is no
separate backup password. Encryption remains AES-256-GCM with a random salt,
nonce and Argon2id (64 MiB, three iterations, one thread). No password or derived
key is persisted. Changing the dashboard password does not re-encrypt old files:
they still require the password used when created.

For an older file, select **This backup uses an older or separate password**.
The current dashboard password authorizes the operation, and the optional older
password decrypts the file. Legacy v1 files with separate passphrases remain
readable. New v2 files require v0.2.0 or later.

Restore first validates the file and previews the configuration. Applying keeps
the existing connectivity confirmation and rollback behavior. A v2 file also
contains the network DNS category policy when the subsystem was available at
export: after confirming the restored network, press **Restore DNS protection**
and verify completion on DNS Filter. Public lists are downloaded again as needed;
query history and downloaded catalogs are not in the backup. This is an explicit
second phase, not an atomic restore of both independent stores.

Local configuration snapshots remain useful undo points, but live on the same
appliance and do not include the separate category policy. Download a portable
backup to another device for disk-failure recovery. Diagnostics are redacted
reports and cannot restore settings.

## Console recovery

`router-recovery` is deliberately available only from the appliance console as
root. There is no unauthenticated recovery HTTP endpoint and no WAN recovery
path.

Before changing canonical state, LAN recovery, snapshot restore, and factory
reset create a checksummed undo snapshot in the existing SQLite store. Restart
`router-applyd` and `routerd` after an offline configuration change so the
canonical state is reconciled transactionally.

## `RecoveryRequired`

`RecoveryRequired` means the router cannot prove that a privileged change was
fully committed or positively rolled back. Examples include:

- a lost or contradictory privileged RPC outcome;
- an incomplete pre-operation intent after process or power interruption;
- an unreadable or corrupt transaction, pending-confirmation, or last-good file;
- failed rollback verification;
- SQLite commit failure with unverified restoration;
- SQLite commit success followed by failed helper `last-good` acknowledgement.

While this state is active, normal configuration mutations are blocked. Do not
manually delete helper journals, pending state, or `last-good.json` to make the
error disappear. Removing evidence can cause a previously executed side effect
to be repeated or can make an older helper file appear newer than SQLite.

The normal recovery order is:

1. Keep local Proxmox/appliance console access open.
2. Record the exact error, current commit, service status, and storage health.
3. Correct the underlying storage or service failure without changing canonical
   configuration by hand.
4. Restart `router-applyd`, then restart `routerd`.
5. `routerd` loads the SQLite canonical configuration and issues the allowlisted
   `RECONCILE` operation before normal management readiness.
6. Verify LAN management, DHCP, DNS, firewall, WAN, and WireGuard as applicable.
7. If reconciliation still fails, use the local recovery commands below or
   restore the known-good Proxmox snapshot/router.

`RECONCILE` is the only operation allowed to supersede unresolved helper journal
state. It applies only the configuration generated from SQLite canonical state;
it is not a general bypass for arbitrary privileged requests.

## Commit-confirm recovery boundaries

Disruptive confirmation is intentionally split into three durable phases:

1. candidate runtime is finalized and verified;
2. the exact candidate revision is committed to SQLite;
3. the helper verifies runtime again, records the candidate as `last-good`, and
   clears pending state.

If failure occurs before phase 2, the previous SQLite configuration remains
canonical and timeout rollback may restore it. If failure occurs after phase 2,
the candidate is canonical and must not be rolled back merely because the helper
acknowledgement was lost; restart/reconciliation must repair helper recovery
metadata from SQLite.

A retry after an explicit final helper storage failure uses a fresh transaction
ID. Transport retries inside one attempt retain the same ID so the helper can
return its idempotent recorded result.

## Discover network interfaces

```sh
sudo router-recovery interfaces
```

The recommendation prefers an existing default route for WAN and a distinct,
carrier-present physical interface for LAN. Virtual bridges, containers,
tunnels, PPP, and loopback interfaces are excluded. Always confirm cabling and
interface names locally before applying a disruptive change.

## Reset password and TOTP

Read the password from standard input so it is not recorded in shell history:

```sh
printf '%s\n' 'a-new-password-of-at-least-12-characters' |
  sudo router-recovery reset-auth --password-stdin --disable-totp
```

The operation stores a new Argon2id hash, optionally removes the TOTP secret,
and revokes every existing session. A running routerd uses the new credential
immediately for login and for every re-authentication prompt; no restart is
required.

## Recover LAN access

```sh
sudo router-recovery set-lan --interface enp2s0 --cidr 192.168.10.1/24
sudo rc-service router-applyd restart
sudo rc-service routerd restart
```

Recovery LAN prefixes are limited to `/16` through `/24`, and the DHCP range is
recalculated inside the new subnet.

After restart, wait for canonical reconciliation and verify that both the old and
new management paths behave as expected before closing the console.

## Restore a snapshot

```sh
sudo router-recovery snapshots
sudo router-recovery restore-snapshot \
  --id snap-123456789 \
  --confirm RESTORE-SNAPSHOT
```

The snapshot checksum is verified before decoding. A second snapshot of the
pre-restore state is created so the restore itself can be undone. Restart both
services afterward so runtime is regenerated and verified from the restored
SQLite configuration.

## Factory reset

```sh
printf '%s\n' 'a-new-password-of-at-least-12-characters' |
  sudo router-recovery factory-reset \
    --wan enp1s0 \
    --lan enp2s0 \
    --password-stdin \
    --confirm FACTORY-RESET
```

Omit `--wan` and `--lan` to use the locally discovered recommendation. Factory
reset returns networking and services to secure defaults, clears TOTP and all
sessions, and preserves the previous configuration as a recovery snapshot.

Factory reset is the last application-level option. It does not replace the
independent pfSense/known-good-router rollback path, a Proxmox snapshot, or
signed recovery media.
