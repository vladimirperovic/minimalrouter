# Minimal Router OS vs OpenWrt vs pfSense — Comparison

## Resource Usage

| Metric | Minimal Router OS | OpenWrt | pfSense |
|--------|-------------------|---------|---------|
| RAM | 140 MiB idle; 203 MiB after setup/config work; 512 MiB tested minimum, 1 GiB comfortable | 64 MiB minimum, 128 MiB preferable (official device guidance) | 1 GiB minimum (official requirement) |
| Disk | ~60 MiB initial payload; use 4 GiB bench / 8 GiB production | >32 MiB flash recommended for modern use | 8 GB minimum (official requirement) |
| Dashboard | 360 KiB static SPA | LuCI is optional and device/image dependent | Included web configurator |
| Packages | 89 in the measured Alpine VM | Image/device dependent | Installation/package set dependent |
| OS base | Alpine Linux 3.22 (musl) | BusyBox (musl) | FreeBSD 14 |
| Build system | Go 1.25 + Vite/React + Alpine mkimage | C/Cross-compile (Buildroot) | Go + C (FreeBSD kernel + userland) |
| Application binaries | 18.2 MiB combined, static and stripped | Image/package dependent | Installation/package set dependent |
| Management API | REST (`/api/v1`) | UCI + LuCI HTML forms | XML API + Web GUI |

The Minimal Router values were measured in an Alpine ARM64 VM on 2026-07-28.
OpenWrt and pfSense values above are official provisioning guidance, not
identical workload measurements. Footprint does not imply equivalent feature
coverage, maturity, or security assurance. See the
[resource and hardware test](RESOURCE_AND_HARDWARE_TEST.md) for method,
limitations, and primary sources.

## Security Model Comparison

### 1. Default Firewall Policy

| Feature | Minimal Router OS | OpenWrt | pfSense |
|---------|-------------------|---------|---------|
| WAN input default | **deny** (nftables policy drop) | deny (unless custom rule added) | block (anti-lockout rules protect LAN) |
| WAN ingress | **WireGuard only** — no TCP/ICMP to WAN | Allows WAN access unless explicitly blocked | Block unsolicited WAN (but flexible) |
| Port forwards | **Forbidden** (validation rejects, nftables ignores) | Allowed by default | Allowed by default |
| LAN access | `192.168.1.0/24` only on eth1 | Any LAN | Any LAN (or VLAN) |
| Bogon/CGNAT drop | **Yes** — atomic nftables set | Requires manual firewall rules | Automatic (alias-based) |
| uRPF anti-spoofing | **Yes** (strict) | Optional | Available |
| IPv6 policy | **Fail-closed** — disabled until parity with IPv4 | Often enabled by default | Often enabled by default |

### 2. Attack Surface

| Feature | Minimal Router OS | OpenWrt | pfSense |
|---------|-------------------|---------|---------|
| Management on WAN | **Forbidden** (nftables + app-layer dual block) | Requires explicit config | Requires explicit config |
| SSH | **Disabled by default** | Available (can be enabled) | Available |
| Unused services | **Removed** from image | Many enabled by default | Many enabled by default |
| Web server | Go single binary | uhttpd ( BusyBox) | nginx + PHP-FPM |
| Package manager | Alpine apk (minimal) | opkg (BusyBox ipkg) | pkg (FreeBSD) |
| Management plane split | **Unprivileged `routerd` + privileged `router-applyd`** over Unix socket | Single process (root) | PHP-FPM + root web server |
| Privilege boundary | `routerd` runs as non-root; `router-applyd` is local-only, peer-credential verified | No process isolation | Limited (PHP in chroot) |

### 3. Authentication & Session Security

| Feature | Minimal Router OS | OpenWrt | pfSense |
|---------|-------------------|---------|---------|
| Password hashing | **Argon2id** (64 MiB, 3 iterations, 2 lanes) | Platform/version dependent | Platform/version dependent |
| Session cookies | **Secure + HttpOnly + SameSite=Strict**, 256-bit entropy, 30 min idle / 8 hr absolute | Session-based (web) | Session-based (web) |
| CSRF protection | **Mandatory** per-stateful request | Limited | Built-in |
| Rate limiting | **Per-source + global**, bounded, restart-aware | Basic (uci) | Basic |
| 2FA | **TOTP** supported | No (plugin) | Yes (plugin) |
| Response redaction | Secrets never in API responses | Possible | Manual |
| Backup encryption | **AES-256-GCM** | No built-in | AES-256 (package) |

