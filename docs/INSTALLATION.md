# Installation

The preferred AMD64/x86-64 path is the Golden Appliance ISO described first. Advanced operators can instead run the signed distribution archive on an Alpine system they manage themselves. Restoring your own settings onto a fresh install is covered at the end.

This is the preferred AMD64/x86-64 first-install path for Proxmox/KVM.

The v0.3.3 ISO is **not a traditional Alpine installer**. It contains a complete,
CI-built bootable MinimalRouter disk image plus a tiny live flasher. The user VM
does not install packages, build an initramfs, partition a target filesystem or
rerun the MinimalRouter installer.

For the architecture and rebuild rules, read
[`GOLDEN-IMAGE.md`](GOLDEN-IMAGE.md).

## Release assets

From the v0.3.3 GitHub release download:

```text
minimalrouter-0.3.3-amd64.iso
minimalrouter-0.3.3-amd64.iso.sha256
```

Verify before use:

```sh
sha256sum -c minimalrouter-0.3.3-amd64.iso.sha256
```

The release also publishes `SHA256SUMS`, signed AMD64/ARM64 manifests, SPDX SBOMs
and GitHub attestations for the tested artifacts.

## Proven Proxmox VM profile

Use a QEMU/KVM VM, not LXC:

- **Firmware:** SeaBIOS for the fully tested v0.3.3 installed-disk path
- **CPU:** 1 vCPU or more
- **RAM:** 1 GiB or more
- **Disk:** one virtual disk, 8 GiB or larger; VirtIO Block is the exact CI target
- **NIC 1:** VirtIO, WAN/test bridge
- **NIC 2:** VirtIO, isolated LAN bridge
- **CD/DVD:** attach the MinimalRouter ISO
- **Console:** keep noVNC available during the pilot
- **Optional serial:** `serial0` socket for `ttyS0` recovery

The installed appliance loads network drivers for whatever hardware it finds at
boot (`mdev` + `hwdrivers`), so other NIC models are detected too. CI exercises
an emulated Intel e1000 LAN port alongside the VirtIO WAN port; physical NICs
are still outside the automated evidence. The Golden image already contains
Alpine 3.22, `linux-lts`, PPPoE support, MinimalRouter and its runtime
dependencies; the QEMU Guest Agent is not required.

Do not put the candidate LAN on the same broadcast domain as another active DHCP
server.

The ISO media itself contains BIOS and UEFI boot metadata. However, the Golden
disk image installed by v0.3.3 is currently qualified end-to-end as an MBR +
ExtLinux + SeaBIOS appliance. Do not treat installer-media UEFI bootability as
proof of an installed UEFI target.

## Optional Proxmox serial console

Add a serial socket:

```sh
qm set <VMID> --serial0 socket
qm terminal <VMID>
```

On BIOS/SeaBIOS boot the ISO shows a `boot:` prompt on both the VGA console and
`ttyS0` for five seconds:

```text
MinimalRouter installer: press Enter or wait 5 seconds for the VGA/noVNC installer.
To install from this serial console instead, type  minimalrouter-serial  and press Enter.
boot:
```

The two hint lines appear on VGA only; on `ttyS0` you see `boot:`, and pressing
Tab lists the two labels. The VGA/noVNC installer is the production default.
Type `minimalrouter-serial` at that prompt when you want the entire install UI on
`ttyS0`. Typing stops the five-second countdown; after 30 seconds the default
starts regardless, so a noisy serial line cannot park the ISO at `boot:`. UEFI
boot uses GRUB, which lists the same two entries as a menu on both consoles.

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

If there are multiple/ambiguous target disks, or the hypervisor is not
QEMU/KVM/Proxmox (for example Hyper-V or VMware, where the single disk appears as
`/dev/sda  Virtual Disk`), automatic erase is disabled. The installer shows
candidate paths, asks for the exact path and requires the exact word:

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
5. **Recovery / SSH root password.** Separate from the Dashboard password. It
   allows root SSH from the trusted LAN, so use a long, unique password:
   firstboot only warns about a short one and does not yet enforce a minimum.

