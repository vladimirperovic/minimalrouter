# Minimal Router OS — all-in-one ISO installation

This is the preferred AMD64/x86-64 installation path. The ISO is a complete
router-appliance installer: Alpine Linux, the target `linux-lts` kernel,
MinimalRouter, Web Dashboard, router dependencies, OpenSSH recovery tools and
the offline packages required for disk installation are already included.
Alpine does **not** need to be installed separately.

The disk installation is designed to complete without Internet access. This is
important for a router installer because WAN/PPPoE may not be working yet.
Alpine's Extended 3.22.5 image is used as the base because Alpine documents the
Extended image as the offline path for a `setup-disk` system installation.

> Minimal Router OS is still a pilot. Test the ISO in an isolated Proxmox VM and
> keep the existing router available until WAN, LAN, DHCP, DNS and reboot have
> all been verified.

## The normal Proxmox setup

For the easiest first test, prepare one QEMU/KVM VM with:

- 1 vCPU or more;
- 1 GiB RAM or more;
- one virtual disk of at least 8 GiB;
- two VirtIO network adapters;
- the first adapter connected toward the modem/ONT (WAN);
- the second adapter connected to an isolated/private client bridge (LAN);
- the MinimalRouter ISO attached as CD/DVD;
- Proxmox console access available during the first install.

Do not use an LXC container. Do not connect the new test LAN to another active
DHCP server during the first install.

For this ordinary **one-disk Proxmox/QEMU VM** you should not need to choose or
confirm the disk manually. MinimalRouter detects the virtual-machine environment
and can automatically use the single clearly virtual disk.

The automatic erase path is deliberately conservative. A disk is auto-selected
only when all of these are true:

1. QEMU/KVM/Proxmox is detected;
2. exactly one candidate installation disk exists;
3. the disk is non-removable;
4. it is clearly VirtIO/QEMU virtual storage.

A raw physical disk passed through to a VM does not qualify just because the VM
itself is running under QEMU. Ambiguous, multi-disk or non-VM layouts fall back
to explicit disk selection and a safety confirmation.

## What happens when the ISO boots

The normal VGA/noVNC path starts automatically. There is no separate
"Press Enter to start the installer" screen.

The first application screen is the MinimalRouter ASCII logo, version and a
short explanation of what the installer is about to ask. The wizard is meant to
be usable without being a network administrator.

A simple rule applies throughout the wizard:

- **`[Y/n]`** means Yes is already selected; press Enter to accept it;
- an empty PPPoE username means "configure PPPoE later";
- passwords must actually be entered and are not echoed;
- if WAN/LAN detection is clear, press Enter to accept the suggestion;
- if detection is ambiguous, the installer asks you which interface to use;
- destructive disk confirmation appears only when the layout is not safe enough
  for automatic VM-disk selection.

## Questions the installer asks

The normal flow is intentionally short:

1. **PPPoE username** — press Enter to skip if you do not want to configure it
   now. If a username is entered, the PPPoE password is requested.
2. **WAN/LAN roles** — the installer probes interfaces and proposes the likely
   WAN and LAN. Press Enter when the proposal is correct.
3. **Web Dashboard administrator password** — required, at least 12 characters.
4. **Review** — shows only non-secret values. Press Enter on `[Y/n]` to accept.
5. **Recovery / SSH root password** — separate from the Dashboard password.
6. **Disk installation** — automatic for a normal one-disk Proxmox/QEMU VM;
   explicit confirmation only for ambiguous layouts.
7. Alpine Linux + MinimalRouter are installed, then the VM reboots from disk.

The live ISO does not become the production router. It records the reviewed
configuration, installs the target system, and the installed system reconciles
WAN/LAN/PPPoE on its own kernel after reboot.

## WAN and LAN defaults

The management LAN starts at:

```text
192.168.1.1/24
```

The Web Dashboard after installation is expected at:

```text
https://192.168.1.1:8443
```

The first-run wizard tries to identify the WAN by PPPoE response and hardware
/link information. With two adapters it proposes WAN/LAN roles and asks for a
simple confirmation instead of requiring interface names from the user.

