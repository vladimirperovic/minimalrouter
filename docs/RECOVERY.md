# Local recovery console

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
and revokes every existing session.

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