### 4. API & Input Validation

| Feature | Minimal Router OS | OpenWrt | pfSense |
|---------|-------------------|---------|---------|
| API spec | Versioned REST API; OpenAPI coverage is still being completed | UCI (text-based) | Platform/package dependent |
| Unknown field rejection | **Yes** (`DisallowUnknownFields`) | No (text parsing) | No (JSON loose) |
| Constant-time compare | **Yes** (`crypto/subtle`) for session/token comparison | No | No |
| Parameterized storage | **SQLite** (parameterized queries) | UCI text files | PHP/PostgreSQL |
| Request size limits | Bounded, nested depth limits | No explicit limit | No explicit limit |
| Audit trail | **Always on** — immutable, metadata-only, secret redaction | Optional logging | Available (plugin) |

### 5. Network Hardening (nftables)

| Feature | Minimal Router OS | OpenWrt | pfSense |
|---------|-------------------|---------|---------|
| WAN Bogon drop | **Yes** (atomic nftables set) | Manual rules needed | Yes (alias) |
| WAN CGNAT leak protection | **Yes** (100.64.0.0/10 drop) | Manual | Manual |
| TCP MSS clamping | **Yes** (auto on WAN/PPPoE) | Manual | Yes |
| WireGuard flood guard | **Yes** (rate-limit before endpoint) | nftables only | No native |
| PersistentKeepalive | **25s** (auto-generated) | Manual | Manual |
| TCP SYN cookies | **Yes** (sysctl) | Yes | Yes |
| ICMP redirect blocking | **Yes** | Optional | Optional |
| Conntrack CT helper | **Never assigned** (no implicit openings) | Sometimes | Sometimes |
| DNS rebind protection | **Yes** (dnsmasq stop-dns-rebind) | Available | Available |
| Reverse proxy | **None** (no WAN services) | uhttpd | nginx |

### 6. Supply Chain & Updates

| Feature | Minimal Router OS | OpenWrt | pfSense |
|---------|-------------------|---------|---------|
| Go module pinning | **Yes** (go.sum, GOSUMDB) | N/A (C toolchain) | N/A |
| Frontend lockfile | **Yes** (pnpm-lock.yaml) | N/A | N/A |
| Reproducible builds | Targeted; independent reproducibility evidence is a release gate | Project-dependent | Project-dependent |
| SBOM generation | Planned | No | No |
| Firmware verification | Ed25519 verifier exists; automatic update path is disabled | Signed release mechanism | Signed release mechanism |
| Signed updates | Planned | Yes (opkg) | Yes (pkg) |
| Recovery media | Planned; signed boot evidence is a release gate | Project release images | Project release images |

## Key Differentiators

### Minimal Router OS is designed for:
- **Minimal attack surface**: Small footprint, few services, no WAN management
- **Deterministic configuration**: JSON model → nftables generation → snapshot → apply → verify
- **Zero WAN exposure**: Only WireGuard tunnel accepted on WAN, everything else drops
- **Safe rollback**: Checksumsummed snapshots, transactional apply, automatic rollback on failure
- **Least privilege**: Unprivileged API + privileged helper over authenticated Unix socket

### OpenWrt is designed for:
- **Embedded flexibility**: Runs on many hardware platforms
- **Customizability**: UCI + shell scripting, enormous package ecosystem
- **Lightweight**: Smallest footprint of all three
- **Community driven**: Large package collection and long deployment history

### pfSense is designed for:
- **Enterprise features**: VLANs, HA clustering, IPsec, extensive plugin ecosystem
- **Maturity**: Based on FreeBSD, long track record
- **Flexibility**: Rich GUI, comprehensive network management
- **Resource hungry**: Much larger footprint than Minimal Router OS or OpenWrt

Minimal Router currently has the smallest supported surface of the three, but
also the least deployment history and external review. It must not be described
as more secure than OpenWrt or pfSense solely because it is smaller.
