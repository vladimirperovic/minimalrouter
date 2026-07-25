# AI Agent Handoff: Minimal Router OS

This file is the continuation point for another AI agent. Read `PROJECT.md`,
`DESIGN.md`, `ARCHITECTURE.md`, `SECURITY.md`, and `docs/TESTING.md` before
changing code. Never use real ISP, administrator, WireGuard, Cloudflare, or
backup credentials in commands, logs, screenshots, fixtures, or Git.

## Current status

The control plane is ready for a controlled bench pilot, not yet for replacing
the production pfSense router. Real physical NIC assignment, the user's actual
pfSense XML, real PPPoE, recovery-media boot, throughput, and an external
penetration test remain release gates.

Implemented and tested:

- Unprivileged `routerd` and root `router-applyd` split over a bounded Unix
  socket with Linux peer-credential verification.
- Transaction pipeline with generation, preflight, checksummed snapshot,
  atomic apply, verification, commit-confirm, and rollback.
- Default-deny nftables; new inbound WAN traffic is WireGuard-only. Dashboard
  and MCP are LAN/WireGuard-only. Port forwards and DNAT fail closed.
- dnsmasq, PPPoE generation, WireGuard, and Squid privileged lifecycle paths.
- Real WireGuard client key generation, one-time `.conf`, and real PNG QR.
  The client private key is never persisted.
- Argon2id authentication, TOTP, persistent rate limits, secure cookies, CSRF,
  same-origin checks, read-only MCP sessions, response redaction, encrypted
  Argon2id + AES-GCM backups, and bounded metadata-only audit events.
- Preview-first pfSense import with explicit Linux interface mapping. NAT is
  imported disabled and unsupported sections are reported.
- Truthful runtime status. Unsupported Cloudflare, AdGuard, Wi-Fi, QoS, DoH,
  and automatic update lifecycle paths are visibly unavailable rather than
  simulated.

Git checkpoint `009d6e7` on `main` contains the main hardening work. Always
inspect `git log -1` because a newer handoff commit may follow it.

## Fast verification

From the repository root:

```sh
env GOCACHE=/private/tmp/minimalrouter-go-cache go test ./...
env GOCACHE=/private/tmp/minimalrouter-go-cache go vet ./...
cd web
pnpm exec tsc --noEmit
pnpm test
```

The Unix-socket test can require execution outside a restrictive sandbox. Do
not weaken or skip that test. For an Apple Silicon Linux cross-build:

```sh
make build-linux-arm64
file bin/routerd bin/router-applyd
```

## Clickable macOS control-plane preview

This is a simulator for UI/API/SQLite/transaction behavior only. It must not be
described as a Linux firewall test and it must never be enabled on Linux.

```sh
cd web
pnpm run build
cd ..
env GOCACHE=/private/tmp/minimalrouter-go-cache \
  go build -o /private/tmp/minimalrouter-routerd-preview ./cmd/routerd
preview_data="$(mktemp -d /private/tmp/minimalrouter-preview.XXXXXX)"
env MINIMALROUTER_PREVIEW_MODE=1 \
  MINIMALROUTER_PREVIEW_HTTP=1 \
  MINIMALROUTER_ALLOW_LOOPBACK_PREVIEW=1 \
  MINIMALROUTER_DATA_DIR="$preview_data" \
  MINIMALROUTER_WEB_DIR="$PWD/web/dist/client" \
  /private/tmp/minimalrouter-routerd-preview
```

Open `http://127.0.0.1:8080`. On a fresh temporary data directory, complete the
wizard using mock PPPoE values and a test password of at least 15 characters.
The earlier live preview was initialized with `minimalrouter-preview`, but
never assume that password for a real appliance.

The preview binds only to loopback, uses an explicitly non-Secure cookie only
for that loopback HTTP process, and displays a warning that Linux networking is
not applied. Production remains HTTPS-only with Secure cookies.

## Reproduce the Alpine ARM64 VM on Apple Silicon

The VM uses Apple's `Virtualization.framework`, not Docker and not the host
network stack. It boots an ephemeral, read-only Alpine ISO, exposes a serial
console, provides NAT outbound networking, and shares this repository read-only
as the `minimalrouter` virtiofs tag.

