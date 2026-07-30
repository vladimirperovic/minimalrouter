# Minimal Router OS

<p align="center">
  <img src="web/public/favicon.svg" alt="Minimal Router OS router logo" width="150" />
</p>

<p align="center">
  <a href="#project-status"><img alt="Status: Early Alpha" src="https://img.shields.io/badge/status-early%20alpha-orange" /></a>
  <a href="https://github.com/vladimirperovic/minimalrouter/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/vladimirperovic/minimalrouter/actions/workflows/ci.yml/badge.svg" /></a>
  <a href="https://github.com/vladimirperovic/minimalrouter/actions/workflows/codeql.yml"><img alt="CodeQL" src="https://github.com/vladimirperovic/minimalrouter/actions/workflows/codeql.yml/badge.svg" /></a>
  <a href="LICENSE"><img alt="License: MIT" src="https://img.shields.io/badge/license-MIT-blue.svg" /></a>
</p>

<p align="center">
  <a href="docs/INSTALLATION.md">Installation</a> ·
  <a href="docs/README.md">Documentation</a> ·
  <a href="SECURITY.md">Security</a> ·
  <a href="CONTRIBUTING.md">Contributing</a> ·
  <a href="ROADMAP.md">Roadmap</a>
</p>

<a id="project-status"></a>

> **Development status: early alpha.** Minimal Router OS is a community-driven
> research and homelab project. It is not yet a drop-in replacement for pfSense,
> OpenWrt, or a commercially supported firewall. Use it on an isolated test
> network until the project publishes a stable release and a completed hardware
> validation matrix.

Minimal Router OS is a small Alpine Linux router appliance with a Go control
plane and a React dashboard. It combines proven Linux networking components with
a narrow, validated configuration system instead of implementing a new packet
processing stack.

- `nftables` for firewalling, NAT, and scheduled device-profile policy;
- `pppd` for PPPoE;
- `dnsmasq` for DHCP, DNS, the global DNS Filter, and bounded service destination sets;
- WireGuard for remote access;
- optional Squid proxy, QoS, Cloudflare DDNS, and Wi-Fi AP support.

The goal is a focused home and small-office router that is understandable,
resource-efficient, secure by default, recoverable after mistakes, and pleasant
to administer.

## Dashboard

The dashboard follows a restrained Apple × Swiss visual system and exposes the
router configuration through the same validated API used by other clients.

![Minimal Router OS dashboard overview showing synthetic router status, traffic, resource use, and navigation](docs/images/dashboard-overview.png)

The image above was captured automatically from the React production build.
Every displayed address, hostname, device, MAC address, status value, and
measurement is synthetic documentation data. It is not a screenshot of a
personal or production network.

## Current capabilities

### Implemented and covered in the development environment

- split control plane: unprivileged `routerd` and privileged `router-applyd`;
- SQLite canonical configuration store;
- transactional generation, preflight, snapshot, apply, verify, and rollback;
- default-deny WAN firewall and NAT;
- PPPoE WAN configuration;
- reliable WAN/LAN interface discovery with explicit operator confirmation;
- DHCP and DNS service;
- global DNS Filter and scheduled device profiles;
- configurable Kids profile, including weekday windows and full-weekend access;
- WireGuard server and split-tunnel phone profiles;
- encrypted backup export and configuration snapshots;
- Argon2id authentication, secure sessions, CSRF protection, and optional TOTP;
- local recovery console for password/TOTP reset, LAN repair, snapshot restore,
  and factory reset;
- live DHCP lease display and redacted audit logs;
- guided first-run wizard;
- frontend unit tests and browser E2E coverage for critical setup/profile flows;
- Alpine/OpenRC packaging and a clean-install CI smoke test;
- signed release workflow with checksums, SPDX SBOMs, GitHub provenance, and a
  pinned-key A/B staging/rollback implementation.

### Optional and disabled by default

- Cloudflare Dynamic DNS;
- Wi-Fi access point;
- Squid forward proxy;
- traffic shaping/QoS;
- WireGuard remote access;
- DNS Filter device profiles.

### Not yet a stable release feature

- production-grade IPv6 parity;
- multi-WAN and high availability;
- VLAN and managed-switch workflows;
- signed bootable recovery images;
- unattended update activation or unattended production support;
- a broad third-party package ecosystem;
- physical-hardware qualification across supported NICs.

Unsupported functionality fails closed or is shown as unavailable rather than
being simulated.

## Kids schedule example

The device-profile editor starts with a practical household preset:

