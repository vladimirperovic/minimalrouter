# Proxmox VM continuation guide for an AI operator

This is the private operational handoff for continuing the owner's existing Minimal Router VM on Proxmox.

It is intentionally stored only in `minimalrouterhome`. It must remain useful without storing real credentials, addresses, MAC addresses, VM IDs, bridge names, hostnames, tokens, backups, packet captures, or household inventory in Git.

## Current repository baseline

Shared application/runtime code is synchronized with:

`vladimirperovic/minimalrouter@df99909a7b161b1a0bcc7149b9dfeaf6a2a51796`

That public baseline includes:

- PR #28 — bounded storage and disk-pressure behavior;
- PR #29 — central appliance-health aggregation and Overview banner;
- the follow-up documentation synchronization;
- earlier privileged recovery, gateway quality, OpenRC supervision, SQLite durability, security, and update/recovery hardening.

`minimalrouterhome` is the production/private deployment repository. Shared engine code follows public `minimalrouter`; owner-specific runtime values stay outside Git.

## Private overlay boundary

Tracked repository files must never contain the live installation values below:

- PPPoE username or password;
- administrator password, password hash, session state, or TOTP secret;
- WireGuard private/preshared keys;
- Cloudflare/provider tokens;
- real public or household IP addresses;
- MAC addresses or household device inventory;
- Proxmox node name, VM ID, live bridge assignments, or raw `qm config` output;
- SQLite/WAL runtime state;
- `/var/lib/minimalrouter-applyd/` recovery metadata;
- generated PPPoE, dnsmasq, nftables, WireGuard, or service configuration;
- backup archives, VM disks, packet captures, or raw logs.

If a trusted local checkout needs temporary deployment material, use ignored paths:

```text
private/runtime/
private/secrets/
private/backups/
```

These directories are not a Git-based secret store and must not be required for building the shared engine.

## What the operator must discover locally

Do not ask the owner to paste secrets into chat if they already exist on the appliance or Proxmox host.

The exact live values are intentionally not in Git. Before modifying anything, discover locally and privately:

- Proxmox node containing the candidate VM;
- exact candidate VM ID/name;
- current vCPU, RAM, disk, guest-agent, boot order, and NIC model;
- which Proxmox bridge/NIC is intended WAN;
- which bridge/NIC is intended LAN;
- whether WAN currently uses a test/NAT path or the real ISP;
- guest WAN/LAN interface identity;
- installed Minimal Router commit/update slot;
- current LAN address/subnet;
- whether PPPoE, WireGuard, DDNS, Squid, Wi-Fi, or other optional services are enabled;
- whether a pending configuration transaction or update operation exists.

PPPoE credentials and other secrets should be read or entered only inside the trusted local environment when needed. Never echo them into evidence files.

## Non-negotiable safety rules

1. Inventory first. Never guess VM identity, WAN/LAN bridge roles, guest NIC order, or current update slot.
2. Do not recreate, delete, clone, rewire, or promote the VM until one candidate is identified unambiguously.
3. Keep the existing pfSense/router fallback independently available until the Minimal Router pilot completes.
4. Initial validation uses an isolated LAN and test/NAT WAN. Do not create two DHCP servers on the same production broadcast domain.
5. Keep Proxmox console access available before networking changes, update activation, rollback, or reboot tests.
6. Use graceful shutdown for normal lifecycle work. `qm stop` is reserved for an explicitly planned destructive power-loss test.
7. Preserve the previous A/B software slot until the new slot survives reboot, management login, DHCP/DNS/NAT checks, and an explicit rollback rehearsal.
8. Preserve helper recovery metadata. Do not delete `last-transaction.json`, `pending-confirmation.json`, `last-good.json`, or related recovery evidence merely to clear an error.
9. Unknown is not success. Accept only positively verified committed/rolled-back state or explicit `RecoveryRequired`.
10. Stop when VM identity, bridge purpose, management reachability, or rollback behavior becomes ambiguous.

## Phase 0 — read-only Proxmox discovery

Run read-only inventory first:

```sh
pvesh get /cluster/resources --type vm --output-format json-pretty
qm list
```

For plausible candidates:

```sh
qm status <VMID>
qm config <VMID>
```

Keep raw output local. Record only sanitized facts in Git documentation.

Proceed only after identifying exactly one Minimal Router candidate and understanding both NIC roles.

## Recommended VM baseline

Use the existing VM when reasonable; do not rebuild merely to match these recommendations.

Reference baseline:

- QEMU/KVM VM, not LXC;
- Alpine Linux 3.22 x86_64;
- CPU type `host` on the fixed homelab node;
- start with 1 vCPU;
- 1 GiB RAM for comfortable pilot headroom;
- 8 GiB reliable virtual disk;
- two VirtIO NICs unless hardware testing specifically requires passthrough;
- QEMU Guest Agent enabled;
- reliable Proxmox and guest time synchronization.

