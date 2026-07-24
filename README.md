# Minimal Router OS

Minimal Router OS is an ultra-lightweight Alpine Linux appliance for home and
small-office routing. It combines proven Linux networking components with a Go control plane (`routerd` + `router-applyd`) and an Apple × Swiss minimalist web interface.

The product is intentionally narrow: quick installation, safe configuration,
automatic snapshots, reliable rollback, and a clean user experience. It is not
intended to replace pfSense or become a general-purpose networking platform.

## Project Status & Built Features

Version 1 core control plane engine is fully implemented:

- **Unprivileged & Privileged Go Binaries** (`routerd` unprivileged management plane + `router-applyd` privileged Unix socket helper).
- **Service Configuration Generators**: Deterministic ruleset generators for `nftables`, `pppd` (PPPoE), `dnsmasq` (DHCP/DNS), `wireguard` (with mobile QR code generator), and `cloudflared` (DDNS & Tunnel).
- **pfSense XML Importer**: Tool for importing existing pfSense `config.xml` files.
- **First-Run Installation Wizard**: Guided 5-step Apple × Swiss setup wizard per `DESIGN.md §14`.
- **Security Baseline**: Argon2id password hashing, 256-bit HttpOnly secure cookie sessions, CSRF protection, rate limiting, and secret redaction.
- **Alpine Linux Packaging**: OpenRC init scripts and automated appliance ISO generator script (`make iso`).

## Core Stack

- **OS**: Alpine Linux 3.22
- **Backend**: Go 1.24 REST API (`/api/v1`)
- **Frontend**: React + TypeScript (Next.js static single-page application)
- **Integrations**: nftables, pppd, dnsmasq, WireGuard, cloudflared
- **Store**: SQLite canonical state store with pre-apply sha256 checksummed snapshots

## Quick Start & Development

### 1. Run Backend Server (`routerd`)

```bash
go run ./cmd/routerd
```

### 2. Run Web Dashboard

```bash
cd web
pnpm install
pnpm dev
```

Open `http://localhost:3000` in browser.

### 3. Build Alpine ISO Appliance

```bash
make iso
```

## Documentation

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
