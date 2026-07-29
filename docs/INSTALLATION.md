# Controlled lab installation

Minimal Router OS is currently an **early-alpha lab appliance**. There is no
signed stable ISO or unattended production installation path. Keep console
access and a known-good router available throughout the test.

This guide covers the self-contained Alpine Linux distribution archive produced
from the source tree. Proxmox-specific preparation is documented in
[PROXMOX.md](PROXMOX.md).

## Supported test target

The currently documented test target is:

- Alpine Linux 3.22;
- x86-64 (`amd64`) or ARM64 where the required networking packages and drivers
  are available;
- one WAN and one LAN interface;
- a VM or dedicated test machine with local console access;
- an isolated LAN that is not bridged to the normal household or office LAN.

For a VM, 1 vCPU, 1 GiB RAM, an 8 GiB disk, and two VirtIO NICs are a practical
starting point. The measured minimum is not a production recommendation.

## Safety checklist

Before installation:

- keep the existing router connected and ready for rollback;
- connect the candidate WAN to a test/NAT network, not directly to the ISP;
- connect the candidate LAN to an isolated bridge or switch;
- do not reuse production PPPoE, Cloudflare, Wi-Fi, proxy, or WireGuard secrets;
- record which virtual or physical interface is WAN and which is LAN;
- take a VM snapshot only when the guest filesystem is in a consistent state;
- never pipe a mutable download directly into a root shell.

## 1. Build the distribution archive

Build on a trusted Linux or macOS development computer with the Go version from
`go.mod`, Node.js 22, and pnpm installed.

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

The build produces an archive and SHA-256 checksum under `build/`.

## 2. Verify the archive before transfer

On the build computer:

```sh
cd build
sha256sum -c minimalrouter-linux-amd64.tar.gz.sha256
```

On macOS, use:

```sh
cd build
shasum -a 256 -c minimalrouter-linux-amd64.tar.gz.sha256
```

Record the exact Git commit used for the build:

```sh
git rev-parse HEAD
```

## 3. Prepare Alpine Linux

Install a clean Alpine Linux 3.22 system. Confirm that both interfaces are
visible before running the Minimal Router installer:

```sh
cat /etc/alpine-release
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

A failed checksum is a hard stop. Delete the archive and investigate the build
or transfer path.

## 5. Extract and install

```sh
mkdir -p /tmp/minimalrouter-install
tar xzf minimalrouter-linux-amd64.tar.gz -C /tmp/minimalrouter-install
cd /tmp/minimalrouter-install/minimalrouter-linux-amd64
sudo sh install.sh
```

The installer must run as root and supports Alpine Linux only. It installs the
application binaries, OpenRC services, web assets, required packages, sysctl and
module configuration, and restrictive file ownership.

Do not modify the generated service files manually. Configuration changes must
go through the validated Minimal Router API and apply pipeline.

## 6. Verify services

```sh
rc-service router-applyd status
rc-service routerd status
rc-update show | grep -E 'routerd|router-applyd'
```

Inspect only redacted logs when requesting help. Never publish credentials,
private keys, session values, real public addresses, hostnames, MAC addresses,
or device inventory.

## 7. Complete first-run setup

Attach a client only to the isolated LAN. Open the HTTPS management address shown
by the appliance or assigned to the selected LAN interface, then complete the
first-run wizard.

During the first trial:

- use test credentials;
- leave optional integrations disabled;
- verify the selected WAN and LAN interfaces before applying;
- keep the console open during every disruptive network change;
- confirm that management is unavailable directly from WAN;
- confirm that a failed or unconfirmed disruptive change rolls back.

A locally generated device certificate may require an explicit browser trust
exception during lab testing. Do not replace HTTPS with plaintext management.

## 8. Basic validation

After setup, verify from an isolated LAN client:

- DHCP lease acquisition;
- DNS resolution;
- outbound IPv4 connectivity through the intended test WAN;
- default-deny unsolicited WAN behavior;
- dashboard authentication and logout;
- backup export using a test password;
- reboot reconciliation;
- console access after a deliberately failed configuration change.

Do not move the appliance into the production network merely because these checks
pass. Physical NIC behavior, real PPPoE, sustained throughput, power-loss
recovery, external scanning, signed recovery media, and independent security
review remain release gates.

## Upgrade and removal

There is no stable in-place upgrade contract yet. Before testing a newer commit,
export an encrypted backup, record the current commit, and keep a full VM or disk
rollback path.

For a failed test, disconnect the candidate WAN and LAN, restore the known-good
router, and only then investigate. See [TROUBLESHOOTING.md](TROUBLESHOOTING.md)
and [SUPPORT.md](../SUPPORT.md).