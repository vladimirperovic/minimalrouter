# Proxmox VE — v0.1.6 Golden ISO pilot

Minimal Router OS should run as a QEMU/KVM VM, not an LXC container.

v0.1.6's preferred first-install path is the Golden Appliance ISO. You do not
install Alpine separately before booting the ISO.

Current evidence: [`CURRENT_VALIDATION.md`](CURRENT_VALIDATION.md).

## Recommended VM

The exact automated end-to-end target is deliberately conservative:

- Machine: ordinary QEMU/KVM PC
- Firmware: **SeaBIOS**
- Architecture: AMD64 / x86-64
- CPU: 1 vCPU or more; `host` is reasonable on a fixed homelab node
- RAM: 1 GiB or more
- Disk: one VirtIO disk, minimum 8 GiB
- NICs: two VirtIO NICs
- CD/DVD: MinimalRouter v0.1.6 ISO
- Console: noVNC available during first pilot
- Optional: `serial0` socket for `ttyS0` recovery

The installed Golden image already contains Alpine 3.22, `linux-lts`, PPPoE
support, MinimalRouter and runtime dependencies. QEMU Guest Agent is not required
for installation and is not part of the minimum Golden appliance contract.

## Network layout

Use two clearly documented adapters:

- **WAN:** test/NAT bridge during initial setup, later the intended ONT/modem path
- **LAN:** isolated bridge or switch used only by the test client

Do not infer WAN/LAN purely from `eth0`/`eth1`. Before confirming firstboot,
compare the MAC addresses shown by MinimalRouter with the NICs in Proxmox.

Never place the candidate DHCP server and the production router DHCP server on
the same broadcast domain.

## Serial console

Add serial without removing noVNC:

```sh
qm set <VMID> --serial0 socket
qm terminal <VMID>
```

Select:

```text
MinimalRouter Installer (serial ttyS0 115200)
```

when you want the install UI on serial. After firstboot, the same port becomes a
normal root recovery getty at 115200.

A serial-only display can be configured later, but keeping both console paths is
safer during initial qualification.

## Installation

1. Create the blank VM using the profile above.
2. Attach the verified `minimalrouter-0.1.6-amd64.iso`.
3. Keep the LAN isolated from the production DHCP domain.
4. Boot the ISO using VGA/noVNC or the dedicated serial entry.
5. The live flasher verifies the Golden image and automatically selects the only
   clearly virtual disk in the normal one-disk VM case.
6. Wait for the raw image copy and automatic reboot.
7. On installed firstboot, confirm WAN/LAN, optionally enter PPPoE, set the
   Dashboard password, review, and set the recovery/SSH root password.
8. After the Dashboard/SSH/serial summary appears, detach the ISO or put the disk
   first in the boot order.
9. Connect a client to the isolated LAN and open `https://192.168.1.1:8443`.

Full flow: [`ISO_INSTALLATION.md`](ISO_INSTALLATION.md).

## First verification

From serial or trusted-LAN SSH:

```sh
uname -a
modprobe pppoe
ip -brief link
ip -brief address
ip route
rc-service sshd status
rc-service router-applyd status
rc-service routerd status
ss -lnt
nft list ruleset
```

Expected management paths:

```text
Dashboard: https://192.168.1.1:8443
SSH:       root@192.168.1.1
Serial:    ttyS0 @ 115200
```

## Real-WAN pilot checklist

Before the candidate replaces the known-good router, record evidence for:

- correct WAN/LAN MAC mapping;
- LAN DHCP and DNS;
- real PPPoE authentication and default route;
- Internet forwarding and expected NAT/firewall behavior;
- no direct Dashboard or SSH exposure from WAN;
- external WireGuard recovery path if configured;
- normal guest reboot recovery;
- cold Proxmox/guest reboot recovery;
- MinimalRouter-managed No-IP update and later public-IP change;
- backup export and restore into a fresh VM;
- external IPv4/IPv6 scan;
- sustained throughput, latency/loss, CPU/RAM and thermals;
- repeated PPPoE reconnects;
- timed device-pause expiry and resume behavior;
- abrupt-power tests only after rollback is prepared.

## Lifecycle and rollback

Use graceful shutdown normally:

```sh
qm shutdown <VMID> --timeout 60
qm status <VMID>
```

Use `qm stop` only for deliberate abrupt-power testing.

Keep the previous router available until the pilot is complete. A Proxmox
snapshot is useful, but it is not a substitute for an encrypted MinimalRouter
backup.

## Current firmware boundary

The installer ISO itself retains BIOS and UEFI boot metadata. The **installed
Golden disk** currently uses an MBR partition table and ExtLinux and is qualified
end-to-end on SeaBIOS. UEFI installed-disk support must be tested separately
before it is documented as supported.

Minimal Router v0.1.6 remains a controlled Beta pilot, not an unattended
pfSense/OpenWrt replacement.
