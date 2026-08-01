# Proxmox VM continuation guide for an AI operator

This is the private operational handoff for continuing the owner's existing
Minimal Router VM on Proxmox. It intentionally contains no live credentials,
addresses, MAC addresses, VM IDs, bridge names, private hostnames, tokens,
backups, packet captures or household inventory.

Read first:

- [`CURRENT_VALIDATION.md`](CURRENT_VALIDATION.md)
- [`PROXMOX_TEST_REPORT_2026-08-01.md`](PROXMOX_TEST_REPORT_2026-08-01.md)
- [`DYNAMIC_DNS.md`](DYNAMIC_DNS.md)
- [`PROXMOX.md`](PROXMOX.md)
- [`RECOVERY.md`](RECOVERY.md)

## Current known-good evidence

The 2026-08-01 owner-Proxmox pilot already established:

- real PPPoE and Internet forwarding through Minimal Router;
- `linux-lts` as the validated Alpine guest kernel for the required PPPoE
  module after the tested `linux-virt` kernel lacked that support;
- approximately 73 MB RAM after a clean `linux-lts` boot and 172 MB after the
  exercised workload;
- 570/327 Mbps in the recorded throughput sample;
- 0% packet loss in the recorded 600-packet test;
- 200/200 DNS queries;
- dashboard 30/30 during the recorded CPU-load sample;
- successful external phone WireGuard handshake and dashboard access;
- successful fallback to pfSense in approximately 93 seconds.

Do not describe basic WireGuard handshake as still untested. Do not describe real
PPPoE as still entirely untested. The remaining work is repeatability, recovery,
No-IP integration proof, longer soak/fault/security validation and restore.

## Current DDNS behavior

The deployment uses **No-IP**. MinimalRouter now supports it natively through
Alpine `inadyn` with provider `no-ip.com`. New configurations default to No-IP;
Cloudflare remains available for backward compatibility.

During the original successful WireGuard test, DDNS was provisioned manually on
the Proxmox side. That proved the external hostname/endpoint concept but is no
longer the intended normal operating path.

The next target test must prove that **MinimalRouter-managed No-IP** works without
that host-side workaround:

1. configure No-IP through the Dynamic DNS dashboard;
2. verify `inadyn --check-config`;
3. verify the bounded one-shot update succeeds;
4. verify OpenRC `inadyn` stays healthy;
5. verify external DNS resolves to the expected current public IPv4;
6. verify WireGuard from an external/mobile network using the No-IP hostname;
7. later verify an actual public-IP change is propagated automatically.

Prefer a scoped No-IP DDNS Key. Never paste its username/password into chat or
commit it to Git.

## Non-negotiable PPPoE kernel check

Before any real-WAN test, inside the guest run:

```sh
cat /etc/alpine-release
uname -a
modprobe pppoe
```

`modprobe pppoe` must succeed. The validated Proxmox path uses Alpine
**`linux-lts`**. If the running kernel does not provide the PPPoE module, stop;
do not attempt to work around it in application code or mark the router ready.
Boot/install `linux-lts`, reboot into it and repeat the check.

The current installers also fail closed on this capability check.

## Private overlay boundary

Tracked repository files must never contain:

- PPPoE username/password;
- administrator password/hash, session or TOTP secret;
- WireGuard private/preshared keys;
- No-IP DDNS Keys/passwords or Cloudflare tokens;
- real public or household IP addresses/hostnames;
- MAC addresses or household device inventory;
- Proxmox node name, VM ID, live bridge assignments or raw `qm config` output;
- SQLite/WAL runtime state;
- `/var/lib/minimalrouter-applyd/` recovery metadata;
- generated PPPoE/dnsmasq/nftables/WireGuard/inadyn configuration;
- backup archives, VM disks, packet captures or raw logs.

Temporary trusted deployment material belongs only in ignored local paths:

```text
private/runtime/
private/secrets/
private/backups/
```

## Phase 0 — read-only discovery

Never assume VM identity or bridge roles from prior chat. Discover locally:

```sh
pvesh get /cluster/resources --type vm --output-format json-pretty
qm list
qm status <VMID>
qm config <VMID>
```

Keep raw output local. Proceed only after identifying exactly one candidate and
understanding WAN/LAN roles.

## Phase 1 — preserve rollback

Before modifying networking, updating or testing:

