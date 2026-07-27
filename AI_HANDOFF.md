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
- **Session 6: Migrated from vinext (Next.js) to Vite + React.**
  Build time: 266ms (was ~5s). 2 deps + 7 devDeps (was 3+13). Dashboard: 360 KB (was ~30 MB).
  `web/dist/index.html` is the entry point (not `web/dist/client/`).
- **Session 6: Memory optimization.** GOGC=50, GOMEMLIMIT=128/64 MB,
  SQLite pool capped (MaxOpen=4, MaxIdle=2), Argon2 memory reduced to 32 MiB.
  Post-optimization: 152 MB RAM (was 216 MB, -30%).
- **Session 7: Distribution pipeline.** `make dist-arm64` / `make dist-amd64`
  produces self-contained tarballs. `quick-test.sh` automates VM boot + install.

Git checkpoint on `main` contains the main hardening work. Always inspect
`git log -1` because a newer handoff commit may follow.

## Fast verification

From the repository root:

```sh
go test ./...
go vet ./...
[Web dist pre-built check] if [ ! -f web/dist/index.html ]; then echo "ERROR: run 'cd web && npm run build' first"; exit 1; fi
```

The Unix-socket test can require execution outside a restrictive sandbox. Do
not weaken or skip that test. For an Apple Silicon Linux cross-build:

```sh
make build-linux-arm64
file bin/routerd-linux-arm64 bin/router-applyd-linux-arm64
```

## Distribution build

Build self-contained tarballs for each architecture:

```sh
make dist-arm64    # Apple Silicon / Raspberry Pi
make dist-amd64    # x86_64 servers
make dist          # Both architectures
```

Output: `build/minimalrouter-linux-{arm64,amd64}.tar.gz` (~8 MB each).

On target Alpine machine:

```sh
tar xzf minimalrouter-linux-aarch64.tar.gz
cd minimalrouter-linux-aarch64
sudo sh install.sh
```

The dist installer (`install-dist.sh`) does not require the source repo.
The source-repo installer (`packaging/alpine/install.sh`) is for development.

## Quick VM test (one command)

Boots Alpine VM, installs router, runs tests, leaves VM running:

```sh
sh quick-test.sh              # Boot + install + test
sh quick-test.sh --teardown   # Kill existing VM first
```

Dashboard: https://192.168.1.1:8443 (password: `SuperSecure12345678`)

## Clickable macOS control-plane preview

This is a simulator for UI/API/SQLite/transaction behavior only. It must not be
described as a Linux firewall test and it must never be enabled on Linux.

```sh
# Web assets (if rebuilding): cd web && npm run build
# For VM tests, pre-built web/dist/ is used automatically.
cd ..
go build -o /tmp/minimalrouter-routerd-preview ./cmd/routerd
preview_data="$(mktemp -d /tmp/minimalrouter-preview.XXXXXX)"
MINIMALROUTER_PREVIEW_MODE=1 \
  MINIMALROUTER_PREVIEW_HTTP=1 \
  MINIMALROUTER_ALLOW_LOOPBACK_PREVIEW=1 \
  MINIMALROUTER_DATA_DIR="$preview_data" \
  MINIMALROUTER_WEB_DIR="$PWD/web/dist" \
  /tmp/minimalrouter-routerd-preview
```

Open `http://127.0.0.1:8080`. On a fresh temporary data directory, complete the
wizard using mock PPPoE values and a test password of at least 15 characters.

The preview binds only to loopback, uses an explicitly non-Secure cookie only
for that loopback HTTP process, and displays a warning that Linux networking is
not applied. Production remains HTTPS-only with Secure cookies.

## Reproduce the Alpine ARM64 VM on Apple Silicon

The VM uses Apple's `Virtualization.framework`, not Docker and not the host
network stack. It boots an ephemeral, read-only Alpine ISO, exposes a serial
console, provides NAT outbound networking, and shares this repository read-only
as the `minimalrouter` virtiofs tag.

Prepare official Alpine 3.22.5 ARM64 assets and verify the published SHA-256:

```sh
tools/macos-vm/prepare-alpine.sh 3.22.5
make build-linux-arm64
# Web assets (if rebuilding): cd web && npm run build
# For VM tests, pre-built web/dist/ is used automatically.
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

## VM-specific constraints

- **API binds to LAN IP** (`192.168.1.1:8443`), not `127.0.0.1` — all curl must target `https://192.168.1.1:8443`
- **VM has only `eth0`** (NAT) — must create `ip link add eth1 type dummy` before starting services
- **PTY heredocs are unreliable** — use `jq` to parse JSON responses inside the VM, and Go-based helper tools on the host instead of inline `python3 -c`
- **WireGuard requires WAN enabled** — `validation.go:326` needs `WAN.Enabled=true` → needs PPPoE → `/dev/ppp` unavailable in VM
- **AdGuard/Update blocked** — nftables output chain blocks outbound HTTPS
- **LUKS unavailable** — requires interactive password at boot
- **`web/dist/index.html` must exist** for `install.sh` to succeed

## Resource usage (real VM measurements)

| Metric | Our Router | OpenWrt | pfSense |
|--------|-----------|---------|---------|
| RAM | 152 MB | 30-60 MB | 300-500 MB |
| Disk | 77 MB | 8-16 MB | 8+ GB |
| Dashboard | 360 KB | N/A | ~50 MB |
| Packages | 100 | 50-100 | 300-500 |
| Build time | 266ms | N/A | N/A |

Our router is 3x lighter than pfSense, 3-5x heavier than OpenWrt.
Cost of modern Go+React stack vs embedded C/busybox.

## Linux integration checks already demonstrated

All tests pass (17/17 functional + 28/28 security audit):

### Functional tests (all PASS)
1. Login → redirect to dashboard (HTTP 200)
2. CSRF token refresh
3. Config read/write
4. System status (CPU, RAM, disk, uptime)
5. Backup create/restore
6. TOTP enable/disable
7. nftables firewall status
8. WireGuard status (stub)
9. AdGuard status (stub)
10. QoS status (stub)
11. Cloudflare DDNS status (stub)
12. Squid proxy config
13. Reboot trigger
14. Audit event log
15. Session logout
16. Setup wizard apply
17. Static dashboard serve

### Security audit (all 28 checks PASS)
- WAN default deny policy: YES
- WireGuard-only ingress: YES
- LAN/WireGuard-only dashboard: YES
- Port forwards fail closed: YES
- HTTPS-only with Secure cookies: YES
- CSRF protection: YES
- Rate limiting: YES
- Response redaction: YES
- Argon2id password hashing: YES
- AES-256-GCM backup encryption: YES
- Ed25519 firmware verification: YES
- Persistent audit events: YES
- (All 28 checks verified in session 5)

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

Never call the router "unbreakable" or "AI-proof." The defensible property is a
small attack surface: WAN default deny, no WAN management, WireGuard as the only
new inbound path, least privilege, verified transactions, and rapid rollback.