Firstboot then saves canonical state, generates unique SSH host keys, enables
recovery gettys, writes `/etc/minimalrouter/firstboot-complete` and allows normal
services to start.

## What should work after firstboot

```text
LAN:             192.168.1.1/24
Web Dashboard:   https://192.168.1.1:8443
SSH recovery:    ssh root@192.168.1.1
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
- verifies `192.168.1.1/24` and Dashboard/API readiness;
- verifies the nftables trusted-LAN SSH rule and TCP/22 listener;
- verifies the kernel/module release match;
- verifies the MinimalRouter state DB and firstboot marker;
- cold-boots the installed appliance without the ISO and proves firstboot does not re-enter;
- forces `routerd` failure and verifies supervision recovery;
- performs a warm reboot and repeats readiness checks;
- verifies an existing MinimalRouter disk is refused rather than overwritten;
- verifies an undersized 4 GiB target is rejected before destructive writes;
- emits `FULL_ISO_INSTALL_OK` only after all required markers pass.

The signed release workflow rebuilds the signed release artifacts and repeats the
full install test against the exact tagged release ISO before publishing it.

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

## Proxmox VE

Run Minimal Router OS as a QEMU/KVM VM, not an LXC container, using the
[VM profile](#proven-proxmox-vm-profile) and the ISO stages above. Current
evidence: [`CURRENT_VALIDATION.md`](CURRENT_VALIDATION.md).

### Network layout

Use two clearly documented adapters:

- **WAN:** test/NAT bridge during initial setup, later the intended ONT/modem path
- **LAN:** isolated bridge or switch used only by the test client

Do not infer WAN/LAN purely from `eth0`/`eth1`. Before confirming firstboot,
compare the MAC addresses shown by MinimalRouter with the NICs in Proxmox.

Never place the candidate DHCP server and the production router DHCP server on
the same broadcast domain.

### Real-WAN pilot checklist

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

### Lifecycle and rollback

Use graceful shutdown normally:

```sh
qm shutdown <VMID> --timeout 60
qm status <VMID>
```

Use `qm stop` only for deliberate abrupt-power testing.

Keep the previous router available until the pilot is complete. A Proxmox
snapshot is useful, but it is not a substitute for an encrypted MinimalRouter
backup.

### Current firmware boundary

The installer ISO itself retains BIOS and UEFI boot metadata. The **installed
Golden disk** currently uses an MBR partition table and ExtLinux and is qualified
end-to-end on SeaBIOS. UEFI installed-disk support must be tested separately
before it is documented as supported.

Minimal Router v0.3.3 remains a controlled Beta pilot, not an unattended
pfSense/OpenWrt replacement.

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

## Full-installer compatibility and interrupted installations

The current updater/firstboot changes require the newly built full distribution
installer on published v0.1.7 and earlier appliances. A production distribution
must be signed and contain `firmware-signing.pub` and `release-manifest.json`.
The installed key is preserved, lower versions are refused, and a same-version
verified reinstall can repair the installation. Local unsigned development builds
are explicit root-operated test installs; they do not become signed Golden releases.

Use Alpine's `dnsmasq-dnssec-nftset` variant. Offline installation requires it to
be installed already with the `nftset` option reported by `dnsmasq --version`.
The plain variant reporting `no-nftset` cannot provide DNS filter profiles and is
refused; do not weaken configuration preflight to work around this dependency.

A full install resets the application rollback generation because bootstrap and
OS integration changes cannot be undone by switching an A/B slot. Keep the normal
operator backup/recovery route. If interrupted, local console recovery remains
available but daemon startup is fenced until the full installer completes. Rerun
the complete installer with the same or newer authorized payload; do not manually
remove `installation.json`. An interrupted offline migration likewise requires
completion through its local recovery command before runtime can start.

See [Web update trust and compatibility](WEB-UPDATE.md) and the required
[Golden ISO evidence](GOLDEN-IMAGE.md). A successful local distribution install
is not evidence that the newly built signed Golden ISO passed its full CI gates.

## Restoring your own settings onto a fresh install

A fresh Minimal Router install is deliberately empty. PPPoE, WireGuard, DDNS,
the proxy, QoS, Wi-Fi and per-device accounting are all switched off, there are
no peers, port forwards, firewall rules or DHCP reservations, and no secret is
stored anywhere. That is what makes a new appliance safe to boot: nothing is
half-configured, and nothing is enabled that would fail without a credential
you have not supplied yet.

The cost of that is retyping your own setup after a reinstall or a lab rebuild.
[`scripts/apply-config.sh`](../scripts/apply-config.sh) exists so you do not
have to.

### Where your settings live

**Not in this repository.** A settings file holds PPPoE credentials, WireGuard
private keys, a DDNS token and the proxy password. Keep it in a private
location — a separate private repository, an encrypted volume, a password
manager attachment. `PRIVACY.md` and the release process both treat private
deployment material as something that stays outside the public tree.

If you keep it in a private git repository, make sure the file is not
world-readable on disk and that the repository is actually private. A leaked
WireGuard private key means anyone holding it can join your tunnel.

### Writing the settings file

The file is JSON and only needs the sections you want to set. It is merged
recursively over whatever the appliance currently has, so anything you leave
out keeps the appliance's value.

```json
{
  "wan": {
    "interface": "eth0",
    "enabled": true,
    "username": "your-isp-login",
    "password": "your-isp-password",
    "mtu": 1492
  },
  "lan": {
    "interface": "eth1",
    "ip_address": "192.168.1.1",
    "netmask": "255.255.255.0",
    "cidr": "192.168.1.1/24"
  },
  "dhcp": {
    "enabled": true,
    "dns_enabled": true,
    "range_start": "192.168.1.100",
    "range_end": "192.168.1.200",
    "lease_time": "12h",
    "dns_servers": ["1.1.1.1", "9.9.9.9"],
    "static_leases": [
      { "id": "nas", "hostname": "home-nas", "mac": "02:b7:50:2e:81:54", "ip_address": "192.168.1.30" }
    ]
  },
  "dns": {
    "records": [{ "name": "nas.lan", "ip": "192.168.1.30" }]
  },
  "wireguard": {
    "enabled": true,
    "interface": "wg0",
    "listen_port": 51820,
    "address": "10.8.0.1/24",
    "private_key": "your-server-private-key",
    "peers": [
      {
        "id": "phone",
        "name": "Phone",
        "public_key": "peer-public-key",
        "allowed_ips": ["10.8.0.2/32"],
        "enabled": true
      }
    ]
  }
}
```

Sections you do not want stay absent. To leave WireGuard off entirely, simply
do not mention it.

### Applying it

```sh
MINIMALROUTER_PASSWORD='your-dashboard-password' \
  scripts/apply-config.sh \
    --host https://192.168.1.1:8443 \
    --config ~/minimalrouterhome/my-router.json \
    --insecure
```

`--insecure` is for the appliance's self-signed certificate on first contact;
drop it once you trust the certificate. If two-factor authentication is on, set
`MINIMALROUTER_TOTP` as well.

Check what would be written before writing it:

```sh
scripts/apply-config.sh --config my-router.json --dry-run
```

The script signs in, reads the live configuration, merges your file over it and
applies the result. When the change touches WAN or LAN the appliance applies it
provisionally and waits for the operator to confirm they can still reach it;
the script has just proved that it can, so it confirms automatically. Without
that confirmation the appliance would roll the change back.

`curl` and `jq` must be available on the machine running the script.

### What it does not do

It does not set the dashboard password, the recovery root password or SSH host
keys. Those are firstboot's job and are deliberately not scriptable from a file
that sits in a repository.

It also does not replace a backup. `docs/RECOVERY.md` covers the encrypted
backup and restore path, which captures the appliance's full canonical state
including material this file has no business holding.