Any different live configuration should be documented, not silently changed.

## Phase 1 — preserve rollback

Before updating or testing:

1. Confirm pfSense/current router can be restored independently.
2. Confirm no configuration confirmation window is pending.
3. Confirm no firmware operation journal is pending.
4. Export an encrypted Minimal Router backup when available.
5. Record the installed commit and `router-update status` privately.
6. Take a known-good Proxmox snapshot from a consistent state.

Preferred host sequence:

```sh
qm shutdown <VMID> --timeout 60
qm status <VMID>
qm snapshot <VMID> pre-test-YYYYMMDD-HHMM --description "Known-good Minimal Router pilot state"
```

A Proxmox snapshot is not a substitute for an application-level encrypted backup.

## Phase 2 — safe guest baseline

Start only the identified candidate and open the console:

```sh
qm start <VMID>
qm status <VMID>
```

Inside the guest, run read-only checks:

```sh
cat /etc/alpine-release
uname -a
ip -brief link
ip -brief address
ip route
findmnt /
df -h
df -i
rc-service router-applyd status
rc-service routerd status
rc-service dnsmasq status 2>/dev/null || true
rc-update show | grep -E 'routerd|router-applyd|dnsmasq|chronyd'
router-update status
readlink -f /var/lib/minimalrouter-update/current 2>/dev/null || true
readlink -f /var/lib/minimalrouter-update/previous 2>/dev/null || true
```

Also confirm privately:

- WAN and LAN guest interfaces match intended Proxmox NICs;
- management is reachable only from the intended LAN/WireGuard path;
- system time is synchronized;
- persistent storage is writable;
- no core service is crash-looping;
- recovery state is not ambiguous.

## Phase 3 — build and update from the current private repository

Use an exact `minimalrouterhome` commit:

```sh
git clone <trusted-private-repository-url>
cd minimalrouterhome
git checkout main
git pull --ff-only
git rev-parse HEAD
pnpm --dir web install --frozen-lockfile
make dist-amd64
cd build
sha256sum -c minimalrouter-linux-amd64.tar.gz.sha256
```

Verify the checksum again after transfer.

For an already-installed appliance, do not overwrite live binaries manually. Use the supported signed A/B path when a trusted signed payload exists:

```sh
router-update status
router-update stage --dir <EXTRACTED_SIGNED_PAYLOAD> --manifest <SIGNED_MANIFEST>
router-update activate --version <VERSION> --confirm ACTIVATE-UPDATE
```

After verification, retain the previous slot. Explicit rollback:

```sh
router-update rollback --confirm ROLLBACK-UPDATE
```

A locally generated development signing key is not equivalent to the owner's production trust anchor.

## PR #28 — storage-pressure behavior to verify on Proxmox

The synchronized engine treats appliance storage as finite.

Expected states:

- below 80% used: `Normal`;
- 80% to below 90%: `Warning`;
- 90% or above: `Critical`.

At `Critical` pressure:

- existing routing/firewall/PPPoE/DHCP/DNS state must continue;
- durable management mutations must return HTTP 507 rather than claiming false success;
- recovery mutations that require persistence are also blocked;
- read-only status and backup export remain available where no new durable mutation is required;
- gateway probing continues, but nonessential gateway-history writes are shed.

Bounded state includes:

- latest 100 config revisions;
- latest 20 snapshots;
- latest 5,000 audit events;
- gateway samples bounded by seven days / 41,000 rows;
- gateway reconnect events bounded by seven days / 2,048 rows;
- routerd/router-applyd logs rotated at 1 MiB with four compressed rotations;
- periodic passive SQLite WAL checkpointing.

Do not perform destructive full-disk/inode/read-only tests on the only candidate VM. Use a disposable clone or dedicated test disk after backup/snapshot/rollback is proven.

Relevant docs:

- `STORAGE_PRESSURE.md`
- `STORAGE_PRESSURE_TEST_PLAN.md`

## PR #29 — central appliance health to verify on Proxmox

Authenticated endpoint:

```text
GET /api/v1/health
```

Aggregate states:

- `Healthy`;
- `Warning`;
- `Degraded`;
- `Recovery required`;
- `Unknown`.

The health model observes:

- recovery/transaction state;
- storage pressure;
- memory pressure;
- conntrack pressure;
- time synchronization;
- WAN/PPPoE and gateway quality;
- routerd/router-applyd supervision and apply socket;
- dnsmasq DNS/DHCP state;
- PPPoE service state when configured;
- WireGuard interface state when configured;
- signed-update trust/pending update state;
- age of the last successful encrypted backup export visible in retained audit state.

