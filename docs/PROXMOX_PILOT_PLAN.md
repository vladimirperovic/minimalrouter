# Proxmox pilot plan — v0.1.5 Golden Appliance

This is the sanitized owner-pilot plan for validating a fresh Minimal Router
v0.1.5 VM before any real gateway cutover. It intentionally contains no VM IDs,
bridge names, MAC addresses, credentials, public addresses, private hostnames or
other operator inventory.

Read first:

- [`CURRENT_VALIDATION.md`](CURRENT_VALIDATION.md)
- [`ISO_INSTALLATION.md`](ISO_INSTALLATION.md)
- [`PROXMOX.md`](PROXMOX.md)
- [`GOLDEN-IMAGE.md`](GOLDEN-IMAGE.md)
- [`RECOVERY.md`](RECOVERY.md)
- [`PROXMOX_AI_HANDOFF.md`](PROXMOX_AI_HANDOFF.md)

## Objective

Install the exact signed v0.1.5 Golden ISO into a new blank, isolated Proxmox VM,
verify the appliance and recovery path, then run the remaining owner-pilot gates
without modifying the known-good router until the candidate has passed.

The first owner-Proxmox pilot already proved basic real PPPoE/Internet forwarding,
a real external WireGuard handshake/dashboard path, and pfSense fallback. This
plan extends that evidence with repeatability, signed-media installation,
recovery, DDNS, backup, storage/power and soak testing.

## Fresh v0.1.5 deployment

1. Verify the published `minimalrouter-0.1.5-amd64.iso` against its standalone
   `.iso.sha256` (and `SHA256SUMS` when the full release set is downloaded).
2. Create a new blank VM using the hardware profile in `PROXMOX.md`.
3. Attach exactly the intended LAN and WAN NICs/bridges and record the mapping
   privately outside the public repository.
4. Boot the Golden ISO. Let the flasher verify and copy the Golden image to the
   blank target disk.
5. After reboot, complete installed firstboot on the selected noVNC/tty1 or
   `ttyS0 @ 115200` console. Confirm WAN/LAN identity, optional PPPoE credentials,
   Dashboard administrator password and recovery/root password.
6. Verify firstboot completes once, `routerd`/`router-applyd` become ready,
   Dashboard HTTPS is reachable from the intended LAN, and the signed-update
   trust anchor exists.
7. Take a known-good Proxmox snapshot and encrypted application backup before
   real-WAN cutover testing.

Do not use a stock Alpine image plus ad-hoc package installation as the normal
v0.1.5 owner-pilot path. The archive install remains an advanced development/CI
path; the Golden ISO is the qualified AMD64/Proxmox user install path.

## Baseline checks

From the local console, record a redacted baseline:

```sh
cat /etc/alpine-release
uname -a
modprobe pppoe
ip -brief link
ip -brief address
ip route
findmnt /
df -h
df -i
rc-service router-applyd status
rc-service routerd status
router-update status
```

`modprobe pppoe` must succeed. Confirm that management is reachable only from the
intended LAN/WireGuard paths, persistent storage is writable, system time is
synchronized and no recovery state is ambiguous.

## Pilot sequence

Run in this order and stop at the first unexplained failure:

1. **Reboot/interface repeatability** — five guest reboots plus repeated
   hypervisor shutdown/start cycles; WAN/LAN identity and core readiness remain
   stable.
2. **LAN/DHCP/DNS/firewall** — verify DHCP/DNS/NAT and default-deny WAN; exercise
   one unconfirmed disruptive change and confirm rollback.
3. **Real PPPoE recovery** — repeat disconnect/reconnect, authentication, MTU,
   reboot and automatic reconnection; keep LAN management available throughout.
4. **WireGuard recovery** — verify an external peer reconnects after PPPoE
   reconnect/reboot without exposing management directly on WAN.
5. **MinimalRouter-managed DDNS** — configure the intended provider with a scoped
   credential, verify daemon health/update and later verify a real public-IP
   change propagates without a host-side workaround.
6. **Connected-device pause** — verify 15-minute, 1-hour and resume behavior on a
   disposable/test LAN client, including timed expiry and reboot persistence as
   designed.
7. **Signed update/rollback** — stage and activate an architecture-matching signed
   release payload, verify both core services run from the same selected slot,
   then perform explicit rollback.
8. **Encrypted backup restore** — restore into a fresh disposable VM and verify
   configuration, credentials and runtime without importing unsafe transient
   identity.
9. **Storage/power faults** — on disposable state only, exercise full disk,
   inode exhaustion, read-only filesystem and abrupt power interruption around
   durable operations.
10. **External scan** — verify the intended WAN exposure from an unrelated
    external IPv4/IPv6 host. Do not broaden firewall rules to make the scan pass.
11. **Sustained measurements** — record throughput, packet rate, latency/loss,
    CPU/RAM/disk/thermal behavior under realistic load.
12. **Soak** — run at least seven days and confirm bounded logs/history/WAL,
    stable services and no unexplained resource growth or reconnect behavior.

## Cutover and fallback

Before moving real traffic:

1. prove the known-good router can be restored independently;
2. keep local console/hypervisor access available;
3. take fresh snapshots/backups;
4. arm any independent host-side rollback/failsafe used by the owner environment;
5. switch only after the isolated candidate has passed the intended gate set;
6. on any unexplained failure, restore the known-good router first and investigate
   the candidate in isolation.

## Evidence and privacy

Record exact release/commit, generic environment, method, duration, result and
limitations. Public evidence must use synthetic labels and documentation address
ranges. Keep credentials, VM inventory, bridge mappings, MACs, hostnames, public
addresses, generated configs, backups, packet captures and raw private logs out
of the repository.

The authoritative current status is always
[`CURRENT_VALIDATION.md`](CURRENT_VALIDATION.md), not an old pilot plan or chat.