1. prove pfSense/current router can be restored independently;
2. confirm no configuration-confirmation window is pending;
3. confirm no firmware/update operation is pending;
4. export an encrypted Minimal Router backup when available;
5. record installed commit/update slot privately;
6. take a known-good Proxmox snapshot from consistent state;
7. for a real gateway cutover, arm an **independent Proxmox-host failsafe** before
   removing the known-good router.

The 2026-08-01 test showed why this matters: the management/Internet connection
can disappear during cutover while a host-local failsafe continues to run.

Use graceful lifecycle commands for ordinary work:

```sh
qm shutdown <VMID> --timeout 60
qm status <VMID>
```

Reserve `qm stop` for an intentionally planned abrupt-power/fallback action.

## Phase 2 — guest baseline

Inside the candidate:

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
rc-service inadyn status 2>/dev/null || true
rc-update show | grep -E 'routerd|router-applyd|inadyn|chronyd'
router-update status
```

Confirm privately:

- WAN/LAN guest interfaces map to intended Proxmox NICs;
- management is reachable only from intended LAN/WireGuard paths;
- time is synchronized;
- persistent storage is writable;
- no core service is crash-looping;
- recovery state is not ambiguous.

## Phase 3 — build/update

Use an exact private repository commit and verify archive checksums. Do not
overwrite live binaries manually. Prefer the supported signed A/B update path
when a trusted signed payload exists.

```sh
router-update status
router-update stage --dir <EXTRACTED_SIGNED_PAYLOAD> --manifest <SIGNED_MANIFEST>
router-update activate --version <VERSION> --confirm ACTIVATE-UPDATE
```

Keep the previous slot until the new version survives reboot, management,
DHCP/DNS/NAT, PPPoE reconnect and rollback rehearsal.

## Phase 4 — next test order

Run in this order and stop at the first unexplained failure:

### A. Reboot/interface repeatability

- five graceful guest reboots;
- at least two graceful Proxmox shutdown/start cycles;
- `modprobe pppoe` succeeds each boot;
- WAN/LAN identity remains stable;
- core services and health remain valid.

### B. Base LAN/WAN behavior

- DHCP/DNS/NAT;
- default-deny WAN;
- dashboard only on intended management paths;
- unconfirmed disruptive change rolls back;
- local recovery remains available.

### C. Real PPPoE recovery

Real PPPoE basic operation already passed once. Extend the evidence:

- repeated disconnect/reconnect;
- authentication and MTU behavior;
- reboot and automatic reconnection;
- gateway quality/health transitions;
- no management dead-end.

### D. MinimalRouter-managed No-IP

This is the highest-value new functional test:

- configure No-IP in dashboard using a scoped credential;
- verify one-shot update and daemon health;
- verify external resolution;
- verify WireGuard via the hostname;
- remove any temporary host-side updater;
- later prove public-IP-change propagation.

### E. WireGuard recovery

Basic external handshake/dashboard access already passed. Test recovery after
PPPoE disconnect/reconnect and reboot plus any wider routing requirements.

### F. Recovery, storage and soak

- signed update/rollback;
- encrypted backup restore into fresh VM;
- storage-pressure thresholds and HTTP 507 behavior;
- full disk/inode/read-only/power-loss only on disposable state;
- external IPv4/IPv6 scan;
- at least seven continuous days with stable memory, logs/WAL/history and
  services.

## Stop conditions

Stop and restore the known-good router if:

- WAN/LAN identity becomes ambiguous;
- `modprobe pppoe` fails on the intended kernel;
- management becomes unreachable and console recovery is unclear;
- a second DHCP server reaches the production LAN;
- default-deny WAN fails;
- rollback cannot positively restore known-good state;
- persistent recovery state is contradictory/corrupt;
- unexplained packet loss, memory/disk growth or repeated crashes occur;
- the operator cannot prove which VM/NIC/bridge is being modified.

## Evidence format

Record new private evidence in a dated report under:

```text
docs/PROXMOX_TEST_REPORT_YYYY-MM-DD.md
```

Use sanitized labels only. Never commit raw Proxmox inventory or secrets.

The handoff is complete when another operator can reproduce the sanitized kernel,
PPPoE, No-IP, WireGuard, boot/recovery, backup/restore, external-scan and soak
results without relying on undocumented chat history.
