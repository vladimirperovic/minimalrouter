# Minimal Router OS

Minimal Router OS is an ultra-lightweight Alpine Linux appliance for home and
small-office routing. It combines proven Linux networking components with a
small Go control plane and a focused web interface.

The product is intentionally narrow: quick installation, safe configuration,
automatic snapshots, reliable rollback, and a clean user experience. It is not
intended to replace pfSense or become a general-purpose networking platform.

## Project status

The project is in the architecture and foundation phase. No production-ready
image exists yet.

## Core stack

- Alpine Linux
- Go backend and REST API
- Svelte + TypeScript frontend
- nftables, pppd, dnsmasq, WireGuard, and cloudflared
- SQLite as the canonical configuration and state store

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

## Dashboard preview

The Apple × Swiss one-page interface prototype lives in `web/`. It includes
internet traffic and PPPoE status, system health, DHCP and static leases,
firewall controls, WireGuard peer and QR flows, Cloudflare DDNS/Tunnel status,
snapshots, backup, and updates.

Run it locally with:

```sh
cd web
pnpm install
pnpm dev
```

## Non-negotiable configuration rule

Linux service configuration is never edited directly by API handlers or UI
actions. Every change follows this pipeline:

`input -> validation -> config model -> generation -> preflight -> snapshot -> apply -> verify -> commit or rollback`

## License

No license has been selected yet. Until a license file is added, all rights are
reserved.
