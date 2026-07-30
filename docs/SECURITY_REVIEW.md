# Security Review — 2026-07-28

> **Scope note:** this dated review predates the optional IoT zone and
> fixed-device scheduler added after 2026-07-28. Their unit/generator/CI evidence
> is documented separately; dedicated-port, managed-switch VLAN, daylight-saving,
> and real-client schedule tests remain required before the next security-review
> verdict.

## Verdict

The current tree is materially safer and suitable for a controlled,
console-accessible bench pilot. It is not yet approved as an unattended
production home-router replacement.

The core that is enabled fails closed and was exercised in a real Alpine ARM64
virtual machine. Features without a complete transactional runtime adapter are
now visibly unavailable and rejected by both the management plane and the
privileged helper. No review can honestly establish that a router is
“completely secure”; the remaining release gates below still matter.

## Scope

This review covered all Markdown documentation, Go control-plane code, the
React/Vite dashboard, OpenAPI, Alpine installers and OpenRC services, release
packaging, Proxmox helpers, and the macOS Virtualization.framework test path.

It did not include real ISP credentials, physical NICs or Wi-Fi hardware, a
user pfSense export, destructive installation to the Mac's internal disk,
external Internet-origin penetration testing, or signed recovery-media boot.
An isolated persistent disk, abrupt power loss, rollback, synthetic WAN scan,
and virtual throughput test were subsequently completed as described below.
The current VM also verified the packaged Cloudflare DDNS configuration and
Wi-Fi's fail-closed behavior without a radio, but not a live DNS update or
wireless client association.

## Material corrections

- Architecture-specific release names now prevent stale or wrong-architecture
  binaries from being installed. Both ARM64 and AMD64 packages are rebuilt
  from the current tree.
- Distribution and overlay builds clear their exact staging directory before
  copying hashed static assets, so obsolete JavaScript is not retained.
- Commit-confirm uses the same configuration revision in `routerd` and
  `router-applyd`; a regression test and VM evidence cover the mismatch.
- Management accepts only the configured LAN/WireGuard destination (plus an
  explicit loopback-preview exception) and validates `Host` to reduce
  DNS-rebinding exposure.
- Cloudflare DDNS now has a real Alpine `inadyn` lifecycle with syntax,
  credential/update, service-health, and rollback checks. Wi-Fi now has a real
  `hostapd` lifecycle, AP-capability preflight, transactional LAN bridge,
  service/membership checks, commit-confirm, and rollback. Cloudflare Tunnel,
  DoH, per-device DNS policy, WAN port forwarding, and automatic updates still
  fail closed instead of simulating success.
- Update verification is fail closed: trusted Ed25519 key, SHA-256, exact
  signature, bounded size, HTTPS/same-origin fetching, and a verified receipt
  are mandatory. The update API remains disabled until a privileged,
  reauthenticated transaction exists.
- Backup routes accepting caller-supplied raw encryption keys were removed.
  Password-reauthenticated encrypted export remains.
- Cloudflare and other generated secret files use restricted permissions.
  Diagnostics redact WireGuard and Cloudflare secrets.
- Global DNS filtering uses only a small reviewed built-in list. The privileged
  apply path performs no blocklist network download. Invalid and placeholder
  built-in entries were removed.
- CAKE and fq_codel generation no longer installs conflicting root qdiscs.
  The root helper invokes fixed `/sbin/tc` argv directly rather than executing
  a generated shell script. Apply verifies the live qdisc; disable and rollback
  clear shaping.
- Proxmox ISO download requires an independently supplied SHA-256 and installs
  only after TLS download and exact verification.
- `routerd` remains unprivileged and `router-applyd` is the narrow root helper.
  Node.js is a build-time dependency only. The router runtime is Bash-free:
  only `wireguard-tools-wg` is installed, and the helper applies a
  shell-hook-free runtime configuration with fixed `wg` and `ip` argv.
- The dashboard enables the implemented Cloudflare DDNS and Wi-Fi controls,
  labels the remaining unavailable features, reports failed saves, and no
  longer presents unsupported controls as working.

## Host verification

The following checks passed on macOS with Go 1.26.5 building the Go 1.25 module:

- `go test -race ./...`
- `go vet ./...`
- frontend ESLint
- TypeScript type-check and Vite 8.1.5 production build
- `pnpm audit --audit-level high`: no known vulnerabilities
- `govulncheck ./...`: zero reachable or imported-package vulnerabilities
- shell syntax checks for installers, OpenRC scripts, VM helpers, and Proxmox
  helpers
- OpenAPI YAML parse and local Markdown-link validation
- static stripped ARM64 and AMD64 Linux builds

`govulncheck` reported one module-level advisory for the unmaintained
`golang.org/x/crypto/openpgp` package. This application does not import or call
that package, so the scanner reported zero affected vulnerabilities.

Final clean distribution artifacts:

| Artifact | Size | SHA-256 |
|---|---:|---|
| `minimalrouter-linux-arm64.tar.gz` | 7.7 MiB | `fabd8a5a5eec23175f95f067363098504af6295c1b51e278c5dc64f476d6e2a6` |
| `minimalrouter-linux-amd64.tar.gz` | 8.3 MiB | `e02bbcf92967b0d99d1e683a81fae245250801aebdb9454f9c330387cb2183ab` |

