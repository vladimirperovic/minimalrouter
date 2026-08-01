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

```sh
modprobe pppoe
```

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
pnpm --dir web install --frozen-lockfile
make dist-amd64
cd build
sha256sum -c minimalrouter-linux-amd64.tar.gz.sha256
```

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
