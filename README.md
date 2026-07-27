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
  paths for `nftables`, `pppd` (PPPoE), `dnsmasq` (DHCP/DNS), WireGuard, and
  Squid. Cloudflare, AdGuard, Wi-Fi AP, and QoS remain disabled until their
  privileged lifecycle adapters are complete.
- **Squid Proxy & Restricted IP Alias**: Non-caching HTTP/HTTPS forward proxy with NCSA basic authentication. Integrates with `nftables` to drop direct WAN internet traffic for restricted IP aliases while allowing browser proxy access on port `3128`.
- **Model Context Protocol (MCP) AI Agent Integration**: Local Go MCP bridge (`cmd/minimalrouter-mcp`) with a server-enforced read-only default. Explicit admin mode is required for supported mutations.
- **pfSense XML Importer**: Preview-first importer for selected settings. NAT rules are imported disabled because WireGuard is the only allowed WAN entry point.
- **First-Run Installation Wizard**: Guided 5-step Apple × Swiss setup wizard per `DESIGN.md §14`.
- **Security Baseline**: Argon2id password hashing, 256-bit HttpOnly secure cookie sessions, optional TOTP, CSRF protection, rate limiting, secret redaction, encrypted backup export, and an installer that refuses unencrypted root filesystems outside explicit lab mode.
- **Proxmox VE Automated Helper**: 1-command Proxmox VM installer (`packaging/proxmox/create-vm.sh`) for automated VM #100 creation.
- **Alpine Linux Packaging**: OpenRC init scripts, hardened sysctls, required
  kernel-module loading, and an appliance overlay builder. `make iso` prepares
  the overlay; an Alpine `mkimage` build host is still required to produce and
  sign bootable recovery media.

## Core Stack

- **OS**: Alpine Linux 3.22 (LUKS Full Disk Encryption Ready)
- **Backend**: Go 1.24 REST API (`/api/v1`)
- **AI Agent Interface**: Model Context Protocol (MCP stdio over JSON-RPC 2.0)
- **Frontend**: React + TypeScript (Next.js static single-page application)
- **Integrations**: nftables, pppd, dnsmasq, WireGuard, and Squid Proxy. Cloudflare, AdGuard, Wi-Fi AP, and QoS currently fail closed until their privileged lifecycle adapters are complete.
- **Store**: SQLite canonical state store with pre-apply sha256 checksummed snapshots

## Quick Start & Proxmox VE Setup

### 1. 1-Line Proxmox VE VM Installation

Run directly in your **Proxmox VE Node Shell**:

```bash
bash <(curl -sSL https://raw.githubusercontent.com/vladimirperovic/minimalrouter/main/packaging/proxmox/create-vm.sh)
```

Creates VM #100 with 512 MB RAM, 1 vCPU, automatic start-on-boot priority (`order=1`), physical WAN bridge (`vmbr0`), and internal private LAN bridge (`vmbr1`).

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

Open `http://localhost:3000` in browser. For VM tests, pre-built
`web/dist/` is used automatically.

### 5. Build the Alpine appliance overlay

```bash
make iso
```

The command does not itself emit a bootable ISO. Follow the printed `mkimage`
command on a trusted Alpine build host and verify/sign the resulting image
before installation.

## Documentation

- [AI Agent Integration (MCP Protocol)](docs/MCP.md)
- [AI continuation, macOS preview, and Alpine VM test guide](AI_HANDOFF.md)
- [Proxmox VE & Homelab Guide](docs/PROXMOX.md)
- [Product vision and scope](PROJECT.md)
- [Product design system](DESIGN.md)
- [System architecture](ARCHITECTURE.md)
- [Security model and policy](SECURITY.md)
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
