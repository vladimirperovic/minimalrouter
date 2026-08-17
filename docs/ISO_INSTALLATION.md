# All-in-one ISO installation

This is the preferred installation path for the current AMD64 / x86-64 pilot.
The Minimal Router OS ISO is a complete appliance installer: it contains the
Alpine Linux 3.22 base system, the target `linux-lts` kernel and modules, the
required Alpine packages, MinimalRouter binaries, the Web Dashboard and the
recovery SSH server. Alpine does **not** need to be installed separately.

The disk installation is designed to work without Internet access. PPPoE may be
configured during the wizard, but the live ISO does not need a working WAN to
partition the disk and install the appliance.

> Minimal Router OS is still an early pilot. Test on an isolated VM first and
> keep the existing router available for rollback.

## What the ISO contains

The builder starts from the pinned Alpine standard ISO and adds:

- Alpine Linux 3.22.5 boot media;
- the complete signed Alpine package bundle required by MinimalRouter;
- the `linux-lts` target kernel and its modules;
- PPP/PPPoE, nftables, dnsmasq, WireGuard and the other router dependencies;
- OpenSSH for temporary recovery access;
- MinimalRouter services and Web Dashboard;
- the first-run console installer;
- BIOS/Syslinux and UEFI/GRUB boot paths;
- VGA/noVNC and `ttyS0` serial recovery paths.

The installer verifies the bundled APK checksums before using them. It discovers
the Alpine repository from the booted media itself; there is no fixed
`/media/cdrom` or similar mount path assumption.

## Build a new ISO

Build from the repository root on a trusted development machine. The builder
needs Go from `go.mod`, Node.js 22, pnpm, `xorriso`, `curl`, `tar`, a SHA-256
utility, and either Docker or Alpine `apk` for resolving the offline package
bundle.

```sh
pnpm --dir web install --frozen-lockfile
make iso
```

The resulting files are written under `build/iso/`:

```text
minimalrouter-<version>-amd64.iso
minimalrouter-<version>-amd64.iso.sha256
APK-SHA256SUMS
```

Verify the ISO before mounting it:

```sh
sha256sum -c build/iso/minimalrouter-*-amd64.iso.sha256
```

On macOS, use the equivalent SHA-256 verification command if GNU `sha256sum` is
not installed.

GitHub Actions also builds and QEMU-boots the ISO on pull requests. Its
`minimalrouter-appliance-iso-<commit>` artifact is the preferred test image when
the **Appliance ISO** check is green because that artifact corresponds exactly
to the tested commit.

## Proxmox VM for the first test

Use a QEMU/KVM VM, not an LXC container. A practical starting configuration is:

- 1 vCPU or more;
- 1 GiB RAM or more;
- 8 GiB virtual disk or larger;
- two VirtIO network adapters;
- one adapter on the WAN bridge toward the modem/ONT;
- one adapter on an isolated LAN bridge;
- the MinimalRouter ISO attached as a virtual CD/DVD drive;
- Proxmox console access kept available during the entire pilot.

Do not connect the test LAN to another active DHCP server while installing.
Keep the current production router available until the new VM has passed the
full reboot and connectivity test.

## Boot console choices

The default boot entry is intended for the normal Proxmox VGA/noVNC console.
The ISO also contains a dedicated serial entry:

```text
Minimal Router OS Installer (serial ttyS0 115200)
```

Use that entry when you want the complete installer wizard on Proxmox serial
console `ttyS0`. The configured serial speed is **115200 baud**.

The installed system keeps a password-protected `ttyS0` recovery login and
persists the serial settings through the installed ExtLinux or GRUB
configuration.

## Installation wizard

After boot, follow the local installer. It will:

1. ask for PPPoE credentials, which may be left empty for an isolated test;
2. inspect the NICs and confirm WAN and LAN roles;
3. ask for the Web Dashboard administrator password;
4. show the non-secret configuration for final confirmation;
5. save that configuration for first disk boot instead of starting the
   production router stack inside the live ISO;
6. ask for a separate Linux root **recovery password**;
7. enable recovery SSH on the selected LAN;
8. ask which disk to erase and require the word `ERASE`;
9. install Alpine Linux plus the complete MinimalRouter package set to disk;
10. copy the verified APK bundle onto the target before the chroot stage;
11. configure SSH and serial recovery on the installed system;
12. reboot into the installed disk, where MinimalRouter performs the final
    network reconciliation.

The live installer deliberately does not run `routerd` or `router-applyd` as a
production router. The reviewed configuration is persisted and the installed
system applies it on its own kernel after reboot.

## Recovery SSH during installation

After the recovery password is created, the selected LAN interface is assigned:

```text
192.168.1.1/24
```

From a client connected only to that LAN, use:

```sh
ssh root@192.168.1.1
```

Use the recovery password from the installer. The live SSH daemon listens only
on `192.168.1.1`; it is not bound to the WAN interface.

This access is intentionally present during the current pilot so a failed disk
or package step can be diagnosed without typing long commands through noVNC.

## After the first disk boot

Detach the ISO if the hypervisor has not already done so and boot from the
installed disk. The expected management endpoints are:

```text
Web Dashboard: https://192.168.1.1:8443
SSH:           root@192.168.1.1
Serial:        ttyS0 @ 115200
```

SSH uses the separate Linux recovery password, not the Web Dashboard password.
The generated firewall allows recovery SSH only from an allowed management LAN
or, when configured, the authenticated WireGuard management interface. No WAN
TCP/22 accept rule is added.

For the current pilot, leave SSH enabled until installation/reboot behavior is
proven. It can be disabled again after the recovery path is no longer needed.

## First verification

Before replacing the existing router, verify from the Proxmox console and from
an isolated LAN client:

```sh
ip -brief link
ip -brief address
ip route
rc-service sshd status
rc-service router-applyd status
rc-service routerd status
ss -lntp | grep ':22'
cat /proc/cmdline
cat /etc/apk/repositories
```

Then confirm:

- the chosen WAN and LAN interfaces are correct;
- the LAN is `192.168.1.1/24`;
- the dashboard opens on `https://192.168.1.1:8443`;
- SSH works from LAN but is not reachable from WAN;
- DHCP and DNS work for an isolated LAN client;
- PPPoE establishes correctly when credentials are configured;
- Internet routing works;
- a normal reboot returns to the same working state;
- the serial login appears on `ttyS0` at 115200 baud.

## If installation fails

The installer opens a local recovery shell when a fatal step fails. If the
failure happens after the recovery password step, SSH should also be available
from the selected LAN.

Useful commands are:

```sh
ip -brief link
ip -brief address
lsblk
mount
cat /etc/apk/repositories
find /media /mnt /run/media -name APKINDEX.tar.gz 2>/dev/null
rc-service sshd status
ss -lntp
cat /tmp/minimalrouter-apk-install.log 2>/dev/null
cat /tmp/minimalrouter-apk-update.log 2>/dev/null
cat /tmp/minimalrouter-target-apk.log 2>/dev/null
```

Do not manually replace `/etc/apk/repositories` with a guessed CD-ROM path. The
installer is expected to locate the boot media and its `APKINDEX.tar.gz`
automatically. If that discovery fails, capture the commands above so the media
layout can be fixed in the installer rather than worked around by hand.