Rules:

- `Recovery required` has highest severity;
- missing evidence remains `Unknown`, never invented as Healthy;
- health collection is read-only;
- health collection does not restart services, reconnect PPPoE, modify firewall state, or expose secrets.

The Overview dashboard health banner refreshes independently every 15 seconds.

Relevant doc: `APPLIANCE_HEALTH.md`.

## Phase 4 — required functional test order

Run in this order and stop at the first unexplained failure.

### A. Boot and reconciliation

- five graceful guest reboot cycles;
- at least two graceful Proxmox shutdown/start cycles;
- WAN/LAN roles stable after every boot;
- `routerd`, `router-applyd`, dnsmasq, nftables, chronyd, and configured services healthy;
- no unexpected `RecoveryRequired` state;
- dashboard and `/api/v1/health` available from the intended management path.

### B. LAN and management boundary

From an isolated client:

- receive DHCP lease;
- resolve DNS through the router;
- authenticate through HTTPS;
- verify dashboard/API are not reachable from WAN;
- verify unsolicited WAN-to-LAN traffic is denied;
- verify an unconfirmed disruptive management/LAN change rolls back;
- verify local recovery remains available.

### C. NAT and forwarding

With test/NAT WAN:

- outbound IPv4 NAT;
- practical TCP and UDP flows;
- parallel connection load;
- management responsiveness under load;
- no unexplained loss/retransmission spikes.

### D. Storage and health

- verify Normal/Warning/Critical thresholds against a disposable test filesystem;
- verify HTTP 507 for durable writes under Critical pressure;
- verify routing and existing services remain active;
- verify gateway history stops growing while live health continues;
- verify logrotate policy;
- verify WAL/history/snapshot growth remains bounded;
- verify central health severity changes correctly for injected non-destructive conditions.

### E. Update and recovery

- signed A/B activation;
- service restart/reboot;
- health verification;
- explicit rollback;
- backup restore into a fresh VM;
- service crash/restart tests;
- destructive disk/read-only/power-loss tests only on a disposable target.

### F. Real PPPoE maintenance window

Only after A–E pass and pfSense rollback is proven:

- enter PPPoE credentials locally;
- establish the real ISP session;
- record negotiated MTU, route, DNS, reconnect time, and CPU without logging secrets;
- disconnect/reconnect repeatedly;
- reboot and verify automatic reconnection;
- verify gateway quality and central health behavior;
- immediately restore the known-good router if authentication, MTU, routing, or management recovery is unclear.

### G. External and soak validation

- external IPv4 scan;
- external IPv6 scan or explicit documented fail-closed behavior;
- WireGuard from an unrelated external/mobile network;
- at least seven continuous days of operation;
- record CPU, RAM, storage, WAL/log/history growth, gateway quality, reconnects, and service restarts;
- repeat backup/restore and update/rollback after the soak.

## Evidence format

Create a new private dated report:

```text
docs/PROXMOX_TEST_REPORT_YYYY-MM-DD.md
```

Include sanitized evidence for:

- exact repository commit;
- Proxmox version/kernel;
- guest Alpine/kernel;
- vCPU/RAM/disk/NIC model;
- topology using synthetic labels only;
- commands used;
- boot/reboot results;
- DHCP/DNS/NAT/firewall results;
- appliance-health results;
- storage-pressure results;
- throughput/packet-rate/latency/jitter/loss results;
- update/rollback and backup/restore results;
- PPPoE/WireGuard results when tested;
- failures and recovery steps;
- remaining limitations;
- final recommendation: isolated pilot, guarded production pilot, or reject.

Never commit the raw Proxmox inventory or secrets as evidence.

## Stop conditions

Stop and restore the known-good router when any of these occurs:

- WAN/LAN identity changes unexpectedly;
- management becomes unreachable and console recovery is unclear;
- a second DHCP server appears on the production LAN;
- default-deny WAN behavior fails;
- durable mutation appears successful while persistence failed;
- rollback cannot positively restore the previous known-good state;
- `RecoveryRequired` cannot be explained and reconciled;
- storage pressure causes forwarding/service teardown contrary to policy;
- persistent state becomes corrupt;
- unexplained packet loss, CPU saturation, memory growth, disk growth, or repeated service restarts occur;
- the operator cannot prove which bridge/NIC/VM is being modified.

## Completion criterion

The Proxmox handoff is complete only when another operator can reproduce the sanitized inventory, boot, health, storage-pressure, DHCP/DNS/NAT/firewall, update/rollback, backup/restore, PPPoE, WireGuard, external scan, and soak results from a private dated report without relying on undocumented chat history.
