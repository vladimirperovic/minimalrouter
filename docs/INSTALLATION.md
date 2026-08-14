# Installation

Minimal Router OS is early alpha. Install only on a lab VM or controlled pilot
with console access and a known-good router ready for rollback.

For Proxmox-specific preparation see [`PROXMOX.md`](PROXMOX.md).

## Requirements

- Alpine Linux 3.22
- AMD64 or ARM64
- one WAN and one LAN interface
- local console access
- working PPPoE kernel support when PPPoE is used

For the validated Proxmox path use Alpine `linux-lts` and verify:

```sh
modprobe pppoe
```

If that fails, stop before installing.

A practical VM starting point is 1 vCPU, 1 GiB RAM and an 8 GiB disk.

## Build

On a trusted development machine with Go from `go.mod`, Node.js 22 and pnpm:

```sh
git clone https://github.com/vladimirperovic/minimalrouter.git
cd minimalrouter
pnpm --dir web install --frozen-lockfile
make dist-amd64
```

For ARM64 use `make dist-arm64`.

Verify the archive before transfer:

```sh
cd build
sha256sum -c minimalrouter-linux-amd64.tar.gz.sha256
```

On macOS use `shasum -a 256 -c ...`.

## Prepare Alpine

Confirm the guest and identify WAN/LAN by topology rather than interface number:

```sh
cat /etc/alpine-release
uname -a
modprobe pppoe
ip -brief link
ip -brief address
```

Do not place the candidate DHCP server on the same broadcast domain as the
production router during initial setup.

### Interface ownership

MinimalRouter owns every WAN, LAN and tunnel interface. A stock `setup-alpine`
host still carries `iface eth0 inet dhcp` in `/etc/network/interfaces`, which
competes with pppd for the WAN, delays boot while `need net` waits for a DHCP
lease that should never be requested, and can re-run `ifup` against an address
`router-applyd` has already installed.

The installer now rewrites that file, declaring every physical interface
`manual` and keeping only loopback automatic. The previous contents are saved to
`/etc/network/interfaces.minimalrouter-backup`. Cloud images additionally have
`cloud-init` removed from the default runlevel, because it re-applies its own
network configuration on every boot.

Confirm after installing:

```sh
cat /etc/network/interfaces
rc-update show default
ip -brief address
```

`eth0` should carry no address until PPPoE is configured, and there should be no
second default route.

## Install

Transfer the archive and checksum over a trusted path, verify the checksum again,
then extract it:

```sh
mkdir -p /tmp/minimalrouter-install
tar xzf minimalrouter-linux-amd64.tar.gz -C /tmp/minimalrouter-install
cd /tmp/minimalrouter-install/minimalrouter-linux-amd64
```

### Normal install

```sh
sudo sh install.sh
```

Normal mode configures Alpine 3.22 repositories when needed and runs `apk update`
and `apk add` for required dependencies.

### Offline / pre-provisioned install

```sh
sudo sh install.sh --offline
```

Offline mode is for an already provisioned, air-gapped node. It:

- does not run `apk update` or `apk add`;
- does not modify Alpine repository configuration;
- checks every required package locally with `apk info -e`;
- aborts if anything is missing.

The required package set is maintained in
`packaging/alpine/install-dist.sh` so documentation does not duplicate it.

`MINIMALROUTER_OFFLINE=1 sudo sh install.sh` is also accepted.

## Start and verify

```sh
rc-service chronyd start
rc-service router-applyd start
rc-service routerd start

rc-service router-applyd status
rc-service routerd status
rc-update show | grep -E 'routerd|router-applyd'
modprobe pppoe
```

Then connect a client only to the isolated LAN and open the HTTPS management
address shown by the installer. Complete the first-run setup with test
credentials.

During the first pilot verify:

- correct WAN/LAN interfaces;
- DHCP and DNS from the isolated LAN;
- outbound Internet connectivity;
- no direct dashboard access from WAN;
- logout/login behavior;
- rollback after an unconfirmed disruptive change;
- console recovery access.

## Trusted management networks

`trusted_networks` controls which source networks may reach the MinimalRouter
administrative Web UI/API. It does not replace authentication: the operator
password still applies, and both layers are enforced on every request. The
check uses the real TCP peer address of the connection and never trusts
forwarded headers. Loopback (`127.0.0.1`, `::1`) is always reachable so console
recovery keeps working.

The default is `192.168.1.0/24`. An empty list denies all remote access (only
local console access remains), and a configuration change that would remove the
caller's own source network is rejected to prevent operator lockout.

To later add a dedicated recovery subnet such as `10.255.255.0/24`, add it to
`trusted_networks` — no code changes are required.

## Dynamic DNS and WireGuard

New configurations default to No-IP DDNS; Cloudflare remains supported. Configure
DDNS only after the base WAN is stable and verify the hostname from an external
resolver.

See [`DYNAMIC_DNS.md`](DYNAMIC_DNS.md) and [`RECOVERY.md`](RECOVERY.md).

## Before any real ISP cutover

Keep the existing router and an out-of-band rollback path ready. A successful
basic test is not a production-readiness claim; repeated PPPoE/reboot recovery,
No-IP propagation, backup restore, external scans, destructive fault tests and a
longer soak are still release gates.
