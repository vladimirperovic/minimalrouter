<<<<<<< HEAD
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
  <a href="docs/DYNAMIC_DNS.md">Dynamic DNS</a> ·
  <a href="docs/README.md">Documentation</a>
</p>

<a id="project-status"></a>

> **Development status: early alpha.** This private repository contains the
> owner's active home-development line. A real owner-Proxmox pilot has now passed
> basic PPPoE/Internet, external WireGuard dashboard access and pfSense fallback,
> but the system remains a guarded pilot rather than an unattended production
> replacement.

Minimal Router OS is an Alpine Linux router appliance with a Go control plane and
static React dashboard. Packet forwarding remains in the Linux kernel through
`nftables`, `pppd`, `dnsmasq`, WireGuard and optional supporting services.

## Current baseline

The tree includes:

- unprivileged `routerd` plus narrow privileged `router-applyd`;
- SQLite canonical state, typed validation and deterministic generation;
- preflight/apply/verification/commit-confirm/rollback;
- default-deny WAN policy and NAT;
- PPPoE, DHCP, DNS, WireGuard, DNS Filter, QoS and Wi-Fi paths;
- **No-IP Dynamic DNS through native Alpine `inadyn`**, default for new configs;
- Cloudflare DDNS retained for legacy/backward compatibility;
- provider-specific DDNS credential validation and provider-switch secret reset;
- Argon2id auth, secure sessions, CSRF, rate limiting and optional TOTP;
- encrypted backup/snapshots/local recovery;
- crash-safe A/B updates and signed-manifest verification;
- bounded storage/log/history behavior;
- central authenticated appliance health;
- Go/frontend/E2E/security/fuzz/ARM64/network-namespace/performance tests.

## 2026-08-01 owner-Proxmox pilot

Recorded target-host evidence:

- real PPPoE and Internet forwarding;
- 570 Mbps download / 327 Mbps upload in the recorded sample;
- 0% loss in 600 packets;
- DNS 200/200;
- dashboard 30/30 during the recorded CPU-load sample;
- 172 MB RAM after the exercised workload;
- external phone WireGuard handshake and dashboard access through the tunnel;
- successful fallback to pfSense in approximately 93 seconds.

### Alpine kernel finding

The first real PPPoE attempt used `linux-virt`, whose running kernel did not
provide the PPPoE module required by the appliance. Switching to **Alpine
`linux-lts`** supplied the module and PPPoE succeeded. The clean LTS boot used
approximately 73 MB RAM in that session.

The private and public installers now fail closed unless:
=======
# Minimal Router OS

<p align="center">
  <img src="web/public/favicon.svg" alt="Minimal Router OS" width="130" />
</p>

<p align="center">
  <a href="#status"><img alt="Status: Beta" src="https://img.shields.io/badge/status-beta-blue" /></a>
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

<p align="center">
  <img src="docs/images/dashboard-overview.svg" alt="Minimal Router dashboard overview" />
</p>

<p align="center"><sub>Current dashboard overview. Public IP and MAC addresses are omitted.</sub></p>

<a id="status"></a>

> **Beta — core routing validated.** Minimal Router is carrying real PPPoE traffic
> in a validated Proxmox deployment, with DHCP/DNS, NAT, WireGuard, Dynamic DNS,
> Squid, gateway monitoring, snapshots/recovery, security controls and audit logs
> working end to end. Remaining gaps are mostly management UI or hardware-specific:
> firewall/port-forward editing, dashboard DHCP reservations, TOTP and Cloudflare
> Tunnel UI, QoS dashboard controls and target-host shaping validation, DNS-filter
> device/guest selection, Wi-Fi on systems with a supported radio, and signing-key
> management. Local-console recovery remains the deliberate safety path while the
> final release/update workflow is completed.

**Lightweight by design.** The validated Proxmox appliance runs the core routing
stack with **512 MiB RAM and 2 vCPUs**, handling real PPPoE traffic, WireGuard,
DHCP/DNS, Dynamic DNS, monitoring and recovery. **1 GiB RAM is recommended** when
enabling more optional services or scaling peer/device counts.

Minimal Router OS is a small Alpine Linux router appliance with a Go control
plane and React dashboard. Linux handles packet forwarding; the project builds on
`nftables`, `pppd`, `dnsmasq`, WireGuard and `inadyn` rather than implementing a
new network stack.

## Highlights

- PPPoE WAN, DHCP/DNS, NAT and default-deny firewall
- WireGuard remote access and peer provisioning
- No-IP and Cloudflare Dynamic DNS
- gateway latency/loss/reconnect monitoring and live bandwidth view
- connected-device search, DHCP lease visibility and Wake-on-LAN
- DNS filtering/device profiles and non-caching Squid
- QoS/SQM backend with CAKE or fq_codel traffic shaping
- optional Wi-Fi AP on supported hardware
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

