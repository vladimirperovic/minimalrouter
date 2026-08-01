# Proxmox VE lab deployment

Minimal Router OS targets a QEMU/KVM virtual machine on Proxmox VE. An
unprivileged LXC container is not a supported router boundary because it shares
the host kernel and cannot provide the same nftables, interface and device
isolation.

Read [`CURRENT_VALIDATION.md`](CURRENT_VALIDATION.md),
[`INSTALLATION.md`](INSTALLATION.md),
[`PROXMOX_TEST_REPORT_2026-08-01.md`](PROXMOX_TEST_REPORT_2026-08-01.md),
[`DYNAMIC_DNS.md`](DYNAMIC_DNS.md), [`TESTING.md`](TESTING.md) and
[`RECOVERY.md`](RECOVERY.md) before using this guide.

## Current status

There is no signed stable ISO. A controlled owner-Proxmox pilot on 2026-08-01
successfully demonstrated real PPPoE/Internet forwarding, the recorded
throughput/load sample, external WireGuard phone access to the dashboard and
operational fallback to pfSense. This is meaningful target-host evidence, not a
production-readiness claim.

Use this only as a controlled pilot with:

- Proxmox console access;
- an isolated LAN during initial validation;
- a test/NAT WAN before the real ISP cutover;
- the existing router ready for immediate rollback.

## Recommended VM baseline

- Alpine Linux 3.22 x86_64;
- **Alpine `linux-lts` guest kernel for the validated PPPoE path**;
- QEMU/KVM VM, not LXC;
- 1 vCPU to begin, CPU type `host` on a fixed homelab node;
- 1 GiB RAM;
- 8 GiB reliable virtual disk;
- two VirtIO NICs;
- QEMU Guest Agent enabled and installed;
- current QEMU machine type supported by the host.

### Why `linux-lts`

The 2026-08-01 real PPPoE test initially used Alpine `linux-virt`. That running
kernel did not provide the PPPoE kernel module required by the appliance. After
switching the guest to `linux-lts`, the PPPoE module was available and the real
WAN test succeeded.

The requirement is capability-based, not merely a package-name rule. Before
installing or testing PPPoE, verify:

```sh
uname -a
modprobe pppoe
```

A failure of `modprobe pppoe` is a hard stop. On the validated Proxmox path,
install/boot `linux-lts`, reboot into it and repeat the check before proceeding.
Both Minimal Router Alpine installers now perform this check themselves and fail
closed when the module is unavailable.

Observed RAM use in the pilot was approximately 73 MB after a clean `linux-lts`
boot and 172 MB after the exercised workload. A larger kernel package on disk did
not imply proportional resident-memory use in this sample.

## Network boundary

Use two explicitly documented NICs:

- `net0`: test WAN/NAT bridge during the first trial;
- `net1`: isolated LAN bridge such as `vmbr1`.

Interface order inside Linux may differ from Proxmox numbering. Confirm roles by
bridge, link state, carrier, address and route; never guess solely from
`eth0`/`eth1` or `ens18`/`ens19` numbering.

Safety requirements:

1. Do not connect the candidate WAN directly to the ISP during the first trial.
2. Do not connect the isolated LAN bridge to the normal household/office LAN.
3. Never allow the active pfSense DHCP server and candidate DHCP server on the
   same broadcast domain.
4. Keep the Proxmox console open during setup, updates, network changes and
   rollback tests.
5. Record bridge purpose locally, but do not publish raw VM configs, MAC
   addresses, public addresses, hostnames or credentials.

## Manual VM creation

1. Create an Alpine Linux 3.22 x86_64 VM.
2. Install/boot `linux-lts` and verify `modprobe pppoe` succeeds.
3. Allocate the baseline CPU, RAM, disk and two VirtIO NICs.
4. Confirm both interfaces exist and identify WAN/LAN by topology, not name.
5. Install and enable `qemu-guest-agent` and time synchronization.
6. Shut down gracefully and take a known-good pre-install snapshot.
7. Build the exact repository commit on a trusted machine.
8. Transfer the archive and checksum over a trusted local path and verify it.
9. Install, start `router-applyd` and `routerd`, and complete setup from a client
   connected only to the isolated LAN.

Build and verify:

```sh
git clone https://github.com/vladimirperovic/minimalrouter.git
cd minimalrouter
git checkout main
git pull --ff-only
git rev-parse HEAD
pnpm --dir web install --frozen-lockfile
make dist-amd64
cd build
sha256sum -c minimalrouter-linux-amd64.tar.gz.sha256
```

## Baseline guest verification

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
rc-update show | grep -E 'routerd|router-applyd'
router-update status
```

Confirm:

- PPPoE kernel support loads successfully;
- WAN/LAN roles remain correct after reboot;
- management is reachable only from intended LAN/WireGuard paths;
- services are not crash-looping;
- storage is writable and has free space;
- system time is correct;
- no configuration or update transaction remains pending.

## Required pilot validation

The first owner pilot has already demonstrated basic real PPPoE/Internet
forwarding, a real external WireGuard phone connection with dashboard access and
a successful operational fallback to pfSense. Do not unnecessarily repeat those
claims as if they were still completely untested; instead extend the evidence.

Before moving toward unattended production use, record:

- five graceful guest/host reboot cycles with stable interface identity;
- repeated PPPoE disconnect/reconnect/authentication/reboot recovery;
- DHCP, DNS, NAT and default-deny WAN validation after those cycles;
- dashboard login/logout and management-boundary checks;
- unconfirmed disruptive-change rollback;
- MinimalRouter-managed **No-IP** update, external resolution and later
  public-IP-change propagation;
- WireGuard recovery after PPPoE reconnect/reboot;
- update activation, reboot, health verification and explicit rollback;
- encrypted backup export and restore into a fresh VM;
- CPU/RAM/disk, packet rate, latency, jitter, packet loss, throughput and thermal
  measurements over longer runs;
- independent external IPv4/IPv6 scan;
- at least seven days of stable operation before any production recommendation.

## Proxmox lifecycle rules

Use graceful shutdown for normal operation:

```sh
qm shutdown <VMID> --timeout 60
qm status <VMID>
```

Use `qm stop` only as an explicitly planned abrupt-power test after backup,
snapshot and rollback readiness. A Proxmox snapshot is not a substitute for an
encrypted Minimal Router backup.

The successful 2026-08-01 fallback used a failsafe scheduled on the Proxmox host
before the router cutover. That host-side safety path remained independent when
the management connection dropped during the transition. Keep an equivalent
out-of-band rollback mechanism during future real-WAN tests.

## Update and rollback

Do not overwrite active binaries manually. Use the verified A/B path for signed
payloads:

```sh
router-update status
router-update stage --dir <EXTRACTED_SIGNED_PAYLOAD> --manifest <SIGNED_MANIFEST>
router-update activate --version <VERSION> --confirm ACTIVATE-UPDATE
```

Restore the previous verified slot with:

```sh
router-update rollback --confirm ROLLBACK-UPDATE
```

## Production boundary

Do not replace pfSense solely because the first real pilot succeeded. Repeated
PPPoE/reboot recovery, MinimalRouter-managed No-IP validation, external scanning,
restore, sustained load/thermal behavior, destructive storage/power tests,
signed recovery media and independent review remain release gates.

See [`TROUBLESHOOTING.md`](TROUBLESHOOTING.md) for safe diagnostics.