## SSH recovery

OpenSSH is included in the ISO and installed system.

After the recovery/root password is set during installation, the selected LAN
is assigned `192.168.1.1/24` and live recovery SSH is started:

```sh
ssh root@192.168.1.1
```

Use the **Recovery / SSH root password**, not the Dashboard password.

Live SSH listens on the selected trusted LAN address, not on WAN. After the
installed system boots, the generated firewall allows TCP/22 only from an
allowed management LAN or authenticated WireGuard management interface. No WAN
SSH accept rule is added.

This is intentionally enabled for the current pilot so a failed installation
can be diagnosed without typing long commands in noVNC.

## Serial console

MinimalRouter provides a `ttyS0` recovery path at:

```text
115200 baud, 8 data bits, no parity, 1 stop bit
```

The ISO contains a dedicated serial installer boot entry in addition to the
normal VGA/noVNC entry. The serial installer owns `ttyS0` directly; it does not
start another getty on the same port while the wizard is running.

After disk installation, `ttyS0` is configured as a password-protected root
recovery login and the serial kernel/bootloader settings are persisted for both
ExtLinux and GRUB paths where applicable.

For a Proxmox serial test, add a serial port to the VM (normally `serial0`,
socket) and use the serial console at 115200. Keep noVNC available during the
pilot as an independent recovery path.

## Clean noVNC console

Kernel messages are deliberately kept out of the interactive installer TTY so
they do not overwrite the question currently being typed. They remain available
for diagnosis with:

```sh
dmesg
```

This specifically prevents messages with kernel timestamps such as
`[154.452236] ...` from appearing in the middle of a password or setup prompt.

## Building a new ISO

From the repository root on a trusted build machine:

```sh
pnpm --dir web install --frozen-lockfile
make iso
```

The builder needs Go from `go.mod`, Node.js 22, pnpm, `xorriso`, `curl`, `tar`,
a SHA-256 utility and either Docker or Alpine `apk` for resolving the signed
offline package bundle.

Outputs are written under `build/iso/`:

```text
minimalrouter-<version>-amd64.iso
minimalrouter-<version>-amd64.iso.sha256
APK-SHA256SUMS
```

Verify the image before use:

```sh
sha256sum -c build/iso/minimalrouter-*-amd64.iso.sha256
```

GitHub Actions also builds the ISO, boots it in QEMU, drives the real serial
wizard, performs a complete offline installation to a blank virtual disk,
reboots from that disk and checks the installed appliance. The workflow also
performs real password-authenticated SSH logins during the live install and
after the installed system boots.

When the **Appliance ISO** check is green, the
`minimalrouter-appliance-iso-<commit>` artifact is preferred for testing because
it is exactly the image produced by that tested commit.

## First verification after reboot

Expected management endpoints:

```text
Web Dashboard: https://192.168.1.1:8443
SSH:           root@192.168.1.1
Serial:        ttyS0 @ 115200
```

Useful checks from console or SSH:

```sh
ip -brief link
ip -brief address
ip route
rc-service sshd status
rc-service router-applyd status
rc-service routerd status
ss -lntp | grep ':22'
nft list ruleset
cat /proc/cmdline
cat /etc/apk/repositories
```

Before replacing the old router, confirm that:

- WAN and LAN are the intended interfaces;
- LAN is `192.168.1.1/24`;
- Dashboard opens at `https://192.168.1.1:8443`;
- SSH works from the trusted LAN and is not exposed on WAN;
- DHCP and DNS work for a LAN client;
- PPPoE establishes when configured;
- Internet routing works;
- a normal reboot returns to the same working state;
- serial recovery login appears on `ttyS0` at 115200.

## If installation fails

A fatal installer step opens a local recovery shell. After the recovery password
stage, SSH should also be available from the selected LAN.

Useful diagnostics:

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
dmesg | tail -100
```

Do not manually replace `/etc/apk/repositories` with a guessed `/media/cdrom`
path. The installer is expected to discover the mounted ISO repository itself.
If it cannot, capture the diagnostics above so the installer can be corrected
rather than worked around by hand.
