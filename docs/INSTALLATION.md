# Controlled lab installation

Minimal Router OS is currently an **early-alpha lab appliance**. There is no
signed stable ISO or unattended production installation path. Keep console
access and a known-good router available throughout the test.

This guide covers the self-contained Alpine Linux distribution archive produced
from the source tree. Proxmox-specific preparation is documented in
[`PROXMOX.md`](PROXMOX.md).

## Supported test target

The currently documented test target is:

- Alpine Linux 3.22;
- x86-64 (`amd64`) or ARM64 where the required networking packages and kernel
  modules are available;
- on the validated Proxmox PPPoE path, **Alpine `linux-lts`**;
- one WAN and one LAN interface;
- a VM or dedicated test machine with local console access;
- an isolated LAN that is not bridged to the normal household or office LAN.

For a VM, 1 vCPU, 1 GiB RAM, an 8 GiB disk and two VirtIO NICs are a practical
starting point. The 2026-08-01 owner-Proxmox pilot observed approximately 73 MB
RAM after a clean `linux-lts` boot and 172 MB after the exercised workload, but
those values are observations rather than sizing guarantees.

## Why the PPPoE kernel preflight matters

The first real Proxmox PPPoE pilot initially used Alpine `linux-virt`. That
running kernel did not provide the PPPoE module needed by the appliance. After
switching to `linux-lts`, PPPoE support was available and the real WAN test
succeeded.

Before installation, verify the running kernel can load PPPoE support:

```sh
uname -a
modprobe pppoe
```

If `modprobe pppoe` fails, stop. On the validated Proxmox path install/boot
`linux-lts`, reboot into it, and repeat the check. The source and distribution
installers perform the same capability check and fail closed instead of leaving
an apparently installed router that cannot establish the intended PPPoE WAN.

## Safety checklist

Before installation:

- keep the existing router connected and ready for rollback;
- connect the candidate WAN to a test/NAT network before a real ISP cutover;
- connect the candidate LAN to an isolated bridge or switch;
- do not reuse production PPPoE, No-IP/Cloudflare, Wi-Fi, proxy or WireGuard
  secrets during initial lab setup;
- record which virtual or physical interface is WAN and which is LAN;
- take a VM snapshot only when the guest filesystem is in a consistent state;
- never pipe a mutable download directly into a root shell.

## 1. Build the distribution archive

Build on a trusted Linux or macOS development computer with the Go version from
`go.mod`, Node.js 22 and pnpm installed.

```sh
git clone https://github.com/vladimirperovic/minimalrouter.git
cd minimalrouter

git status --short
pnpm --dir web install --frozen-lockfile
make dist-amd64
```

For ARM64, use:

```sh
make dist-arm64
```

## 2. Verify the archive before transfer

On the build computer:

```sh
cd build
sha256sum -c minimalrouter-linux-amd64.tar.gz.sha256
```

On macOS:

```sh
cd build
shasum -a 256 -c minimalrouter-linux-amd64.tar.gz.sha256
```

Record the exact Git commit:

```sh
git rev-parse HEAD
```

## 3. Prepare Alpine Linux

Install a clean Alpine Linux 3.22 system. On Proxmox, boot `linux-lts` for the
validated PPPoE path. Confirm kernel support and both interfaces before running
the Minimal Router installer:

```sh
cat /etc/alpine-release
uname -a
modprobe pppoe
ip -brief link
ip -brief address
```

Do not guess interface roles from numbering alone. Hypervisors and hardware may
present interfaces in a different order after reboot.

## 4. Transfer and verify

Copy the archive and checksum to the Alpine machine over a trusted path. Verify
the checksum again on the target:

```sh
sha256sum -c minimalrouter-linux-amd64.tar.gz.sha256
```

A failed checksum is a hard stop.

## 5. Extract and install

```sh
mkdir -p /tmp/minimalrouter-install
tar xzf minimalrouter-linux-amd64.tar.gz -C /tmp/minimalrouter-install
cd /tmp/minimalrouter-install/minimalrouter-linux-amd64
sudo sh install.sh
```

The installer runs as root and supports Alpine Linux only. It installs required
packages, binaries, OpenRC services, web assets, sysctl/module configuration and
restrictive file ownership. It also loads every required router kernel module;
missing `pppoe` support aborts installation with the `linux-lts` guidance above.

Do not modify generated service files manually. Configuration changes must go
through the validated Minimal Router API and apply pipeline.

## 6. Verify services

```sh
rc-service router-applyd status
rc-service routerd status
rc-update show | grep -E 'routerd|router-applyd'
modprobe pppoe
```

Inspect only redacted logs when requesting help. Never publish credentials,
private keys, session values, real public addresses, private hostnames, MAC
addresses or device inventory.

## 7. Complete first-run setup

Attach a client only to the isolated LAN. Open the HTTPS management address shown
by the appliance or assigned to the selected LAN interface, then complete the
first-run wizard.

During the first trial:

- use test credentials;
- leave optional integrations disabled until base routing is verified;
- verify the selected WAN and LAN interfaces before applying;
- keep the console open during every disruptive network change;
- confirm that management is unavailable directly from WAN;
- confirm that a failed or unconfirmed disruptive change rolls back.

A locally generated device certificate may require an explicit browser trust
exception during lab testing. Do not replace HTTPS with plaintext management.

## 8. Dynamic DNS / No-IP

New configurations default to **No-IP** DDNS through Alpine `inadyn`. Cloudflare
remains available for compatibility. Prefer a scoped No-IP DDNS Key rather than
the main account password.

Do not assume DDNS is working merely because Internet access works. After the
base WAN is stable, configure No-IP in the **Dynamic DNS** dashboard and validate
it from an external resolver. The full provider and diagnostic contract is in
[`DYNAMIC_DNS.md`](DYNAMIC_DNS.md).

The 2026-08-01 pilot already demonstrated that a working externally resolved
hostname allowed a phone to establish WireGuard and open the dashboard; the
remaining test is to prove that the newly implemented **MinimalRouter-managed
No-IP updater** performs that update without a Proxmox-host workaround.

## 9. Basic validation

After setup, verify from an isolated LAN client:

- DHCP lease acquisition;
- DNS resolution;
- outbound IPv4 connectivity through the intended WAN;
- default-deny unsolicited WAN behavior;
- dashboard authentication and logout;
- backup export using a test password;
- reboot reconciliation;
- console access after a deliberately failed configuration change.

For a real-ISP pilot, additionally verify:

- PPPoE disconnect/reconnect and reboot recovery;
- MinimalRouter-managed No-IP update and external resolution;
- WireGuard recovery using the No-IP hostname;
- host-side/out-of-band rollback remains ready before each disruptive cutover.

Do not move the appliance into unattended production merely because these checks
pass. Repeated NIC/PPPoE behavior, sustained throughput and thermals, power-loss
recovery, external scanning, restore, signed recovery media and independent
security review remain release gates.

## Upgrade and removal

There is no stable in-place upgrade contract yet. Before testing a newer commit,
export an encrypted backup, record the current commit, and keep a full VM or disk
rollback path.

For a failed test, disconnect the candidate WAN and LAN, restore the known-good
router, and only then investigate. See [`TROUBLESHOOTING.md`](TROUBLESHOOTING.md)
and [`SUPPORT.md`](../SUPPORT.md).