The current Proxmox VM allocation is **2 vCPUs and 512 MiB RAM**. The full core
routing stack remains operational within that allocation.

External WireGuard handshake/dashboard access also passed. Operational fallback
to pfSense took about 93 seconds.

The tested Alpine `linux-virt` kernel lacked the required PPPoE module;
`linux-lts` worked. The validated Proxmox path therefore requires a successful:
>>>>>>> public/main

```sh
modprobe pppoe
```

<<<<<<< HEAD
succeeds. `linux-lts` is the validated Proxmox path.

### No-IP status

The actual deployment uses **No-IP**. During the successful WireGuard test, DDNS
was provisioned manually on the Proxmox side. That proved the external endpoint
and WireGuard path but not an appliance-managed updater.

This branch implements No-IP inside MinimalRouter via `inadyn`. The next target
proof is to configure it through the dashboard, verify external resolution and
WireGuard through that hostname, remove the host-side workaround, and later
prove automatic propagation after a real public-IP change.

See:

- [`docs/PROXMOX_TEST_REPORT_2026-08-01.md`](docs/PROXMOX_TEST_REPORT_2026-08-01.md)
- [`docs/DYNAMIC_DNS.md`](docs/DYNAMIC_DNS.md)
- [`docs/CURRENT_VALIDATION.md`](docs/CURRENT_VALIDATION.md)

## Existing Proxmox VM

The VM ID, node, bridge names, addresses and credentials are intentionally not
stored in Git. Any future operator must start with:

- [`docs/PROXMOX_AI_HANDOFF.md`](docs/PROXMOX_AI_HANDOFF.md)

That guide requires read-only discovery, `linux-lts`/`modprobe pppoe` validation,
independent pfSense rollback, and private handling of all live credentials.

## What remains unproven

Still required before unattended production use:

- repeated Proxmox/guest reboot and interface identity;
- repeated real PPPoE disconnect/reconnect/reboot recovery;
- MinimalRouter-managed No-IP update and later public-IP-change propagation;
- WireGuard recovery after PPPoE reconnect/reboot;
- sustained packet-rate/CPU/IRQ/latency/jitter/thermal testing;
- external IPv4/IPv6 scanning;
- backup restore into a fresh VM;
- full-disk/inode/read-only/service-crash/power-loss tests on disposable state;
- at least seven days continuous operation;
- owner-qualified signed recovery media and independent security review.

## Controlled development build

```sh
git clone <trusted-private-repository-url>
cd minimalrouterhome
git checkout main
git pull --ff-only
git rev-parse HEAD
=======
See [`docs/CURRENT_VALIDATION.md`](docs/CURRENT_VALIDATION.md) for the exact
validated scope and remaining gates.

## Quick start

Validated Proxmox baseline: **2 vCPUs, 512 MiB RAM minimum; 1 GiB recommended**.

Build a development archive:

```sh
git clone https://github.com/vladimirperovic/minimalrouter.git
cd minimalrouter
>>>>>>> public/main
pnpm --dir web install --frozen-lockfile
make dist-amd64
cd build
sha256sum -c minimalrouter-linux-amd64.tar.gz.sha256
```

<<<<<<< HEAD
Do not overwrite live router binaries manually; use the documented install/A-B
update path.

## Safety and privacy

Never commit or publish:

- Proxmox host/node/VM/bridge inventory or raw VM configs;
- PPPoE credentials or administrator credentials;
- WireGuard private/preshared keys, profiles or QR codes;
- No-IP DDNS Keys/passwords, Cloudflare tokens or signing private keys;
- backups, databases, snapshots, packet captures or runtime logs;
- real addresses/private hostnames, MACs or household inventory.

Keep pfSense available until the remaining private target-host gates are closed.

## Documentation

Start with:

- [`docs/README.md`](docs/README.md)
- [`docs/CURRENT_VALIDATION.md`](docs/CURRENT_VALIDATION.md)
- [`docs/INSTALLATION.md`](docs/INSTALLATION.md)
- [`docs/PROXMOX.md`](docs/PROXMOX.md)
- [`docs/PROXMOX_AI_HANDOFF.md`](docs/PROXMOX_AI_HANDOFF.md)
- [`docs/PROXMOX_TEST_REPORT_2026-08-01.md`](docs/PROXMOX_TEST_REPORT_2026-08-01.md)
- [`docs/DYNAMIC_DNS.md`](docs/DYNAMIC_DNS.md)
- [`docs/RECOVERY.md`](docs/RECOVERY.md)
- [`docs/TESTING.md`](docs/TESTING.md)

## License

Minimal Router OS is available under the [MIT License](LICENSE).
=======
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
>>>>>>> public/main
