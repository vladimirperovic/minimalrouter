# Minimal Router OS

<p align="center">
  <img src="web/public/favicon.svg" alt="Minimal Router OS" width="130" />
</p>

<p align="center">
  <a href="#status"><img alt="Status: Beta" src="https://img.shields.io/badge/status-beta-blue" /></a>
  <a href="https://github.com/vladimirperovic/minimalrouter/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/vladimirperovic/minimalrouter/actions/workflows/ci.yml/badge.svg" /></a>
  <a href="https://github.com/vladimirperovic/minimalrouter/actions/workflows/codeql.yml"><img alt="CodeQL" src="https://github.com/vladimirperovic/minimalrouter/actions/workflows/codeql.yml/badge.svg" /></a>
  <a href="https://github.com/vladimirperovic/minimalrouter/releases/tag/v0.1.6"><img alt="Beta release: v0.1.6" src="https://img.shields.io/badge/beta-v0.1.6-6b7280" /></a>
  <a href="LICENSE"><img alt="MIT License" src="https://img.shields.io/badge/license-MIT-blue.svg" /></a>
</p>

<p align="center">
  <a href="docs/ISO_INSTALLATION.md">Golden ISO install</a> ·
  <a href="docs/PROXMOX.md">Proxmox</a> ·
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

Minimal Router is a focused Alpine Linux router appliance with a small Go control
plane and React dashboard. It drives standard Linux networking components directly:
`nftables`, `pppd`, `dnsmasq`, WireGuard and `inadyn`.

The project intentionally avoids a plugin ecosystem and a second configuration
language. The goal is a router small enough to understand, test and safely adapt —
including with an AI coding agent — while keeping privileged operations typed,
recoverable and fail-closed.

<a id="status"></a>

> **Beta — v0.1.6.** The preferred AMD64/Proxmox installation path is the
> **Golden Appliance ISO**. Alpine Linux, the matching `linux-lts` kernel and
> modules, MinimalRouter, Dashboard and runtime packages are built in CI before
> the user VM boots. The ISO verifies and flashes that prebuilt image, reboots,
> then runs a short first-boot router configuration. v0.1.6 also promotes the
> approved dashboard visual system to production, keeps the public demo aligned
> with the production UI, adds the pushed mobile navigation interaction and
> expands the release gate with cold-boot, supervision and installer-safety
> validation. This is still a controlled-pilot Beta, not an unattended
> pfSense/OpenWrt replacement. See
> [`docs/CURRENT_VALIDATION.md`](docs/CURRENT_VALIDATION.md).

## v0.1.6 quick start — Proxmox

Download these assets from the **v0.1.6 GitHub release**:

```text
minimalrouter-0.1.6-amd64.iso
minimalrouter-0.1.6-amd64.iso.sha256
```

Verify before attaching the ISO:

```sh
sha256sum -c minimalrouter-0.1.6-amd64.iso.sha256
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

Full instructions: [`docs/ISO_INSTALLATION.md`](docs/ISO_INSTALLATION.md) and
[`docs/PROXMOX.md`](docs/PROXMOX.md).

> The installer ISO contains BIOS and UEFI boot metadata, but the v0.1.6
> **installed Golden target** that is fully exercised end-to-end is the
> SeaBIOS/MBR path. Do not claim UEFI installed-disk qualification yet.

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
- local console, trusted-LAN SSH and serial recovery paths

Deliberately **not** included: multi-WAN, BGP/OSPF, IDS/IPS, captive portals,
HA failover or a general plugin system.

## Manual/archive installation

The signed AMD64 and ARM64 distribution archives remain available for advanced
operators who deliberately install onto an existing Alpine 3.22 system. That is
no longer the preferred Proxmox first-install path.

See [`docs/INSTALLATION.md`](docs/INSTALLATION.md).

## Working with an AI agent

Before changing privileged networking or installer code, point the agent at:

- [`AGENTS.md`](AGENTS.md)
- [`ARCHITECTURE.md`](ARCHITECTURE.md)
- [`DESIGN.md`](DESIGN.md)
- [`SECURITY.md`](SECURITY.md)
- [`docs/GOLDEN-IMAGE.md`](docs/GOLDEN-IMAGE.md) — mandatory before changing ISO/installer code
- `internal/config/validation.go`
- `internal/services/nftables.go`

The Golden ISO rule is intentionally strict: **the user VM is a flasher target,
not an Alpine build host**. Do not reintroduce live `apk`, `setup-disk`, `mkinitfs`,
target chroots or application installation into the live flasher just to make a
single environment work.

## Development

Requirements: Go from `go.mod`, Node.js 22 and pnpm.

```sh
go test -race ./...
go vet ./...
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
- [`docs/ISO_INSTALLATION.md`](docs/ISO_INSTALLATION.md) — preferred v0.1.6 ISO install
- [`docs/GOLDEN-IMAGE.md`](docs/GOLDEN-IMAGE.md) — exact ISO architecture and rebuild rules
- [`docs/PROXMOX.md`](docs/PROXMOX.md) — VM baseline and pilot procedure
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
