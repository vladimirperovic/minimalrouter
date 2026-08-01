# Proxmox VE deployment and existing VM pilot

Minimal Router OS targets a QEMU/KVM virtual machine on Proxmox VE. The owner has
an existing candidate VM; live node/VM IDs, bridge names, addresses and credentials
are intentionally not stored in Git.

Another operator must start with the private handoff:

- [`PROXMOX_AI_HANDOFF.md`](PROXMOX_AI_HANDOFF.md)

Also read [`CURRENT_VALIDATION.md`](CURRENT_VALIDATION.md),
[`PROXMOX_TEST_REPORT_2026-08-01.md`](PROXMOX_TEST_REPORT_2026-08-01.md),
[`DYNAMIC_DNS.md`](DYNAMIC_DNS.md), [`INSTALLATION.md`](INSTALLATION.md) and
[`RECOVERY.md`](RECOVERY.md).

## Current status

The 2026-08-01 owner-Proxmox pilot already demonstrated real PPPoE/Internet,
570/327 Mbps in the recorded throughput sample, 0% loss in a 600-packet test,
200/200 DNS queries, external phone WireGuard access to the dashboard and a
successful fallback to pfSense in approximately 93 seconds.

This is controlled-pilot evidence, not production readiness.

## Validated guest baseline

- Alpine Linux 3.22 x86_64;
- **Alpine `linux-lts` for the validated PPPoE path**;
- QEMU/KVM VM, not LXC;
- 1 vCPU to begin, CPU type `host` on a fixed homelab node;
- 1 GiB RAM;
- 8 GiB reliable virtual disk;
- two VirtIO NICs;
- QEMU Guest Agent installed/enabled;
- reliable host/guest time synchronization.

The initial real-WAN attempt used `linux-virt`, whose running kernel did not
provide the PPPoE module required by this appliance. `linux-lts` did, and PPPoE
then succeeded.

Before any real-WAN work:

```sh
uname -a
modprobe pppoe
```

If `modprobe pppoe` fails, stop. Boot/install `linux-lts` on the validated path
and repeat the check. Current installers enforce this capability check.

Observed RAM during the pilot was approximately 73 MB after a clean `linux-lts`
boot and 172 MB after the exercised workload.

## Existing VM rule

Do not recreate or rewire the VM before read-only discovery:

```sh
pvesh get /cluster/resources --type vm --output-format json-pretty
qm list
qm status <VMID>
qm config <VMID>
```

Keep raw output local. Proceed only when candidate identity and both NIC bridge
roles are unambiguous.

## Preserve rollback

Before a real gateway cutover:

1. prove pfSense/current router can be restored independently;
2. confirm no config/update transaction is pending;
3. export an encrypted application backup when possible;
4. record installed commit/update slot privately;
5. take a consistent known-good Proxmox snapshot;
6. arm an independent **host-side failsafe** before removing the active router.

The 2026-08-01 pilot showed the management connection can disappear during the
cutover while a Proxmox-host systemd timer still executes independently.

Use graceful shutdown for routine work:

```sh
qm shutdown <VMID> --timeout 60
qm status <VMID>
```

Reserve forced stop for intentional destructive/fallback actions.

## Guest baseline

Inside the guest:

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
router-update status
```

Confirm WAN/LAN mapping, management boundary, time sync, writable storage,
service health and absence of ambiguous recovery state.

## Dynamic DNS / No-IP

The deployment uses **No-IP**. MinimalRouter now supports No-IP natively through
Alpine `inadyn`. During the original successful WireGuard test, DDNS was run
manually on the Proxmox side. That was useful diagnosis, but it is not the
intended final configuration.

The next target-host test must configure No-IP through MinimalRouter itself and
prove:

- config check and bounded one-shot update;
- healthy OpenRC `inadyn` service;
- external DNS resolution to the current public IPv4;
- WireGuard via the No-IP hostname without host-side DDNS;
- later automatic propagation after a real public-IP change.

See [`DYNAMIC_DNS.md`](DYNAMIC_DNS.md).

## Required next tests

The first pilot already closed basic real PPPoE and basic external WireGuard
handshake/dashboard access. Extend rather than repeat the evidence:

1. five graceful guest reboots and multiple Proxmox stop/start cycles;
2. stable WAN/LAN identity and `modprobe pppoe` on every boot;
3. repeated PPPoE disconnect/reconnect/authentication/reboot recovery;
4. MinimalRouter-managed No-IP plus public-IP-change propagation;
5. WireGuard recovery after PPPoE reconnect/reboot;
6. backup restore into a fresh VM;
7. longer CPU/RAM/throughput/packet-rate/IRQ/latency/jitter/thermal testing;
8. external IPv4/IPv6 scans;
9. storage/read-only/process-crash/power-loss tests on disposable state;
10. at least seven days continuous operation.

## Evidence and production boundary

Record new sanitized evidence in
`docs/PROXMOX_TEST_REPORT_YYYY-MM-DD.md`. Never commit live identifiers or
credentials.

Do not remove pfSense solely because the first pilot succeeded. Repeated recovery,
No-IP appliance integration, restore, external scans, destructive fault tests,
soak, signed recovery media and independent review remain mandatory before an
unattended production recommendation.
