# Local recovery console

`router-recovery` is deliberately available only from the appliance console as
root. There is no unauthenticated recovery HTTP endpoint and no WAN recovery
path.

Before changing canonical state, LAN recovery, snapshot restore, and factory
reset create a checksummed undo snapshot in the existing SQLite store. Restart
`router-applyd` and `routerd` after an offline configuration change so the
canonical state is reconciled transactionally.

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
printf '%s\n' 'a-new-password-of-at-least-15-characters' |
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

## Restore a snapshot

```sh
sudo router-recovery snapshots
sudo router-recovery restore-snapshot \
  --id snap-123456789 \
  --confirm RESTORE-SNAPSHOT
```

The snapshot checksum is verified before decoding. A second snapshot of the
pre-restore state is created so the restore itself can be undone.

## Factory reset

```sh
printf '%s\n' 'a-new-password-of-at-least-15-characters' |
  sudo router-recovery factory-reset \
    --wan enp1s0 \
    --lan enp2s0 \
    --password-stdin \
    --confirm FACTORY-RESET
```

Omit `--wan` and `--lan` to use the locally discovered recommendation. Factory
reset returns networking and services to secure defaults, clears TOTP and all
sessions, and preserves the previous configuration as a recovery snapshot.