Each archive contains one current hashed JavaScript asset and one current
hashed CSS asset; the Linux executables are static, stripped, and match the
archive architecture.

## Alpine ARM64 VM evidence

Environment:

- Alpine Linux 3.22.5
- Linux `6.12.94-0-virt`, ARM64
- macOS Virtualization.framework
- ephemeral read-only installation media

Observed results:

| Check | Result |
|---|---|
| First-run setup and login | HTTP 200 |
| `routerd` identity | dedicated `routerd` user |
| `router-applyd` identity | root, Unix socket only |
| Node package/process | absent |
| Host-header/DNS-rebinding attempt | HTTP 404 |
| Request to WAN-side address | HTTP 404 |
| Wi-Fi, Cloudflare, and DoH enable attempts | HTTP 422 |
| Removed raw-key backup route | HTTP 404 |
| Commit-confirm revision | engine revision 3 = helper revision 3 |
| Global DNS sinkhole | HTTP 200, 17 entries, blocked lookup returned `0.0.0.0` |
| dnsmasq configuration | syntax check passed |
| CAKE | HTTP 200, one CAKE root qdisc plus ingress |
| fq_codel | HTTP 200, HTB root + fq_codel child plus ingress |
| QoS disable | HTTP 200, project shaping removed |
| nftables ruleset | loaded successfully |
| stateful firewall rule | present |
| privileged-helper error log | no apply errors |

The VM also returned HSTS, `no-store`, CSP, frame/cross-origin protections, and
a restrictive permissions policy. CSP no longer allows inline scripts.
`style-src 'unsafe-inline'` remains because the current React dashboard uses
inline style attributes; removing those styles is a defense-in-depth task.

## Power-loss, rollback, and throughput evidence

A second test attached a fresh 4 GiB persistent virtio disk to the ARM64 VM,
stored both application state directories on ext4. Setup and login succeeded.
The VM was force-killed during the commit-confirm window for an unconfirmed
LAN-address change. After reboot, `e2fsck` recovered the journal with exit
status 0, the persisted marker/hash and account remained valid, and boot
reconciliation restored revision 2 and `192.168.1.1/24` with no pending
transaction.

Measured whole-system memory was 140 MiB idle after reboot and 203 MiB after
setup/configuration activity. The application processes accounted for
approximately 98 MiB idle and 151 MiB under the exercised management workload.
The clean installed payload is about 60 MiB before kernel/boot files, logs, and
snapshots after the verified Wi-Fi and Cloudflare DDNS packages were added.
The complete
WireGuard integration test passed with 512 MiB; 1 GiB remains the comfortable
production recommendation.

A subsequent clean ARM64 VM installed 89 Alpine packages occupying 43,147,851
bytes (41.1 MiB), with neither Bash nor `wg-quick` present. The direct production WireGuard
lifecycle passed preflight, interface/address/route/MTU application, a real
handshake, five encrypted packets, and cleanup in an isolated Linux namespace.

An isolated network-namespace test sustained 955.9 Mbit/s through stateful
nftables/NAT with CAKE set to 1 Gbit/s. Twenty pings during load had zero loss
and 0.377 ms average latency. A synthetic WAN IPv4 scan found all TCP ports
1–10000 filtered; the fail-closed IPv6 policy did not answer host discovery.
These are virtual data-path results, not physical-NIC, PPPoE, thermal, or
Internet-origin evidence.

The full method and constraints are recorded in
[`RESOURCE_AND_HARDWARE_TEST.md`](RESOURCE_AND_HARDWARE_TEST.md).

## Current supported surface

Supported in the controlled pilot:

- HTTPS dashboard/API, authentication, sessions, CSRF, optional TOTP, rate
  limiting, audit metadata, and password-reauthenticated encrypted backups
- typed snapshot/apply/verify/commit-or-rollback pipeline
- nftables default-deny firewall and LAN-to-WAN NAT
- PPPoE generation/lifecycle, dnsmasq DHCP/DNS, WireGuard, Squid, global DNS
  sinkhole, and CAKE/fq_codel
- Cloudflare DDNS through Alpine `inadyn`
- Wi-Fi access point through `hostapd` on a compatible AP-capable radio
- preview-first pfSense import with imported NAT disabled

Explicitly unavailable:

- Cloudflare Tunnel
- DNS over HTTPS
- per-device DNS filtering
- inbound WAN port forwarding
- automatic firmware update
- factory reset

## Remaining release gates

Before replacing a household's production router:

1. Test stable physical WAN/LAN NIC identity, reboot, and power-loss recovery.
2. Establish and reconnect real PPPoE in a maintenance window.
3. Prove DHCP, DNS, NAT, WireGuard recovery, backup restore, and rollback with
   real clients.
4. Run independent external IPv4 and IPv6 scans from the WAN.
5. Run fault injection for full disk, read-only filesystem, service crash,
   interrupted transaction, and corrupted snapshot.
6. Measure sustained throughput, packet loss, latency, memory, and thermals on
   reference hardware.
7. Produce, sign, verify, and boot recovery/install media. The repository
   currently prepares an overlay but does not contain a completed Alpine
   `mkimage` profile or signed ISO.
8. Complete an independent focused penetration test and review the root
   helper's remaining syscall/filesystem confinement.

Until those gates pass, use the appliance only on an isolated bench with local
console access and an easy path back to the existing router.
