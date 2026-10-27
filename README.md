# Minimal Router OS

<p align="center">
  <img src="web/public/favicon.svg" alt="Minimal Router OS" width="130" />
</p>

<p align="center">
  <a href="#status"><img alt="Status: Beta" src="https://img.shields.io/badge/status-beta-blue" /></a>
  <a href="https://github.com/vladimirperovic/minimalrouter/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/vladimirperovic/minimalrouter/actions/workflows/ci.yml/badge.svg" /></a>
  <a href="https://github.com/vladimirperovic/minimalrouter/actions/workflows/codeql.yml"><img alt="CodeQL" src="https://github.com/vladimirperovic/minimalrouter/actions/workflows/codeql.yml/badge.svg" /></a>
  <a href="https://github.com/vladimirperovic/minimalrouter/releases/tag/v0.3.3"><img alt="Beta release: v0.3.3" src="https://img.shields.io/badge/beta-v0.3.3-6b7280" /></a>
  <a href="LICENSE"><img alt="MIT License" src="https://img.shields.io/badge/license-MIT-blue.svg" /></a>
</p>

<p align="center">
  <a href="docs/INSTALLATION.md">Installation</a> ·
  <a href="docs/GOLDEN-IMAGE.md">Golden image design</a> ·
  <a href="docs/CURRENT_VALIDATION.md">Validation</a> ·
  <a href="https://vladimirperovic.github.io/minimalrouter/">Live dashboard demo</a> ·
  <a href="docs/README.md">Docs</a> ·
  <a href="SECURITY.md">Security</a>
</p>

<p align="center">
  <strong><a href="https://vladimirperovic.github.io/minimalrouter/">Try the interactive dashboard</a></strong><br />
  Browser-only demo with synthetic data; no sign-in or router connection required.
</p>

> The public demo runs entirely in the browser with synthetic documentation data.
> It does not connect to a router, expose a management API, or contain real
> credentials, addresses, or device information.

## A home router you can actually change

Minimal Router is a focused Alpine Linux router appliance with a small Rust control
plane and React dashboard. It drives standard Linux networking components directly:
`nftables`, `pppd`, `dnsmasq`, WireGuard and `inadyn`.

The project intentionally avoids a plugin ecosystem and a second configuration
language. The goal is a router small enough to understand, test and safely adapt —
including with an AI coding agent — while keeping privileged operations typed,
recoverable and fail-closed.

<a id="status"></a>

> **Beta — v0.3.3.** The preferred AMD64/Proxmox installation path is the
> **Golden Appliance ISO**. Alpine Linux, the matching `linux-lts` kernel and
> modules, MinimalRouter, Dashboard and runtime packages are built in CI before
> the user VM boots. The ISO verifies and flashes that prebuilt image, reboots,
> then runs a short first-boot router configuration. v0.3.3 closes a gap in the
> **Encrypted DNS, VPN & proxy bypass** DNS filter category introduced in v0.3.2:
> Apple devices that had cached the iCloud Private Relay alias kept reaching the
> relay through its CNAME target, so Safari Private Browsing could still resolve
> blocked names through Apple's Oblivious DoH. The category now also blocks that
> target and Firefox's DoH canary domain. The bootstrap tools are byte-identical
> to v0.3.1, so v0.3.1 and v0.3.2 appliances take v0.3.3 as an ordinary signed
> dashboard update; v0.3.0 and earlier appliances need the signed full
> distribution installer once. This is still a controlled-pilot Beta, not an
> unattended pfSense/OpenWrt replacement. See
> [`docs/CURRENT_VALIDATION.md`](docs/CURRENT_VALIDATION.md).

## v0.3.3 quick start — Proxmox

Download these assets from the **v0.3.3 GitHub release**:

```text
minimalrouter-0.3.3-amd64.iso
minimalrouter-0.3.3-amd64.iso.sha256
```

Verify before attaching the ISO:

```sh
sha256sum -c minimalrouter-0.3.3-amd64.iso.sha256
```

Create a QEMU/KVM VM with the currently proven target profile:

- SeaBIOS;
- x86-64 / AMD64;
- 1 vCPU or more;
- 1 GiB RAM or more;
- one VirtIO disk of at least 8 GiB;
- two VirtIO NICs: WAN and isolated LAN;
- local noVNC console, with optional `ttyS0` serial recovery.

Attach the ISO and boot. The default path uses VGA/noVNC. A separate
**MinimalRouter Installer (serial ttyS0 115200)** entry drives the complete
installer over serial.

The install has two stages:

1. the live ISO verifies `golden.img.gz`, safely selects the target VM disk,
   copies the already-built appliance byte-for-byte and reboots;
2. the installed appliance asks for WAN/LAN roles, optional PPPoE, Dashboard
   password and a separate recovery/SSH root password.

After first boot:

```text
Dashboard: https://192.168.1.1:8443
SSH:       root@192.168.1.1
Serial:    ttyS0 @ 115200
```

Full instructions, including the Proxmox VM profile:
[`docs/INSTALLATION.md`](docs/INSTALLATION.md).

> The installer ISO contains BIOS and UEFI boot metadata, but the v0.3.3
> **installed Golden target** that is fully exercised end-to-end is the
> SeaBIOS/MBR path. Do not claim UEFI installed-disk qualification yet.

## If the ISO gives you trouble: build it yourself onto an Alpine VM

Attaching and booting an ISO is the part of this that most often goes wrong —
upload limits, storage that will not take an ISO, a hypervisor that boots the
wrong device, a console you cannot reach. If you hit that wall, you do not have
to fight it. There is a second, fully supported path: build the distribution
archive from this repository and install it on an ordinary Alpine VM you create
yourself.

