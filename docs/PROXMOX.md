# Proxmox VE lab deployment

Minimal Router OS targets a QEMU/KVM virtual machine on Proxmox VE. An
unprivileged LXC container is not a supported router boundary because it shares
the host kernel and cannot provide the same nftables, interface, and device
isolation.

Read [`CURRENT_VALIDATION.md`](CURRENT_VALIDATION.md),
[`INSTALLATION.md`](INSTALLATION.md), [`TESTING.md`](TESTING.md), and
[`RECOVERY.md`](RECOVERY.md) before using this guide.

## Current status

There is no signed stable ISO. The current tree has automated clean-Alpine,
update/rollback, crash/fuzz, ARM64, security, network-namespace, and performance
validation, but those tests do not establish production readiness on a particular
Proxmox host.

Use this only as a controlled pilot with:

- Proxmox console access;
- an isolated LAN;
- a test/NAT WAN during initial validation;
- the existing router ready for immediate rollback.

Do not use `packaging/proxmox/create-vm.sh` as an unattended production installer
until a reviewed signed image and independently verifiable checksum are published.

## Recommended VM baseline

- Alpine Linux 3.22 x86_64;
- QEMU/KVM VM, not LXC;
- 1 vCPU to begin, CPU type `host` on a fixed homelab node;
- 1 GiB RAM;
- 8 GiB reliable virtual disk;
- two VirtIO NICs;
- QEMU Guest Agent enabled and installed;
- current QEMU machine type supported by the host.

The measured 512 MiB minimum is a test result, not a production sizing promise.

## Network boundary

Use two explicitly documented NICs:

- `net0`: test WAN/NAT bridge during the first trial;
- `net1`: isolated LAN bridge such as `vmbr1`.

Interface order inside Linux may differ from Proxmox numbering. Confirm roles by
bridge, link state, carrier, address, and route; never guess from `eth0`/`eth1` or
`ens18`/`ens19` alone.

Safety requirements:

1. Do not connect the candidate WAN directly to the ISP during the first trial.
2. Do not connect the isolated LAN bridge to the normal household/office LAN.
3. Never allow the active pfSense DHCP server and the candidate DHCP server on the
   same broadcast domain.
4. Keep the Proxmox console open during setup, updates, network changes, and
   rollback tests.
5. Record bridge purpose locally, but do not publish raw VM configs, MAC addresses,
   public addresses, or credentials.

## Manual VM creation

1. Create an Alpine Linux 3.22 x86_64 VM.
2. Allocate the baseline CPU, RAM, disk, and two VirtIO NICs.
3. Install Alpine and confirm both interfaces exist.
4. Install and enable `qemu-guest-agent` and time synchronization.
5. Shut down gracefully and take a known-good pre-install snapshot.
6. Build the exact repository commit on a trusted machine.
7. Transfer the archive and checksum over a trusted local path.
8. Verify the checksum on the guest.
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

The first-run management address is normally shown by the appliance and is often
`https://192.168.1.1:8443` after interface reconciliation. Do not assume the
address if the selected LAN differs.

## Proxmox lifecycle rules

Use graceful shutdown for normal operation:

```sh
qm shutdown <VMID> --timeout 60
qm status <VMID>
```

Use `qm stop` only as an explicitly planned abrupt-power test after backup,
snapshot, and rollback readiness. Do not force-stop automatically when graceful
shutdown fails; inspect the console and guest-agent state first.

A Proxmox snapshot should be taken only from a known consistent state. It is not a
substitute for an encrypted Minimal Router backup.

## Baseline guest verification

Inside the guest:

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
rc-update show | grep -E 'routerd|router-applyd'
router-update status
```

Confirm:

- WAN/LAN roles remain correct after reboot;
- management is reachable only from intended LAN/WireGuard paths;
- services are not crash-looping;
- storage is writable and has free space;
- system time is correct;
- no configuration or update transaction remains pending.

## Required pilot validation

Before moving beyond the isolated test topology:

- five graceful reboot cycles;
- DHCP, DNS, NAT, and default-deny WAN validation;
- dashboard login/logout and management-boundary checks;
- unconfirmed disruptive-change rollback;
- update activation, reboot, health verification, and explicit rollback;
- encrypted backup export and restore into a fresh VM;
- CPU/RAM/disk measurements at idle and under load;
- latency, jitter, packet loss, throughput, packet rate, and management
  responsiveness under load;
- external scan, real WireGuard, and real PPPoE only after isolated tests pass;
- at least seven days of stable operation before any production recommendation.

Record the exact Proxmox version, host kernel, guest kernel, VM resources, NIC
model, bridge topology using synthetic labels, offload settings, commands,
measurements, failures, and recovery steps.

## Update and rollback

Do not overwrite active binaries manually. Use the verified A/B path for signed
payloads:

```sh
router-update status
router-update stage --dir <EXTRACTED_SIGNED_PAYLOAD> --manifest <SIGNED_MANIFEST>
router-update activate --version <VERSION> --confirm ACTIVATE-UPDATE
```

After activation, restart services or reboot and verify health. Restore the
previous verified slot with:

```sh
router-update rollback --confirm ROLLBACK-UPDATE
```

Keep the previous slot until the new version has survived reboot, management,
DHCP/DNS/NAT, and rollback rehearsal.

## Production boundary

Do not replace pfSense solely because the VM boots or a synthetic throughput test
passes. Real PPPoE, real NIC behavior, external scanning, restore, sustained load,
power-loss recovery, signed recovery media, and independent review remain gates.

See [`TROUBLESHOOTING.md`](TROUBLESHOOTING.md) for safe diagnostics.