- YouTube, Steam, and Wikipedia/Wikimedia;
- Monday-Friday from `19:00` through `23:59`;
- all-day access on Saturday and Sunday.

The schedule and selected services are editable. Managed devices require stable
LAN addresses and must use the router resolver. DNS-derived classification is a
household convenience policy, not a high-assurance application firewall; read
[docs/DEVICE_PROFILES.md](docs/DEVICE_PROFILES.md) before relying on it.

## Project principles

- **Safe defaults:** WAN is default-deny and management is not exposed directly
  to WAN.
- **Least privilege:** the network-facing API runs separately from the privileged
  apply helper.
- **Deterministic changes:** configuration is validated and generated from typed
  models rather than arbitrary shell fragments.
- **Recoverability:** disruptive changes use snapshots, verification,
  confirmation, local-console recovery, and rollback.
- **Honest status:** documentation distinguishes implemented behavior, measured
  evidence, planned work, and unsupported features.
- **Small scope:** features may be declined when they significantly expand attack
  surface or long-term maintenance cost.

## Minimal Router OS and pfSense

This is an approximate comparison of project scope, not a claim of feature or
security parity.

| Area | Minimal Router OS — current alpha | pfSense |
|---|---|---|
| Maturity | Experimental, community development project | Mature production firewall platform |
| Intended use today | Lab, homelab pilot, controlled testing | Production home, business, and enterprise deployments |
| Base operating system | Alpine Linux | FreeBSD |
| Hardware architecture | Development targets include x86-64 and ARM64 | x86-64 plus supported Netgate ARM appliances |
| RAM | About 140 MiB idle and about 203 MiB after setup/config activity in one ARM64 VM test; 512 MiB tested minimum, 1 GiB recommended for comfortable development use | Official minimum is 1 GiB; actual sizing depends on states, packages, VPN, and traffic |
| Disk | Small application payload; 8 GiB is currently recommended for the appliance, logs, snapshots, and upgrades | Official minimum is 8 GB |
| CPU | Narrow service set is expected to have low idle CPU use, but a fair cross-platform benchmark has not yet been published | Depends heavily on throughput, VPN, IDS/IPS, packages, and state count |
| Firewall/NAT | Focused generated `nftables` policy; WAN port forwards intentionally unsupported in the secure profile | Extensive firewall, NAT, policy routing, aliases, schedules, and advanced features |
| Remote administration | WireGuard-first; no WAN web management | Multiple mature VPN and administration options |
| DNS filtering | Basic global sinkhole plus bounded DNS-derived device schedules | DNS filtering is commonly added with optional packages or a separate resolver |
| Packages | No general package ecosystem | Large optional package system |
| IPv6 | Disabled/fail-closed until policy parity is complete | Mature IPv6 support |
| High availability | Not implemented | CARP and established HA workflows |
| Support | Community best effort | Community plus commercial Netgate support options |

The lower measured memory footprint is mainly a consequence of Minimal Router OS
having a much narrower feature set. pfSense remains substantially more mature,
more flexible, more thoroughly deployed, and the safer choice when its advanced
features or production support are required.

References:

- pfSense minimum requirements: https://docs.netgate.com/pfsense/en/latest/hardware/minimum-requirements.html
- pfSense hardware sizing: https://docs.netgate.com/pfsense/en/latest/hardware/size.html
- pfSense package system: https://docs.netgate.com/pfsense/en/latest/packages/

A more detailed three-way comparison is available in
[docs/COMPARISON.md](docs/COMPARISON.md). Dated measurement evidence is in
[docs/RESOURCE_AND_HARDWARE_TEST.md](docs/RESOURCE_AND_HARDWARE_TEST.md).

## Architecture

```mermaid
flowchart LR
    Browser[Administrator browser]
    UI[React dashboard]
    Routerd[routerd — unprivileged Go API]
    DB[(SQLite canonical state)]
    Applyd[router-applyd — privileged helper]
    Recovery[Local recovery console]
    Linux[Linux networking services]
    LAN[LAN clients]
    WAN[Internet]

    Browser --> UI
    UI -->|HTTPS /api/v1| Routerd
    Routerd --> DB
    Routerd -->|typed local IPC| Applyd
    Recovery -->|local console only| DB
    Applyd --> Linux
    LAN <--> Linux
    Linux <--> WAN
```

Packet forwarding stays in the Linux kernel. API handlers do not execute
arbitrary shell commands or edit service configuration directly.

Every configuration change follows the same invariant:

```text
input → validation → typed model → generation → preflight → snapshot
      → apply → verification → commit or rollback
```

See [ARCHITECTURE.md](ARCHITECTURE.md), [SECURITY.md](SECURITY.md), and
[docs/RECOVERY.md](docs/RECOVERY.md).

## Development setup

Requirements:

- Go version declared in `go.mod`;
- Node.js 22;
- pnpm.

```sh
git clone https://github.com/vladimirperovic/minimalrouter.git
cd minimalrouter

go test -race ./...
go vet ./...

pnpm --dir web install --frozen-lockfile
pnpm --dir web lint
pnpm --dir web test
pnpm --dir web build
```

Run browser E2E tests after installing the Playwright Chromium dependency:

```sh
pnpm --dir web exec playwright install chromium
pnpm --dir web test:e2e
```

Run the dashboard development server:

```sh
pnpm --dir web dev
```

The production router does not require Node.js. The dashboard is compiled to
static assets during the build. See [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md)
for the complete workflow.

## Controlled installation

There is no signed stable ISO yet. For a controlled lab installation, use a
clean Alpine Linux 3.22 VM or dedicated test system with two network interfaces.

Start with [docs/INSTALLATION.md](docs/INSTALLATION.md). Proxmox users should also
read [docs/PROXMOX.md](docs/PROXMOX.md).

Build a self-contained x86-64 archive:

```sh
make dist-amd64
```

The CI workflow installs the generated archive in a clean privileged Alpine
container and completes the first-run wizard over HTTPS. This smoke test does not
replace physical NIC, real ISP, power-loss, throughput, recovery-media, or
independent security testing.

## Security and privacy

A router is a security boundary. Read [SECURITY.md](SECURITY.md) before running
the project or changing privileged code. Do not report vulnerabilities in a
public issue; use the private reporting method described in the security policy.

The current project does not intentionally include project-operated analytics,
advertising, or cloud telemetry. Local data and optional integrations are
explained in [PRIVACY.md](PRIVACY.md).

Never commit or publicly attach:

- PPPoE usernames or passwords;
- administrator passwords, hashes, sessions, or CSRF values;
- WireGuard private keys, preshared keys, profiles, or QR codes;
- Cloudflare or other provider tokens;
- release signing private keys;
- exported backups;
- real runtime databases, configuration files, snapshots, packet captures, logs,
  public addresses, hostnames, MAC addresses, or device inventory.

## Community and governance

Beginners, homelab users, network engineers, security reviewers, designers,
technical writers, testers, translators, and experienced Go or React developers
are welcome.

- [CONTRIBUTING.md](CONTRIBUTING.md) — contribution workflow and definition of done;
- [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) — expected community behavior;
- [GOVERNANCE.md](GOVERNANCE.md) — decision-making, security review, and release authority;
- [MAINTAINERS.md](MAINTAINERS.md) — active maintainers and ownership;
- [SUPPORT.md](SUPPORT.md) — support scope and privacy-safe diagnostics.

## Documentation

The complete documentation index is [docs/README.md](docs/README.md).

Key documents:

- [Architecture](ARCHITECTURE.md)
- [Product scope](PROJECT.md)
- [Security policy and threat model](SECURITY.md)
- [Privacy](PRIVACY.md)
- [Installation](docs/INSTALLATION.md)
- [Recovery](docs/RECOVERY.md)
- [DNS Filter device profiles](docs/DEVICE_PROFILES.md)
- [Release security and rollback](docs/RELEASE_SECURITY.md)
- [Troubleshooting](docs/TROUBLESHOOTING.md)
- [Development guide](docs/DEVELOPMENT.md)
- [Testing guide](docs/TESTING.md)
- [Current security review](docs/SECURITY_REVIEW.md)
- [Roadmap](ROADMAP.md)
- [Changelog](CHANGELOG.md)
- [Architecture decisions](docs/adr/README.md)

## Releases

There is no stable signed release yet. Do not treat a development archive or a
source commit as production-ready firmware. Official releases must follow
[docs/RELEASE_PROCESS.md](docs/RELEASE_PROCESS.md) and
[docs/RELEASE_SECURITY.md](docs/RELEASE_SECURITY.md), publish signed manifests,
checksums, SPDX SBOMs, provenance, known limitations, and the exact supported
deployment class.

## License

Minimal Router OS is available under the [MIT License](LICENSE).

The project name and documentation do not imply endorsement by Netgate, pfSense,
OpenWrt, AdGuard, Cloudflare, Alpine Linux, or any other referenced project or
company.