This is a good task to hand to an AI coding agent. It is mechanical, every step
is a documented command, and the agent can read the repository while it works.
Paste something like this:

```text
Clone https://github.com/vladimirperovic/minimalrouter and read
docs/INSTALLATION.md.

1. Build the x86-64 distribution archive from source with `make dist-amd64`.
2. Walk me through creating the Proxmox VM: SeaBIOS, 1+ vCPU, 1+ GiB RAM, one
   VirtIO disk of 8 GiB or more, two VirtIO NICs (WAN and an isolated LAN).
3. I will install Alpine Linux 3.24 with the linux-lts kernel on it.
4. Copy build/minimalrouter-linux-amd64.tar.gz to the VM, extract it, and run
   `sudo sh install.sh`.
5. Verify the result using the checks in docs/INSTALLATION.md.
```

Building needs Rust 1.99.1 (with zig and cargo-zigbuild for the static musl targets) and Node 22.13+ with pnpm; the VM needs Alpine 3.24 with
a kernel that has the PPPoE module. `make dist-amd64` writes
`build/minimalrouter-linux-amd64.tar.gz`, the same archive layout the release
publishes, including its `install.sh`.

Two honest caveats. An archive you build locally is **not signed**, so it does
not carry the Ed25519 manifest the release archives use and it does not install
the firmware-update trust anchor the way the release ISO does; see
[`docs/RELEASE_SECURITY.md`](docs/RELEASE_SECURITY.md). And the Golden ISO is
the path that CI installs and cold-boots end-to-end on every release, so it
remains the qualified one. Treat this route as the practical fallback, not as an
equally validated install.

Details: [`docs/INSTALLATION.md`](docs/INSTALLATION.md) (see *Alternative:
signed distribution archive on an existing Alpine system*) and
[`docs/DEVELOPMENT.md`](docs/DEVELOPMENT.md).

## What it does

- PPPoE WAN, DHCP/DNS, NAT and a default-deny firewall
- WireGuard remote access with peer provisioning
- Dynamic DNS via No-IP or Cloudflare
- gateway latency, loss and reconnect monitoring with live bandwidth
- connected-device list, DHCP reservations and Wake-on-LAN
- per-device Internet pause controls and monthly traffic accounting
- DNS filtering with per-device schedules
- optional non-caching Squid proxy
- QoS/SQM shaping with CAKE or fq_codel
- optional Wi-Fi access point on supported hardware
- transactional configuration with confirmation, rollback and recovery
- encrypted backups, snapshots and crash-safe A/B updates
- signed updates installed from the dashboard when a new release appears
- local console, trusted-LAN SSH and serial recovery paths

Deliberately **not** included: multi-WAN, BGP/OSPF, IDS/IPS, captive portals,
HA failover or a general plugin system.

## Manual/archive installation

The signed AMD64 and ARM64 distribution archives remain available for advanced
operators who deliberately install onto an existing Alpine 3.22 or 3.24 system. That is
no longer the preferred Proxmox first-install path.

See [`docs/INSTALLATION.md`](docs/INSTALLATION.md).

## Working with an AI agent

Before changing privileged networking or installer code, point the agent at:

- [`AGENTS.md`](AGENTS.md)
- [`ARCHITECTURE.md`](ARCHITECTURE.md)
- [`DESIGN.md`](DESIGN.md)
- [`SECURITY.md`](SECURITY.md)
- [`docs/GOLDEN-IMAGE.md`](docs/GOLDEN-IMAGE.md) — mandatory before changing ISO/installer code
- `rust/crates/mr-config/src/config/validation.rs`
- `rust/crates/mr-services/src/services/nftables.rs`

The Golden ISO rule is intentionally strict: **the user VM is a flasher target,
not an Alpine build host**. Do not reintroduce live `apk`, `setup-disk`, `mkinitfs`,
target chroots or application installation into the live flasher just to make a
single environment work.

## Development

Requirements: Rust 1.99.1 (`rustup`), Node.js 22 and pnpm.

```sh
make test
make vet
pnpm --dir web install --frozen-lockfile
pnpm --dir web lint
pnpm --dir web test
pnpm --dir web build
pnpm --dir web test:e2e
```

Build the Golden ISO on a trusted Linux builder with Docker and the documented ISO
tools:

```sh
make iso
```

The `Appliance ISO` workflow additionally boots the production ISO, flashes a
blank 8 GiB QEMU disk, completes firstboot over `ttyS0`, performs a real SSH
login and verifies the installed appliance.

## Documentation

- [`docs/README.md`](docs/README.md) — documentation index
- [`docs/INSTALLATION.md`](docs/INSTALLATION.md) — Golden ISO install, Proxmox VM profile, archive install, restoring settings
- [`docs/GOLDEN-IMAGE.md`](docs/GOLDEN-IMAGE.md) — exact ISO architecture and rebuild rules
- [`docs/CURRENT_VALIDATION.md`](docs/CURRENT_VALIDATION.md) — what is actually proven
- [`docs/RELEASE_SECURITY.md`](docs/RELEASE_SECURITY.md) — signed release and verification model
- [`docs/RECOVERY.md`](docs/RECOVERY.md) — recovery and rollback
- [`ARCHITECTURE.md`](ARCHITECTURE.md) — application architecture

## Security and privacy

A router is a security boundary. Read [`SECURITY.md`](SECURITY.md) before changing
privileged code, and report vulnerabilities privately.

Never commit real credentials, private keys, backups, databases, packet captures,
public addresses, private hostnames, MAC addresses or a household device inventory.
See [`PRIVACY.md`](PRIVACY.md).

## License

MIT — see [`LICENSE`](LICENSE).
