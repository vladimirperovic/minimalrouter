# Minimal Router OS — private home development repository

<p align="center">
  <img src="web/public/favicon.svg" alt="Minimal Router OS router logo" width="150" />
</p>

<p align="center">
  <a href="#project-status"><img alt="Status: Early Alpha" src="https://img.shields.io/badge/status-early%20alpha-orange" /></a>
  <a href="https://github.com/vladimirperovic/minimalrouterhome/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/vladimirperovic/minimalrouterhome/actions/workflows/ci.yml/badge.svg" /></a>
  <a href="LICENSE"><img alt="License: MIT" src="https://img.shields.io/badge/license-MIT-blue.svg" /></a>
</p>

<p align="center">
  <a href="docs/INSTALLATION.md">Installation</a> ·
  <a href="docs/PROXMOX.md">Proxmox</a> ·
  <a href="docs/PROXMOX_AI_HANDOFF.md">AI VM handoff</a> ·
  <a href="docs/CURRENT_VALIDATION.md">Current validation</a> ·
  <a href="docs/README.md">Documentation</a>
</p>

<a id="project-status"></a>

> **Development status: early alpha.** This private repository contains the
> owner's active home-development line. It is suitable for an isolated,
> console-accessible Proxmox pilot with pfSense ready for rollback. It is not yet
> an unattended production replacement.

Minimal Router OS is a focused Alpine Linux router appliance with a Go control
plane and a static React dashboard. Packet forwarding remains in the Linux
kernel through `nftables`, `pppd`, `dnsmasq`, WireGuard, and optional supporting
services.

## Current baseline

The current tree includes:

- unprivileged `routerd` plus narrow privileged `router-applyd`;
- SQLite canonical configuration state and migrations;
- typed validation and deterministic configuration generation;
- snapshot, preflight, apply, verification, commit-confirm, and rollback;
- default-deny WAN policy and LAN-to-WAN NAT;
- PPPoE, DHCP, DNS, WireGuard, DNS Filter profiles, QoS, DDNS, and Wi-Fi paths;
- Argon2id authentication, secure sessions, CSRF, rate limiting, and optional TOTP;
- encrypted backup export, configuration snapshots, and local recovery console;
- crash-safe A/B activation and rollback using a durable operation journal;
- signed manifests, SHA-256 verification, checksums, SPDX SBOMs, and provenance;
- frontend unit tests and Playwright browser E2E tests;
- clean Alpine install, wizard, update activation, and rollback CI;
- race tests, `vet`, `govulncheck`, secret scanning, `gosec`, `shellcheck`, and
  `actionlint`;
- API/update benchmarks, fuzzing, ARM64 QEMU smoke tests, and isolated
  WAN-router-LAN network tests.

The dashboard build uses TypeScript 6.0.3 and Node.js type definitions 26.1.2.
Node.js remains a build-time dependency only.

See [`docs/CURRENT_VALIDATION.md`](docs/CURRENT_VALIDATION.md) for the dated
validation summary and remaining manual gates.

## Existing Proxmox VM

The owner has already created a Proxmox VM, but the VM ID, node, bridge names,
addresses, and credentials are intentionally not stored in Git.

A future AI operator must start with the private handoff:

- [`docs/PROXMOX_AI_HANDOFF.md`](docs/PROXMOX_AI_HANDOFF.md)

That document requires read-only discovery before any start, rewire, update, or
destructive test. It explains how to preserve pfSense rollback, verify the VM
boundary, boot safely, update through the verified path, run tests in the correct
order, redact evidence, and stop when the topology is ambiguous.

## What remains unproven

The following still require target-host evidence:

- stable Proxmox WAN/LAN identity across repeated reboots;
- real ISP PPPoE connection, reconnect, MTU, and authentication;
- actual VirtIO or passed-through NIC throughput, packet rate, CPU, IRQ, latency,
  jitter, and thermals;
- real WireGuard throughput and recovery from an unrelated network;
- external IPv4/IPv6 scanning;
- backup restore into a fresh VM;
- destructive fault injection on a disposable target;
- sustained operation and bounded disk/log growth;
- owner-signed install/recovery media and independent security review.

## Controlled development build

```sh
git clone <trusted-private-repository-url>
cd minimalrouterhome
git checkout main
git pull --ff-only
git rev-parse HEAD
pnpm --dir web install --frozen-lockfile
make dist-amd64
cd build
sha256sum -c minimalrouter-linux-amd64.tar.gz.sha256
```

A development archive is not a signed stable firmware release. Do not overwrite
live router binaries manually; follow the installation or A/B update procedures.

## Development validation

```sh
go test -race ./...
go vet ./...

pnpm --dir web install --frozen-lockfile
pnpm --dir web lint
pnpm --dir web test
pnpm --dir web build
pnpm --dir web exec playwright install chromium
pnpm --dir web test:e2e
```

Additional automated suites are defined in the CI, Deep validation, Performance,
and security workflows.

## Safety and privacy

Never commit or publish:

- Proxmox hostnames, node names, VM IDs, bridge inventory, or raw VM configs;
- PPPoE credentials or administrator credentials;
- WireGuard keys, profiles, or QR codes;
- provider tokens or signing private keys;
- backups, databases, snapshots, packet captures, or runtime logs;
- real addresses, hostnames, MAC addresses, or household device inventory.

Keep the existing pfSense VM/appliance available until the Minimal Router VM has
passed the private Proxmox test report and sustained pilot period.

## Documentation

Start with:

- [`docs/README.md`](docs/README.md)
- [`docs/CURRENT_VALIDATION.md`](docs/CURRENT_VALIDATION.md)
- [`docs/INSTALLATION.md`](docs/INSTALLATION.md)
- [`docs/PROXMOX.md`](docs/PROXMOX.md)
- [`docs/PROXMOX_AI_HANDOFF.md`](docs/PROXMOX_AI_HANDOFF.md)
- [`docs/TESTING.md`](docs/TESTING.md)
- [`docs/RECOVERY.md`](docs/RECOVERY.md)
- [`docs/RESOURCE_AND_HARDWARE_TEST.md`](docs/RESOURCE_AND_HARDWARE_TEST.md)
- [`docs/SECURITY_REVIEW.md`](docs/SECURITY_REVIEW.md)
- [`ROADMAP.md`](ROADMAP.md)
- [`CHANGELOG.md`](CHANGELOG.md)

## Releases

There is no stable signed release yet. Official releases must follow the release
process and security documentation, use an owner-controlled signing identity,
and publish exact supported deployment claims.

## License

Minimal Router OS is available under the [MIT License](LICENSE).
