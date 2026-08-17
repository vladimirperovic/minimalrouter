# MinimalRouter v0.1.4 Golden Appliance ISO — source of truth

This document records **how and why** the v0.1.4 Golden ISO is built. It exists so
a future maintainer or AI agent does not accidentally return to the fragile
installer model that preceded it.

If the ISO is ever rebuilt, refactored or debugged, read this file before changing
`packaging/alpine/`, `.github/workflows/iso.yml`, `scripts/ci/iso-full-install.exp`
or the release workflow.

## The invariant

**The user's Proxmox VM is a flasher target, not an Alpine build host.**

All operating-system construction happens before the user boots the ISO:

```text
CI / trusted build host
  ├─ build Go binaries + Dashboard
  ├─ assemble Alpine 3.22 rootfs with linux-lts and runtime packages
  ├─ install MinimalRouter into that rootfs
  ├─ create one bootable 8 GiB MBR/ext4 Golden disk image
  ├─ compress + SHA256 the image
  └─ inject it into a verified Alpine Extended ISO with a tiny flasher

User Proxmox VM
  ├─ boot ISO
  ├─ verify Golden image
  ├─ raw-copy Golden image to safe target disk
  ├─ persist selected console marker
  ├─ reboot
  └─ installed firstboot collects router-specific state
```

The live flasher must never become a second OS installer.

## Why the old model was abandoned

### 1. Offline APK/repository failures

The original all-in-one installer tried to perform package transactions from the
live environment. Real recovery sessions hit failures such as temporary CDN
errors, wrong `/media/cdrom/...` repository paths, missing `APKINDEX` discovery
and packages such as `sfdisk` not being selectable.

That made a router installer depend on networking and package-manager state before
WAN/PPPoE was working — exactly when it must be most self-contained.

**v0.1.4 rule:** `live-installer.sh` performs no `apk` transaction.

### 2. Live-kernel / target-kernel mismatch

The Alpine Extended ISO can boot one `linux-lts` patch release while the rootfs
builder resolves a newer v3.22 `linux-lts`. During development we observed a live
kernel and installed kernel with different patch releases. Trying to build or
repair target initramfs/modules from the live kernel produced missing-module
behavior and made the install depend on whichever package version happened to be
resolved that day.

**v0.1.4 rule:** kernel, initramfs and `/lib/modules/<release>` are assembled and
validated together in CI. The live flasher never runs `mkinitfs`.

### 3. Too many destructive steps on the user VM

A traditional live installer had to partition, format, resolve packages, chroot,
install MinimalRouter, build boot files and then configure networking. Every step
created another failure/recovery branch and forced long commands through noVNC.

The Golden model reduces the destructive target operation to essentially:

```sh
gzip -dc golden.img.gz | dd of=/dev/<safe-target> bs=4M
sync
reboot
```

### 4. CI harness false negatives

Two late v0.1.4 failures were test-harness defects rather than appliance defects:

- an `sshd` runlevel marker assumed whitespace before `sshd`, which is not
  guaranteed by Alpine's `rc-update` formatting;
- `INSTALLED_SSH_OK` had already been consumed after proving a real SSH login and
  was incorrectly expected a second time later.

These were fixed without weakening the real install checks. Future agents should
never “solve” a failing E2E by deleting a real runtime assertion; first establish
whether the failure is the appliance or the observer.

## Authoritative source files

Read these in order:

```text
packaging/alpine/build-iso.sh
packaging/alpine/build-rootfs.sh
packaging/alpine/build-golden-image.sh
packaging/alpine/live-installer.sh
packaging/alpine/firstboot.sh
packaging/alpine/firstboot.initd
scripts/ci/iso-full-install.exp
.github/workflows/iso.yml
.github/workflows/release.yml
```

User-facing guides:

```text
docs/ISO_INSTALLATION.md
docs/PROXMOX.md
docs/INSTALLATION.md
```

## Stage A — build MinimalRouter distribution

`build-iso.sh` normally builds AMD64 with release metadata:

```text
BUILD_VERSION = VERSION
BUILD_COMMIT  = source commit
BUILD_DATE    = build timestamp
```

For ordinary CI, the distribution is created by the ISO builder.

For a **GitHub release**, the order is different and security-sensitive:

1. build release AMD64/ARM64 distributions;
2. sign them with the protected firmware signing key;
3. embed `firmware-signing.pub` into each distribution;
4. call `build-iso.sh` with:

```text
MINIMALROUTER_USE_EXISTING_DIST=1
MINIMALROUTER_REQUIRE_SIGNED_DIST=1
```

This prevents the ISO builder from overwriting the signed AMD64 payload with an
unsigned development rebuild. A release ISO must fail if
`build/dist/minimalrouter-linux-amd64/firmware-signing.pub` is absent.

## Stage B — build Alpine rootfs

`packaging/alpine/build-rootfs.sh` builds a clean Alpine 3.22 x86-64 rootfs in a
trusted builder/container.

Important packages include:

```text
alpine-base alpine-conf linux-lts linux-firmware-none
e2fsprogs e2fsprogs-extra grub grub-efi syslinux dosfstools util-linux
nftables ppp ppp-pppoe dnsmasq iproute2 iputils-ping iputils-arping
ca-certificates openssh-server wireguard-tools-wg doas squid hostapd iw
inadyn chrony logrotate
```

`resize2fs` is explicitly required because firstboot expands/verifies the Golden
filesystem against the real VM disk.

The rootfs builder:

- seeds current official Alpine keys before target-root package operations;
- installs packages into the target root;
- runs MinimalRouter's distribution installer in image-build/offline mode;
- removes build staging files;
- removes `/etc/machine-id` and SSH host keys so clones do not share identity;
- writes the installed console/MOTD guidance;
- produces `minimalrouter-rootfs-<version>-amd64.tar.gz`.

Package installation happens here, never in the user's live flasher.

## Stage C — build the bootable Golden disk

`packaging/alpine/build-golden-image.sh` creates a logical 8 GiB raw image.

Current v0.1.4 layout:

```text
DOS/MBR partition table
└─ partition 1
   ├─ start: 1 MiB
   ├─ type: Linux (83)
   ├─ bootable
   ├─ ext4
   └─ label: minimalrouter-root
```

The builder reads the ext4 UUID and writes the same UUID into `/etc/fstab` and
ExtLinux:

```text
root=UUID=<UUID> rootfstype=ext4
```

This makes the copied appliance independent of whether the target disk later
appears as `/dev/vda`, `/dev/sda` or another supported block name.

The image contains the exact matching set:

```text
/boot/vmlinuz-lts
/boot/initramfs-lts
/lib/modules/<same-kernel-release>
```

It also installs:

```text
/usr/libexec/minimalrouter/firstboot
/etc/init.d/minimalrouter-firstboot
/usr/sbin/router-setup
```

and records:

```text
/etc/minimalrouter/installed
  installed_by=golden-image
  image_layout=mbr-ext4
  root_uuid=<UUID>
```

ExtLinux is installed once in CI, and Syslinux MBR code is written to the raw disk.
The raw 8 GiB disk is then gzip-compressed; sparse/zero blocks compress heavily.

## Stage D — build the live ISO shell

`packaging/alpine/build-iso.sh` downloads the exact Alpine Extended base ISO and
verifies its published SHA-256 before remastering it.

The final ISO injects:

```text
/minimalrouter/VERSION
/minimalrouter/BUILD_COMMIT
/minimalrouter/BUILD_DATE
/minimalrouter/BUILD-INFO
/minimalrouter/golden.img.gz
/minimalrouter/golden.img.gz.sha256
/minimalrouter.apkovl.tar.gz
/boot/syslinux/syslinux.cfg
/boot/grub/grub.cfg
```

The live overlay adds one OpenRC service: `minimalrouter-installer`.

Production boot entries:

```text
MinimalRouter Installer (VGA/noVNC)
MinimalRouter Installer (serial ttyS0 115200)
```

VGA/noVNC is the default. Serial passes:

```text
minimalrouter.console=ttyS0 console=ttyS0,115200
```

## Stage E — live flasher safety contract

`packaging/alpine/live-installer.sh` may:

- locate the Golden image;
- verify SHA-256 and gzip integrity;
- verify minimum memory and target-disk size;
- enumerate non-removable disks;
- detect a previous MinimalRouter installation;
- automatically erase only a single, clearly virtual QEMU/Proxmox disk;
- require an exact manual disk path plus `ERASE` when selection is ambiguous;
- raw-copy the Golden image;
- write the console marker;
- sync and reboot.

It must **not**:

```text
apk add / apk update / apk fix
setup-disk
sfdisk / mkfs.*
mkinitfs
chroot
install-core.sh
unpack a target rootfs
mount and patch the flashed target
```

`.github/workflows/iso.yml` contains static guards that fail when these forbidden
patterns are reintroduced.

## Target-disk protection

