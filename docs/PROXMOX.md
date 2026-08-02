# Proxmox VE

Minimal Router OS should run as a QEMU/KVM VM, not an unprivileged LXC container.
Use this guide only for a controlled pilot with console access and a known-good
router ready for rollback.

Current evidence: [`CURRENT_VALIDATION.md`](CURRENT_VALIDATION.md).

## Recommended VM

- Alpine Linux 3.22 x86_64
- Alpine `linux-lts` for the validated PPPoE path
- 1 vCPU, CPU type `host` on a fixed homelab node
- 1 GiB RAM
- 8 GiB disk
- two VirtIO NICs
- QEMU Guest Agent

The 2026-08-01 pilot observed about 73 MB RAM after a clean `linux-lts` boot and
172 MB after the exercised workload. These are observations, not sizing
requirements.

## PPPoE preflight

The tested `linux-virt` kernel did not provide the required PPPoE module;
`linux-lts` did. Before installation:

```sh
uname -a
modprobe pppoe
```

If `modprobe pppoe` fails, stop.

## Network layout

Use two clearly documented NICs:

- WAN: test/NAT bridge during initial setup
- LAN: isolated bridge/switch

Do not infer WAN/LAN from `eth0`, `eth1`, `ens18`, etc. Confirm by bridge,
carrier, address and route.

Never place the candidate DHCP server and the production router DHCP server on
the same broadcast domain.

## Setup

1. Create the Alpine VM and boot `linux-lts`.
2. Verify `modprobe pppoe`.
3. Attach the two VirtIO NICs and confirm their roles.
4. Install QEMU Guest Agent and time synchronization.
5. Take a known-good pre-install snapshot.
6. Build and verify the exact Minimal Router commit.
7. Install using [`INSTALLATION.md`](INSTALLATION.md).
8. Complete first-run setup from a client on the isolated LAN.

Useful guest checks:

```sh
cat /etc/alpine-release
uname -a
modprobe pppoe
ip -brief link
ip -brief address
ip route
df -h
df -i
rc-service router-applyd status
rc-service routerd status
router-update status
```

## Pilot checklist

Before unattended use, record evidence for:

- repeated guest and Proxmox reboot cycles;
- stable WAN/LAN identity;
- repeated PPPoE reconnect and reboot recovery;
- DHCP, DNS, NAT and WAN default-deny behavior;
- rollback of an unconfirmed network change;
- MinimalRouter-managed No-IP update and later IP-change propagation;
- WireGuard recovery after PPPoE reconnect/reboot;
- update activation and rollback;
- encrypted backup restore into a fresh VM;
- sustained throughput, packet rate, latency/loss, CPU/RAM and thermals;
- external IPv4/IPv6 scan;
- at least seven days of stable operation.

## Lifecycle and rollback

Use graceful shutdown normally:

```sh
qm shutdown <VMID> --timeout 60
qm status <VMID>
```

Use `qm stop` only for a deliberate abrupt-power test after rollback is ready.
A Proxmox snapshot is not a substitute for an encrypted Minimal Router backup.

For real-WAN testing, keep an out-of-band host-side rollback mechanism. The
2026-08-01 pilot successfully returned to pfSense in about 93 seconds using such
a path.

## Update

Use the A/B update commands rather than overwriting active binaries:

```sh
router-update status
router-update stage --dir <SIGNED_PAYLOAD> --manifest <SIGNED_MANIFEST>
router-update activate --version <VERSION> --confirm ACTIVATE-UPDATE
router-update rollback --confirm ROLLBACK-UPDATE
```

Minimal Router remains a controlled pilot, not yet an unattended pfSense
replacement.
