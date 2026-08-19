# Installation

Minimal Router OS v0.1.5 has two installation paths.

## Preferred: Golden Appliance ISO (AMD64 / Proxmox)

For a new x86-64 Proxmox/KVM VM, use the published Golden ISO. Alpine Linux does
**not** need to be installed first.

Start here:

- [`ISO_INSTALLATION.md`](ISO_INSTALLATION.md) — complete ISO procedure
- [`PROXMOX.md`](PROXMOX.md) — VM configuration
- [`GOLDEN-IMAGE.md`](GOLDEN-IMAGE.md) — how the ISO is built and why

The ISO contains a CI-built, bootable Golden disk image. The live environment
only verifies and copies that image to the VM disk, records whether VGA or serial
was selected, and reboots. The installed appliance then runs firstboot.

Current fully exercised target:

```text
Architecture: AMD64 / x86-64
Hypervisor:   QEMU/KVM / Proxmox
Firmware:     SeaBIOS
Disk:         >= 8 GiB, VirtIO proven in CI
Memory:       >= 1 GiB for installation
NICs:         2 VirtIO adapters (WAN + LAN)
Dashboard:    https://192.168.1.1:8443
Serial:       ttyS0 @ 115200
```

The installer media itself retains BIOS and UEFI boot metadata, but v0.1.5 does
not claim a fully qualified UEFI **installed-disk** path yet.

## First boot

After the Golden image is flashed, the VM reboots from its disk. Firstboot runs
before networking, SSH, `router-applyd` or `routerd` are allowed to start.

It asks for:

1. WAN/LAN roles;
2. optional PPPoE credentials — leave username empty to configure later;
3. Dashboard administrator password (minimum 12 characters);
4. final non-secret review;
5. separate Linux root password for local/serial/trusted-LAN SSH recovery.

Firstboot then:

- saves canonical router configuration;
- generates unique SSH host keys;
- enables recovery gettys including `ttyS0`;
- marks firstboot complete;
- allows normal networking and MinimalRouter services to start.

Expected endpoints:

```text
Web Dashboard: https://192.168.1.1:8443
SSH recovery:  ssh root@192.168.1.1
Serial:        ttyS0 @ 115200
```

## Alternative: signed distribution archive on an existing Alpine system

The release also publishes AMD64 and ARM64 distribution archives for advanced
operators who deliberately manage the base Alpine installation themselves.
This is useful for development, unsupported platforms and controlled migrations;
it is no longer the recommended first-time Proxmox path.

Requirements:

- Alpine Linux 3.22;
- a kernel containing the required PPPoE module (`linux-lts` is the validated x86-64 path);
- one WAN and one LAN interface;
- local console access;
- release archive and matching verification material.

Before modifying the host:

```sh
cat /etc/alpine-release
uname -a
modprobe pppoe
ip -brief link
ip -brief address
```

Extract the release archive into a private directory and run:

```sh
sudo sh install.sh
```

For a fully pre-provisioned air-gapped Alpine host:

```sh
sudo sh install.sh --offline
```

Offline archive mode never installs missing packages. It checks them locally and
fails if dependencies are absent.

### Interface ownership

MinimalRouter owns WAN, LAN and tunnel interfaces. The distribution installer
rewrites `/etc/network/interfaces` so physical interfaces are `manual` and saves
the prior file as:

```text
/etc/network/interfaces.minimalrouter-backup
```

This avoids a stock Alpine DHCP stanza competing with PPPoE or `router-applyd`.

## Verification after installation

From console or trusted-LAN SSH:

```sh
uname -a
modprobe pppoe
ip -brief address
ip route
rc-service sshd status
rc-service router-applyd status
rc-service routerd status
ss -lnt
nft list ruleset
```

Confirm the Dashboard opens at exactly:

```text
https://192.168.1.1:8443
```

Before real ISP cutover, also confirm DHCP/DNS from an isolated LAN client,
correct WAN/LAN mapping, no WAN management exposure and a known-good rollback
path.

## Before unattended use

A successful install is not a production-readiness claim. Keep the existing
router available until repeated real PPPoE/reboot recovery, backup restore,
external scans, destructive fault tests and longer soak testing are complete.

Current evidence: [`CURRENT_VALIDATION.md`](CURRENT_VALIDATION.md).
