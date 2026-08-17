# Minimal Router OS v0.1.4 — Golden ISO installation

This is the preferred AMD64/x86-64 first-install path for Proxmox/KVM.

The v0.1.4 ISO is **not a traditional Alpine installer**. It contains a complete,
CI-built bootable MinimalRouter disk image plus a tiny live flasher. The user VM
does not install packages, build an initramfs, partition a target filesystem or
rerun the MinimalRouter installer.

For the architecture and rebuild rules, read
[`GOLDEN-IMAGE.md`](GOLDEN-IMAGE.md).

## Release assets

From the v0.1.4 GitHub release download:

```text
minimalrouter-0.1.4-amd64.iso
minimalrouter-0.1.4-amd64.iso.sha256
```

Verify before use:

```sh
sha256sum -c minimalrouter-0.1.4-amd64.iso.sha256
```

The release also publishes `SHA256SUMS` and a GitHub attestation for the tested
ISO.

## Proven Proxmox VM profile

Use a QEMU/KVM VM, not LXC:

- **Firmware:** SeaBIOS for the fully tested v0.1.4 installed-disk path
- **CPU:** 1 vCPU or more
- **RAM:** 1 GiB or more
- **Disk:** one virtual disk, 8 GiB or larger; VirtIO Block is the exact CI target
- **NIC 1:** VirtIO, WAN/test bridge
- **NIC 2:** VirtIO, isolated LAN bridge
- **CD/DVD:** attach the MinimalRouter ISO
- **Console:** keep noVNC available during the pilot
- **Optional serial:** `serial0` socket for `ttyS0` recovery

Do not put the candidate LAN on the same broadcast domain as another active DHCP
server.

The ISO media itself contains BIOS and UEFI boot metadata. However, the Golden
disk image installed by v0.1.4 is currently qualified end-to-end as an MBR +
ExtLinux + SeaBIOS appliance. Do not treat installer-media UEFI bootability as
proof of an installed UEFI target.

## Optional Proxmox serial console

Add a serial socket:

```sh
qm set <VMID> --serial0 socket
qm terminal <VMID>
```

The ISO has two entries:

```text
MinimalRouter Installer (VGA/noVNC)
MinimalRouter Installer (serial ttyS0 115200)
```

The VGA/noVNC entry is the production default. Select the serial entry when you
want the entire install UI on `ttyS0`.

After installation, `ttyS0` remains a password-protected recovery login at
115200 baud. For the first real pilot, keep noVNC as an independent fallback even
when using serial.

## Stage 1 — live Golden-image flasher

When the ISO boots, the live environment does only a small set of operations:

1. locate `/minimalrouter/golden.img.gz` on the boot media;
2. verify its SHA-256 checksum;
3. run `gzip -t` to reject corruption;
4. confirm at least 1 GiB installation memory and an 8 GiB target disk;
5. enumerate safe non-removable candidate disks;
6. stop if an existing MinimalRouter installation marker is detected;
7. on a normal one-disk QEMU/Proxmox VM, auto-select the clearly virtual disk;
8. raw-copy the already-built appliance with `gzip -dc ... | dd`;
9. record `tty1` or `ttyS0` in the safe post-MBR gap so firstboot continues on
   the console the operator selected;
10. sync and reboot.

No package transaction occurs on the user VM.

The live flasher deliberately does **not** run:

```text
apk add / apk update
setup-disk
sfdisk / mkfs
mkinitfs
chroot
install.sh / install-core.sh
```

If there are multiple/ambiguous target disks, automatic erase is disabled. The
installer shows candidate paths and requires the exact word:

```text
ERASE
```

before a manual target is destroyed.

## Stage 2 — installed-appliance firstboot

After Stage 1 the VM boots the Golden image from its disk. The matching
`linux-lts` kernel, initramfs and `/lib/modules/<release>` tree are already there.

Firstboot runs before network-facing services and asks for:

1. **WAN/LAN roles.** MinimalRouter probes PPPoE/link evidence and suggests ports.
   If confidence is low, the operator must explicitly confirm the MAC-address
   mapping; Enter alone is not accepted.
2. **Optional PPPoE.** Leave the username empty to configure PPPoE later.
3. **Dashboard administrator password.** Minimum 12 characters.
4. **Review.** Confirms non-secret WAN/LAN/management choices.
5. **Recovery / SSH root password.** Separate from the Dashboard password.

Firstboot then saves canonical state, generates unique SSH host keys, enables
recovery gettys, writes `/etc/minimalrouter/firstboot-complete` and allows normal
services to start.

## What should work after firstboot

```text
LAN:            192.168.1.1/24
Web Dashboard:  https://192.168.1.1:8443
SSH recovery:   ssh root@192.168.1.1
Serial recovery: ttyS0 @ 115200
```

From console/SSH:

```sh
ip -brief address
ip route
modprobe pppoe
rc-update show default
rc-service sshd status
rc-service router-applyd status
rc-service routerd status
ss -lnt
nft list ruleset
cat /etc/apk/repositories
cat /proc/cmdline
```

## What CI proves

The `Appliance ISO` GitHub Actions workflow builds the actual production ISO and
then:

- verifies the ISO checksum and embedded Golden payload;
- boots the production default and proves the flasher service starts;
- remasters **only the boot-menu default** to serial so Expect can drive the same
  installer non-interactively;
- flashes a blank 8 GiB VirtIO disk;
- reboots into the installed appliance;
- completes firstboot over `ttyS0`;
- logs in as root over the installed serial getty;
- performs a real password-authenticated SSH login;
- verifies `192.168.1.1/24`;
- verifies the nftables trusted-LAN SSH rule and TCP/22 listener;
- verifies the kernel/module release match;
- verifies the MinimalRouter state DB and firstboot marker;
- verifies `routerd` service/readiness and Dashboard TCP/8443;
- emits `FULL_ISO_INSTALL_OK` only after all required markers pass.

The signed release workflow repeats the full install test against the release ISO
before publishing it.

## Current limitations

The automated test does **not** prove:

- real ISP PPPoE credentials/authentication;
- physical NIC behavior;
- a full installed-disk UEFI path;
- long-duration operation, thermals or power-loss behavior;
- external IPv4/IPv6 exposure from a real ISP.

Those remain owner-Proxmox/real-lab gates.

## If the install fails

Do not turn the live ISO back into a manual Alpine installer.

Use noVNC or serial to capture the exact last message. The flasher opens a local
recovery shell on fatal errors without making further disk changes. Firstboot
also opens a recovery shell if configuration cannot be completed safely.

Collect only redacted diagnostics such as:

```sh
lsblk
cat /proc/cmdline
ip -brief link
ip -brief address
dmesg | tail -100
```

If the installed system reached recovery login, also collect:

```sh
rc-service sshd status
rc-service router-applyd status
rc-service routerd status
nft list ruleset
ss -lnt
```

Never paste real PPPoE, DDNS or WireGuard credentials into a public issue.
