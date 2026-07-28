# Minimal Router OS

Minimal Router OS is an ultra-lightweight Alpine Linux appliance for home and
small-office routing. It combines proven Linux networking components with a Go control plane (`routerd` + `router-applyd`) and an Apple × Swiss minimalist web interface.

The product is intentionally narrow: quick installation, safe configuration,
automatic snapshots, reliable rollback, and a clean user experience. It is not
intended to replace pfSense or become a general-purpose networking platform.

## Project Status & Built Features

The core control-plane path is implemented and has been exercised in an
isolated Alpine Linux VM. This repository is suitable for a controlled pilot,
not yet an unattended replacement for a production firewall. Physical NIC,
real ISP PPPoE, imported site configuration, recovery media, and external
penetration tests remain release gates.

- **Unprivileged & Privileged Go Binaries** (`routerd` unprivileged management plane + `router-applyd` privileged Unix socket helper).
- **Service Configuration Generators**: Deterministic, privileged lifecycle
  paths for `nftables`, `pppd` (PPPoE), `dnsmasq` (DHCP/DNS), WireGuard,
  Squid, global DNS blocklisting, QoS, Cloudflare Dynamic DNS, and a Wi-Fi
  access point on compatible hardware. DoH, per-device DNS policy,
  Cloudflare Tunnel, and automatic updates fail closed until verified runtime
  adapters and a matching security policy exist.
- **Squid Proxy & Restricted IP Alias**: Non-caching HTTP/HTTPS forward proxy with NCSA basic authentication. Integrates with `nftables` to drop direct WAN internet traffic for restricted IP aliases while allowing browser proxy access on port `3128`.
- **Model Context Protocol (MCP) AI Agent Integration**: Local Go MCP bridge (`cmd/minimalrouter-mcp`) with a server-enforced read-only default. Explicit admin mode is required for supported mutations.
- **pfSense XML Importer**: Preview-first importer for selected settings. NAT rules are imported disabled because WireGuard is the only allowed WAN entry point.
- **First-Run Installation Wizard**: Guided 5-step Apple × Swiss setup wizard per `DESIGN.md §14`.
- **Live DHCP Devices**: The authenticated dashboard reads the bounded
  dnsmasq lease table from RAM and shows currently leased IPv4 clients without
  storing a separate device-history database.
- **Security Baseline**: Argon2id password hashing, 256-bit HttpOnly secure cookie sessions, optional TOTP, CSRF protection, rate limiting, secret redaction, and encrypted backup export.
- **Proxmox VE Support**: Manual Alpine VM lab deployment is documented. The
  automated ISO helper is retained for a future signed release and is not the
  current installation path.
- **Alpine Linux Packaging**: OpenRC init scripts, hardened sysctls, required
  kernel-module loading, and an appliance overlay builder. `make iso` prepares
  the overlay; an Alpine `mkimage` build host is still required to produce and
  sign bootable recovery media.

## Core Stack

- **OS**: Alpine Linux 3.22 on a standard root filesystem
- **Backend**: Go 1.25 REST API (`/api/v1`)
- **AI Agent Interface**: Model Context Protocol (MCP stdio over JSON-RPC 2.0)
- **Frontend**: React + TypeScript + Vite, compiled to static assets. Node.js
  is a development/build dependency only and is not installed on the router.
- **Integrations**: nftables, pppd, dnsmasq, WireGuard, Squid, global DNS
  blocklisting, QoS, Cloudflare DDNS through `inadyn`, and Wi-Fi AP through
  `hostapd` on an AP-capable radio. Cloudflare Tunnel, DoH, per-device DNS
  policy, and automatic updates are visibly unavailable and rejected by the
  backend.
- **Runtime shell**: BusyBox `ash`; Node.js, Bash, and `wg-quick` are absent.
  WireGuard is applied with fixed `wg`/`ip` argument arrays.
- **Store**: SQLite canonical state store with pre-apply sha256 checksummed snapshots

## Quick Start & Proxmox VE Setup

### 1. Proxmox VE VM preparation

No signed Minimal Router release ISO exists yet, so the automated ISO helper
is not the current installation path. For a lab trial, manually create an
Alpine Linux 3.22 x86_64 VM with 1 vCPU, 1 GiB RAM, an 8 GiB disk, and two
VirtIO NICs. Keep the WAN NIC on a test/NAT bridge and the LAN NIC on an
isolated bridge. Build `make dist-amd64` on a development computer, copy the
resulting archive into the VM, verify its checksum, and run its `install.sh`.
See the [Proxmox guide](docs/PROXMOX.md) for the exact sequence.

### 2. Connect your AI Agent via MCP

```bash
go build -o bin/minimalrouter-mcp ./cmd/minimalrouter-mcp
```
Add `minimalrouter-mcp` to your Claude Desktop / AI Agent configuration. See [MCP Server Guide](docs/MCP.md).

### 3. Run Backend Server (`routerd`) Locally

```bash
go run ./cmd/routerd
```

### 4. Run Web Dashboard (development only)

```sh
cd web
pnpm install
pnpm dev
```

Open the URL printed by Vite (normally `http://localhost:5173`). For VM tests, pre-built
`web/dist/` is used automatically.

### 5. Build the Alpine appliance overlay

```bash
make iso
```

The command does not itself emit a bootable ISO. Follow the printed `mkimage`
release-gate guidance: a reviewed Alpine profile, package repository, image
signature, and boot test are still required. The repository intentionally does
not print a command for a profile that does not yet exist.

## Documentation

- [AI Agent Integration (MCP Protocol)](docs/MCP.md)
- [AI continuation, macOS preview, and Alpine VM test guide](AI_HANDOFF.md)
- [Proxmox VE & Homelab Guide](docs/PROXMOX.md)
- [Product vision and scope](PROJECT.md)
- [Product design system](DESIGN.md)
- [System architecture](ARCHITECTURE.md)
- [Security model and policy](SECURITY.md)
- [Security review and current evidence](docs/SECURITY_REVIEW.md)
- [Resource, power-loss, and network test evidence](docs/RESOURCE_AND_HARDWARE_TEST.md)
- [Delivery roadmap](ROADMAP.md)
- [Development setup](docs/DEVELOPMENT.md)
- [Testing strategy](docs/TESTING.md)
- [Contributing guide](CONTRIBUTING.md)
- [Architecture decisions](docs/adr/README.md)

## Non-Negotiable Invariant

Linux service configuration is never edited directly by API handlers or UI actions. Every change follows this pipeline:

`input -> validation -> config model -> generation -> preflight -> snapshot -> apply -> verify -> commit or rollback`

## License

[MIT License](LICENSE)