Automatic erase is allowed only when all are true:

1. QEMU/KVM/Proxmox is detected;
2. exactly one candidate installation disk exists;
3. it is non-removable;
4. it is clearly a virtual disk type accepted by the safety check.

Existing MinimalRouter installations are detected by an exact `tty1`/`ttyS0`
marker in the post-MBR gap, with filesystem-label fallback for older images.

If detected, the ISO stops before overwrite.

## Console marker design

The flasher must remember whether the operator chose VGA or serial without
mounting the freshly copied root filesystem.

It writes exactly one string at byte offset 32768 in the unused post-MBR gap:

```text
tty1
```

or:

```text
ttyS0
```

Installed `firstboot.sh` reads that marker and binds its full interactive session
to the selected console.

## Stage F — installed firstboot

`minimalrouter-firstboot` is ordered:

```text
before networking sshd router-applyd routerd
```

Nothing network-facing may start with cloned/default state.

Firstboot:

1. verifies/expands the ext4 root filesystem with `resize2fs`;
2. runs `router-setup collect` for WAN/LAN, optional PPPoE and Dashboard password;
3. applies the reviewed config offline to canonical state;
4. sets a separate recovery/root password;
5. generates unique SSH host keys;
6. restores `tty1` and `ttyS0` recovery gettys and adds `ttyS0` to `securetty`;
7. writes `/etc/minimalrouter/firstboot-complete`;
8. returns control to OpenRC so networking and MinimalRouter services may start.

Expected management endpoints:

```text
https://192.168.1.1:8443
ssh root@192.168.1.1
ttyS0 @ 115200
```

## E2E test — what “green Appliance ISO” means

`scripts/ci/iso-full-install.exp` boots QEMU with:

- one blank 8 GiB VirtIO disk;
- two VirtIO NICs;
- serial stdio;
- a host-forward used for a **real** SSH login after install.

CI changes only the ISO boot-menu default to the existing serial entry so Expect
can drive the same production flasher.

The test proves:

- Golden checksum/gzip verification;
- safe VM disk auto-selection;
- raw image copy and reboot;
- installed `linux-lts` boot;
- firstboot completion over `ttyS0`;
- recovery root login on the installed `ttyS0` getty;
- real password-authenticated SSH login;
- LAN `192.168.1.1/24`;
- nftables trusted-LAN SSH accept rule;
- SSH TCP/22 listener;
- `sshd` enabled in OpenRC;
- firstboot-complete marker and SQLite state DB;
- kernel and `/lib/modules/$(uname -r)` match;
- `routerd` service and readiness marker;
- Dashboard TCP/8443 listener;
- valid Alpine v3.22 main/community repositories.

`FULL_ISO_INSTALL_OK` is emitted only after every required marker succeeds.

The signed release workflow repeats this full install test against the release
ISO after firmware signing and before publication.

## Release artifacts

v0.1.4 release publication includes the production ISO and verification material:

```text
minimalrouter-0.1.4-amd64.iso
minimalrouter-0.1.4-amd64.iso.sha256
SHA256SUMS
```

The ISO is also covered by a GitHub artifact attestation. The appliance inside the
ISO is constructed from the signed AMD64 distribution and contains the pinned
`firmware-signing.pub` trust anchor used by `router-update`.

## What CI does not prove

Do not turn automated evidence into a broader claim. v0.1.4 still needs separate
real evidence for:

- real ISP PPPoE authentication/reconnect behavior;
- physical NICs;
- installed-disk UEFI boot;
- external IPv4/IPv6 scans;
- thermals and sustained throughput;
- abrupt power-loss behavior;
- long unattended operation.

The installer ISO contains UEFI boot metadata, but the current Golden target disk
is MBR + ExtLinux and the full E2E target is SeaBIOS.

## Future-agent checklist

Before changing the ISO architecture:

1. identify whether the failure is build, live flasher, firstboot, installed
   runtime or test harness;
2. do not add live package installation as a shortcut;
3. do not regenerate target initramfs from the live kernel;
4. keep one source of truth for kernel/modules inside the Golden image;
5. preserve safe disk auto-selection and reinstall guard;
6. preserve both VGA and `ttyS0` recovery;
7. preserve firstboot-before-networking ordering;
8. preserve signed release payload/trust anchor;
9. keep real SSH/serial/runtime assertions in E2E;
10. update this document and user install docs with any architecture change.

If a future design cannot satisfy these invariants, document the reason and add
equivalent or stronger safety/evidence before replacing them.
