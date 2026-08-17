# MinimalRouter golden-image appliance

This document records how the MinimalRouter v0.1.4 appliance ISO is built and installed so the process remains reproducible for future maintainers and for the private `minimalrouterhome` profile.

## Goal

The user VM is not an Alpine build machine. Installation must never depend on package repositories, dependency resolution, `setup-disk`, `apk add`, `mkinitfs`, a target chroot, or rerunning the MinimalRouter application installer.

The production model is:

1. GitHub CI builds Alpine Linux and MinimalRouter once.
2. CI creates a complete bootable 8 GiB disk image.
3. CI compresses the image and records its SHA256 checksum.
4. The bootable ISO contains that compressed golden image and a very small flasher.
5. The flasher verifies the image, selects a safe target disk and writes the image byte-for-byte.
6. The VM reboots from the installed disk.
7. A one-shot first-boot wizard stores WAN/LAN, optional PPPoE, Dashboard credentials and the recovery password.
8. Normal router services start only after first boot completes.

## Build pipeline

`packaging/alpine/build-iso.sh` is the top-level builder.

It performs these stages:

- builds the amd64 MinimalRouter distribution and Dashboard;
- calls `packaging/alpine/build-rootfs.sh` to assemble the complete installed Alpine filesystem with the current Alpine 3.22 `linux-lts` kernel and all MinimalRouter runtime packages;
- calls `packaging/alpine/build-golden-image.sh` to turn that filesystem into a bootable disk image;
- downloads and verifies the Alpine Extended ISO used only as the live boot shell;
- creates the tiny MinimalRouter flasher OpenRC overlay;
- injects `golden.img.gz`, its SHA256 manifest and build metadata into the ISO;
- preserves BIOS and UEFI bootability of the installer ISO.

Package installation happens only while CI assembles the root filesystem. It never happens on the user VM.

## Golden disk layout

The v0.1.4 proven target layout is intentionally simple:

- 8 GiB logical raw disk image;
- DOS/MBR partition table;
- one active Linux partition starting at 1 MiB;
- ext4 root filesystem;
- Syslinux/ExtLinux BIOS bootloader;
- Alpine `linux-lts` kernel, its matching modules and its matching initramfs, all produced in the same CI build.

The filesystem is labelled `minimalrouter-root`, but booting does not depend on a device path or filesystem label. CI reads the ext4 filesystem UUID immediately after formatting it and writes the same UUID into both `/etc/fstab` and ExtLinux:

```text
root=UUID=<golden-filesystem-uuid> rootfstype=ext4
```

This is important because the same image may appear as `/dev/vda`, `/dev/sda` or an NVMe device on different Proxmox configurations. The root filesystem identity travels inside the image itself.

## ISO flasher

`packaging/alpine/live-installer.sh` deliberately has a very small responsibility set.

It may:

- locate `/minimalrouter/golden.img.gz` on the boot media;
- validate SHA256 and gzip integrity;
- check minimum RAM and disk size;
- enumerate installation disks;
- stop if an existing MinimalRouter image is detected;
- automatically select the only clearly virtual QEMU/Proxmox disk;
- require an exact `ERASE` confirmation for ambiguous/manual disk selection;
- run `gzip -dc ... | dd of=<disk>`;
- record whether the UI used `tty1` or `ttyS0` in the unused post-MBR gap;
- sync and reboot.

It must not:

- run `apk` package transactions;
- run `setup-disk`;
- partition or format the target;
- mount or mutate the flashed target filesystem;
- run `mkinitfs`;
- chroot into the target;
- rerun MinimalRouter installation scripts.

The filesystem UUID, kernel, modules, initramfs, bootloader and application are final before the ISO is published.

## First boot

The golden image contains `minimalrouter-firstboot`, ordered before networking, SSH and the two MinimalRouter services.

On the first installed-disk boot it asks only for router-specific state:

1. WAN and LAN roles;
2. optional PPPoE credentials;
3. Web Dashboard administrator password;
4. separate Linux root password for emergency console/trusted-LAN SSH recovery.

The wizard then saves the configuration with `router-setup apply --offline`, generates unique SSH host keys, records `/etc/minimalrouter/firstboot-complete`, restores recovery gettys and allows the normal services to start.

After first boot:

- Web Dashboard: `https://192.168.1.1:8443`
- SSH recovery: `ssh root@192.168.1.1`
- Serial recovery: `ttyS0` at 115200 baud

SSH is intended for the trusted LAN only and must not be exposed on WAN.

## CI proof

`.github/workflows/iso.yml` treats the appliance path as a release gate.

The workflow verifies that the live flasher contains none of the forbidden target-install commands, builds the exact golden image, verifies the ISO payload, boots the production ISO, then runs a complete QEMU installation test using an 8 GiB VirtIO disk.

The end-to-end test must prove all of the following from the installed disk:

- golden image checksum succeeds;
- safe QEMU disk auto-selection works;
- raw image copy completes;
- reboot reaches the installed kernel;
- first-boot wizard completes;
- the installed kernel has its matching `/lib/modules/$(uname -r)` tree;
- configuration database exists;
- LAN address `192.168.1.1/24` is applied;
- SSH is enabled and actually accepts a recovery login;
- serial root recovery works;
- nftables permits SSH on the trusted LAN;
- `FULL_ISO_INSTALL_OK` is emitted only after all checks pass.

An ISO should not be treated as a production candidate until this workflow is green.

## Proxmox v0.1.4 target

The first proven installed-disk path is:

- SeaBIOS;
- amd64/x86_64;
- at least 1 GiB RAM;
- at least 8 GiB virtual disk;
- two network adapters (WAN and LAN);
- VirtIO Block is the exact disk model used by CI.

The installer ISO itself remains BIOS and UEFI bootable, but the v0.1.4 golden target disk is currently a SeaBIOS/MBR appliance. A dual BIOS/UEFI golden disk is a later hardening step.

The current root partition occupies the 8 GiB golden-image layout. A larger target disk is accepted, but automatic partition growth beyond the image layout is a later enhancement.

## `minimalrouter` and `minimalrouterhome`

The golden image must remain generic and contain no site secrets.

The intended separation is:

- public `minimalrouter`: application, appliance OS, installer, schemas and generic defaults;
- private `minimalrouterhome`: Vladimir's site profile such as PPPoE, WireGuard, DHCP/static leases and other private configuration.

MinimalRouter v0.1.4 is also intended to expose pfSense-style XML configuration import/export under the Recovery area of the Web Dashboard. That XML profile is the appropriate portable input for a private home configuration; encrypted `.mrbak` remains the full protected backup format.

Secrets must never be baked into the public golden image or public repository.

## Recovery and rollback principles

- Keep the previous production router available until WAN, LAN, DHCP, DNS and reboot behavior have been verified on the new appliance.
- Rebooting the ISO against a disk already marked as MinimalRouter must stop instead of silently overwriting it.
- The golden image artifact and its checksum tie an installation to one CI-tested build.
- Git history remains the source-level rollback path; appliance artifacts remain the binary-level test/install path.
