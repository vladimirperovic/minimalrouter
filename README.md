# Minimal Router OS

<p align="center">
  <img src="web/public/favicon.svg" alt="Minimal Router OS" width="130" />
</p>

<p align="center">
  <a href="#status"><img alt="Status: Early Alpha" src="https://img.shields.io/badge/status-early%20alpha-orange" /></a>
  <a href="https://github.com/vladimirperovic/minimalrouter/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/vladimirperovic/minimalrouter/actions/workflows/ci.yml/badge.svg" /></a>
  <a href="https://github.com/vladimirperovic/minimalrouter/actions/workflows/codeql.yml"><img alt="CodeQL" src="https://github.com/vladimirperovic/minimalrouter/actions/workflows/codeql.yml/badge.svg" /></a>
  <a href="LICENSE"><img alt="MIT License" src="https://img.shields.io/badge/license-MIT-blue.svg" /></a>
</p>

<p align="center">
  <a href="docs/INSTALLATION.md">Install</a> ·
  <a href="docs/PROXMOX.md">Proxmox</a> ·
  <a href="docs/CURRENT_VALIDATION.md">Validation</a> ·
  <a href="docs/README.md">Docs</a> ·
  <a href="SECURITY.md">Security</a> ·
  <a href="ROADMAP.md">Roadmap</a>
</p>

<a id="status"></a>

> **Early alpha.** Use only in a controlled lab/pilot with local console access
> and a known-good router ready for rollback. There is no stable signed ISO yet.

Minimal Router OS is a small Alpine Linux router appliance with a Go control
plane and React dashboard. Linux handles packet forwarding; the project builds on
`nftables`, `pppd`, `dnsmasq`, WireGuard and `inadyn` rather than implementing a
new network stack.

<p align="center">
  <img src="docs/images/dashboard-overview.svg" alt="Minimal Router dashboard overview" />
</p>

<p align="center"><sub>Current dashboard overview. Public IP and MAC addresses are replaced with documentation/example values.</sub></p>

## Highlights

- PPPoE WAN, DHCP/DNS, NAT and default-deny firewall
- WireGuard remote access and peer provisioning
- No-IP and Cloudflare Dynamic DNS
- gateway latency/loss/reconnect monitoring and live bandwidth view
- connected-device search, static lease visibility and Wake-on-LAN
- DNS filtering/device profiles, optional Squid, QoS and Wi-Fi AP
- transactional config apply with confirmation, rollback and recovery
- unprivileged `routerd` plus narrow privileged `router-applyd`
- encrypted backups, snapshots and crash-safe A/B updates
- bounded storage, health aggregation and security-focused CI

## Real Proxmox pilot

A controlled 2026-08-01 pilot carried real Internet traffic through the router
for about 27 minutes and successfully fell back to pfSense.

| Test | Minimal Router | pfSense |
|---|---:|---:|
| Download | **570 Mbps** | 543 Mbps |
| Upload | **327 Mbps** | 318 Mbps |
| Packet loss (600 packets) | **0%** | **0%** |
| DNS (200 queries) | **12.65 ms, 200/200** | 13.00 ms, 200/200 |
| CPU stress | **0% loss, dashboard 30/30** | — |
| RAM after test | **172 MB** | — |

External WireGuard handshake/dashboard access also passed. Operational fallback
to pfSense took about 93 seconds.

The tested Alpine `linux-virt` kernel lacked the required PPPoE module;
`linux-lts` worked. The validated Proxmox path therefore requires a successful:

```sh
modprobe pppoe
```

See [`docs/CURRENT_VALIDATION.md`](docs/CURRENT_VALIDATION.md) for the exact
validated scope and remaining gates.

## Quick start

Build a development archive:

```sh
git clone https://github.com/vladimirperovic/minimalrouter.git
cd minimalrouter
pnpm --dir web install --frozen-lockfile
make dist-amd64
cd build
sha256sum -c minimalrouter-linux-amd64.tar.gz.sha256
```

Install on a clean Alpine 3.22 test VM after verifying PPPoE kernel support:

```sh
tar xzf minimalrouter-linux-amd64.tar.gz
cd minimalrouter-linux-amd64
sudo sh install.sh
```

For a pre-provisioned air-gapped VM where all dependencies are already installed:

```sh
sudo sh install.sh --offline
```

Offline mode only checks installed packages with `apk info -e`; it does not run
`apk update` or `apk add`. Missing dependencies abort the installation.

Full instructions: [`docs/INSTALLATION.md`](docs/INSTALLATION.md) and
[`docs/PROXMOX.md`](docs/PROXMOX.md).

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

CI also covers clean Alpine installation, update/rollback, fuzzing, CodeQL,
secret scanning, ARM64/QEMU, network namespaces and performance checks.

## Documentation

- [`docs/README.md`](docs/README.md) — short documentation index
- [`docs/CURRENT_VALIDATION.md`](docs/CURRENT_VALIDATION.md) — what is proven now
- [`docs/INSTALLATION.md`](docs/INSTALLATION.md) — install and offline install
- [`docs/PROXMOX.md`](docs/PROXMOX.md) — VM baseline and safe pilot procedure
- [`docs/DYNAMIC_DNS.md`](docs/DYNAMIC_DNS.md) — No-IP / Cloudflare
- [`docs/RECOVERY.md`](docs/RECOVERY.md) — recovery and rollback
- [`docs/TESTING.md`](docs/TESTING.md) — test strategy and manual gates
- [`ARCHITECTURE.md`](ARCHITECTURE.md) — architecture details

## Security and privacy

A router is a security boundary. Read [`SECURITY.md`](SECURITY.md) before changing
privileged code and report vulnerabilities privately.

Never commit real credentials, private keys, backups, databases, packet captures,
public addresses, private hostnames, MAC addresses or household device inventory.
See [`PRIVACY.md`](PRIVACY.md).

## License

MIT — see [`LICENSE`](LICENSE).