On 2026-07-26 the runner in `tools/macos-vm` was compiled from the committed
Swift source, ad-hoc signed with the committed entitlement, and booted Alpine
3.22.5 kernel `6.12.94-0-virt` on ARM64. The `AI_HANDOFF.md` file was visible
through virtiofs and a write probe was rejected with `Operation not permitted`.
The guest then powered off cleanly.

Prepare official Alpine 3.22.5 ARM64 assets and verify the published SHA-256:

```sh
tools/macos-vm/prepare-alpine.sh 3.22.5
make build-linux-arm64
cd web
pnpm run build
cd ..
tools/macos-vm/run-alpine.sh 3.22.5
```

At the Alpine serial console, log in as `root` (the official live image has no
password), obtain NAT networking, and mount the repository:

```sh
setup-interfaces -a
mkdir -p /mnt/minimalrouter
mount -t virtiofs minimalrouter /mnt/minimalrouter
cd /mnt/minimalrouter
```

Point `/etc/apk/repositories` only at Alpine v3.22 main/community, then install
the real service dependencies:

```sh
printf '%s\n' \
  'https://dl-cdn.alpinelinux.org/alpine/v3.22/main' \
  'https://dl-cdn.alpinelinux.org/alpine/v3.22/community' \
  > /etc/apk/repositories
apk update
apk add nftables ppp ppp-pppoe dnsmasq iproute2 curl ca-certificates \
  wireguard-tools-wg-quick squid
```

For a disposable lab install only:

```sh
MINIMALROUTER_ALLOW_UNENCRYPTED=1 sh packaging/alpine/install.sh
rc-service router-applyd start
rc-service routerd start
```

The unencrypted override is forbidden on a real appliance. This live ISO is
ephemeral: all VM changes disappear when it stops.

## Linux integration checks already demonstrated

Earlier runs of this same ARM64 VM demonstrated:

- nftables configuration preflight and atomic load.
- dnsmasq configuration validation and service start.
- WireGuard interface configuration and a real client/server handshake.
- HTTPS returned `200` through LAN and through the WireGuard tunnel.
- Direct raw-WAN HTTPS timed out.
- Squid started with its generated policy.
- Restart reconciliation re-applied canonical state.
- API transaction output redacted PPPoE and WireGuard secrets.

Repeat them after material firewall, apply-helper, WireGuard, authentication,
or packaging changes. Record exact commands and results in this file or
`docs/TESTING.md`; do not convert previous observations into permanent claims.

Useful service evidence:

```sh
rc-service router-applyd status
rc-service routerd status
tail -n 200 /var/log/router-applyd.log
tail -n 200 /var/log/router-applyd.err
tail -n 200 /var/log/routerd.log
tail -n 200 /var/log/routerd.err
nft list ruleset
wg show
ss -lntup
```

Audit metadata is in SQLite and available to an authenticated administrator at
`GET /api/v1/audit/events`. It intentionally records method, path, actor, and
result without request bodies or secrets.

## Required remaining release gates

Do these last, with the user present and a rollback cable/console available:

1. Inspect the real pfSense XML offline; make an encrypted backup and redact a
   copy for test evidence.
2. Preview import and manually review every unsupported section and interface
   mapping. Do not enable imported NAT.
3. Map physical WAN/LAN NICs by stable identity, not guessed `eth0`/`eth1`.
4. Test PPPoE on a disconnected bench or maintenance window.
5. Prove LAN DHCP/DNS/NAT, WireGuard recovery, reboot reconciliation, backup
   restore, and automatic rollback before moving the WAN cable.
6. Run an external IPv4 and IPv6 scan from an unrelated network. Only the
   selected WireGuard UDP port may answer.
7. Measure throughput, latency, packet loss, memory, thermal behavior, and
   management responsiveness under load.
8. Produce and boot signed recovery media and rehearse rollback to pfSense.

Never call the router “unbreakable” or “AI-proof.” The defensible property is a
small attack surface: WAN default deny, no WAN management, WireGuard as the only
new inbound path, least privilege, verified transactions, and rapid rollback.
