# Security

## 1. Security objective

Minimal Router OS is a network security boundary. A defect in its management
plane can expose every device behind it, so secure defaults and safe recovery
are product requirements, not optional hardening.

This document is both the initial threat model and the minimum security bar for
version 1.

## 2. Supported security boundary

The project protects against:

- Unsolicited traffic and active attackers on the WAN
- Untrusted or compromised devices on the LAN
- Cross-site requests and hostile web content targeting an authenticated admin
  browser
- Malformed API input and configuration injection
- Brute-force authentication attempts
- Accidental administrator lockout caused by a bad configuration
- Tampered or incompatible project updates
- Secret leakage through normal logs, API responses, backups, or file
  permissions
- Compromise of an unprivileged management process, by minimizing what it can
  do directly

The following are not fully mitigated in version 1:

- An attacker with persistent physical access
- A compromised BIOS, hypervisor, kernel, or root account
- Bootloader compromise or lack of UEFI Secure Boot enforcement
- Single-user mode local console attacks (deferred to V2 encrypted root/locked bootloader)
- LAN-side Layer 2 attacks like ARP spoofing or rogue DHCP servers (deferred to V2 managed VLAN switch integration)
- Malicious hardware or firmware
- Traffic analysis by the ISP
- Denial of service that saturates the physical WAN link

Out-of-scope threats must not be described as solved.

## 3. Trust zones

| Zone | Trust level | Default policy |
|---|---|---|
| WAN | Untrusted | No management access; deny unsolicited input |
| LAN clients | Partially trusted | Internet forwarding allowed by policy; management requires authentication |
| Admin browser | Authenticated, not inherently safe | Same-origin HTTPS, CSRF protection, short-lived session |
| `routerd` | Unprivileged | No arbitrary service or filesystem control |
| `router-applyd` | Privileged and high risk | Local-only, authenticated, allowlisted operations |
| Canonical store | Sensitive | Local ownership, strict permissions, transactional access |
| Build and update system | High trust | Reproducible inputs, signed artifacts, protected keys |

Direct management exposure on WAN is forbidden. Remote administration first
establishes WireGuard, then reaches the same HTTPS management service through
the authenticated tunnel.

## 4. Secure defaults

- Accept management requests only when their local destination is an intended
  LAN or WireGuard address, in addition to the nftables boundary.
- Redirect no plaintext login page; serve management over HTTPS only.
- Deny WAN input and deny forwarding unless a generated rule permits it.
- Disable SSH by default.
- Disable unused services and remove unused packages from the image.
- Run each service under a dedicated account where supported.
- Mount writable paths narrowly and keep application code read-only where
  practical.
- Use restrictive `umask` and explicit ownership for generated files.
- Do not enable UPnP, WPS, remote shell, telemetry upload, or cloud management
  implicitly.
- Do not ship a default password.

### 4.1 pfSense Security Hardening Controls

Minimal Router OS automatically enforces key pfSense enterprise security protections in its core network generation pipelines:

1. **Bogon, CGNAT & Multicast WAN Input Drop**: Incoming packets on WAN interfaces claiming to originate from private (`10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`), loopback (`127.0.0.0/8`), CGNAT (`100.64.0.0/10`), or multicast (`224.0.0.0/4`) IP blocks are atomically dropped by `nftables`.
2. **WAN Output Bogon Leak Protection**: Outgoing packets on WAN attempting to leak internal private or spoofed IP source addresses are dropped in `chain output`.
3. **uRPF Strict Anti-Spoofing**: `nftables` enforces `fib saddr . iif oif missing drop` to discard spoofed interface packets.
4. **IPv6 Fail-Closed Policy**: IPv6 is disabled at sysctl and dropped on WAN
   until it has complete policy parity with IPv4.
5. **DNS Rebind Attack Protection**: `dnsmasq` enforces `stop-dns-rebind` to prevent malicious external DNS responses from mapping public domains to local private LAN IPs or loopback (`127.0.0.1`).
6. **No WAN TCP or Ping Service**: New WAN TCP and ICMP traffic has no accept
   rule and reaches the default drop policy.
7. **WireGuard Flood Guard**: New WireGuard UDP packets are rate-limited before
   reaching the endpoint; excess packets are dropped.
8. **TCP MSS Clamping (PMTU Discovery)**: Automatic MSS clamping (`tcp flags syn tcp option maxseg size set rt mtu`) on WAN/PPPoE interfaces prevents packet fragmentation attacks and connection stalls.
9. **WireGuard PersistentKeepalive Enforcement**: `PersistentKeepalive = 25` is automatically generated for all active WireGuard peers to maintain tunnel state behind stateful NAT firewalls.
10. **No WAN Port Forwards**: Enabled port-forward entries are rejected by
    validation and are also ignored by the nftables generator as defense in
    depth. pfSense NAT entries import disabled.

### 4.2 Kernel Hardening (sysctl)

Minimal Router OS applies aggressive Linux kernel-level defaults before the `nftables` ruleset even loads:

- **TCP SYN Cookies**: `net.ipv4.tcp_syncookies = 1` enabled to protect against SYN floods before firewall rules process packets.
- **ICMP Redirects**: Disable accepting and sending ICMP redirects (`net.ipv4.conf.all.accept_redirects = 0`, `net.ipv4.conf.all.send_redirects = 0`) to prevent routing table manipulation attacks.
- **Source Routing**: Disable IP source routing (`net.ipv4.conf.all.accept_source_route = 0`).
- **Kernel Pointer Hiding**: `kernel.kptr_restrict = 2` and `kernel.dmesg_restrict = 1` to prevent unprivileged users (`routerd`) from inspecting kernel symbols or memory logs.
- **BPF JIT Hardening**: `net.core.bpf_jit_harden = 2` to mitigate eBPF JIT spraying attacks.
- **No Conntrack Helper Assignment**: The generated nftables policy never
  assigns an application-layer `ct helper`, so `related` does not create
  implicit FTP/SIP-style WAN openings. This avoids relying on a sysctl removed
  from some Alpine kernels.
- **Unprivileged Kernel Attack-Surface Reduction**: Unprivileged BPF is
  disabled until reboot, protected link/FIFO/file sysctls are enabled, and
  proxy ARP/route-localnet are disabled.

## 5. Authentication

Version 1 has one local administrator account.

- The password is created during the first-run wizard.
- Require a minimum of 15 characters and allow at least 64 characters,
  whitespace, Unicode, and password-manager-generated values.
- Do not require arbitrary character-class rules or periodic password changes.
- Never truncate a password silently.
- Hash passwords with Argon2id and a cryptographically random salt.
- Store the algorithm, version, salt, and cost parameters with the hash so they
  can be upgraded.
- Use constant-time comparison for verifier output.
- Re-authenticate before password changes, backup export, factory reset, or
  update-channel changes.

Initial Argon2id baseline for reference hardware:

- Memory: 64 MiB
- Iterations: 3
- Parallelism: benchmarked and capped to available CPU, from 1 to 4 lanes
- Output: 32 bytes
- Salt: at least 16 random bytes

The release build must benchmark login on the lowest supported hardware and
document any parameter change. Cost reductions require a security review.

Authentication responses are generic. Login attempts are rate-limited per
source and globally using bounded, restart-aware state. Avoid permanent account
lockout, which can become a local denial-of-service vector.

There is no insecure password-recovery path. Recovery requires authenticated
local console access and must invalidate all sessions.

## 6. Sessions and browser security

- Generate opaque session IDs from at least 256 bits of operating-system
  randomness.
- Store session state server-side; do not put credentials or authorization
  decisions in client-readable tokens.
- Set cookies with `Secure`, `HttpOnly`, `SameSite=Strict`, `Path=/`, and no
  `Domain` attribute.
- Rotate the session ID after login, re-authentication, and privilege-sensitive
  changes.
- Invalidate all sessions on password reset or administrator recovery.
- Use a 30-minute idle timeout and an 8-hour absolute timeout.
- Do not place session IDs or CSRF tokens in URLs or logs.

All HTTP responses must include strict security headers:
- `Strict-Transport-Security: max-age=63072000; includeSubDomains; preload` (HSTS)
- `X-Content-Type-Options: nosniff`
- `X-Frame-Options: DENY`
- `Cross-Origin-Opener-Policy: same-origin`
- `Cross-Origin-Resource-Policy: same-origin`

All state-changing browser requests require:

1. A valid authenticated session.
2. A per-session CSRF token in a custom header.
3. A valid same-origin `Origin` value when browsers send it.
4. A JSON content type for API mutations.

CORS is disabled by default. If ever enabled, it must use an explicit origin
allowlist and must never combine wildcard origins with credentials.

The UI ships a restrictive Content Security Policy, denies framing, avoids
inline scripts, and uses dependency pinning. Host headers are validated to
reduce DNS-rebinding attacks against the LAN interface.

## 7. TLS

- Use Go's standard TLS implementation and supported secure defaults.
- Permit only TLS 1.2 and TLS 1.3 unless platform requirements become stricter.
- Generate a unique device certificate/key during installation; never reuse an
  image-wide private key.
- Store private keys with owner-only permissions.
- Make certificate fingerprints visible during local setup.
- Never silently downgrade to HTTP.

The trusted-certificate workflow and certificate rotation require an ADR before
implementation. HTTPS protects transport even when a browser cannot initially
validate a self-issued device certificate; the product must explain that state
clearly and support fingerprint verification.

## 8. API and input handling

- Define the API in OpenAPI and parse into strict typed models.
- Reject unknown security-sensitive fields.
- Limit request body size, collection size, string length, and nesting depth.
- Validate enums, numeric ranges, IP/CIDR values, ports, interface identifiers,
  hostnames, and cross-field invariants.
- Use constant-time byte comparisons (`crypto/subtle.ConstantTimeCompare` in Go) for validating session IDs, API tokens, and CSRF header tokens to prevent timing attacks.
- Use parameterized SQL only.
- Return minimal errors without stack traces, paths, secrets, or command output.
- Require a revision/ETag on configuration updates to prevent stale writes.
- Require idempotency keys for mutation retries.
- Rate-limit expensive endpoints and serialize apply operations.

API validation is necessary but not sufficient: the configuration model and
privileged helper validate again at their trust boundaries.

### 8.1 AI and MCP boundary

AI output is untrusted input, regardless of model capability. A prompt
injection, malicious web page, or compromised MCP client must not become router
administrator authority by default.

- MCP advertises only read tools unless the operator locally sets
  `MINIMALROUTER_MCP_MODE=admin`.
- Default MCP login requests a read-only API session. The API stores that
  privilege with the session and rejects all state-changing methods.
- Returned configuration redacts PPPoE, WireGuard, Cloudflare, Squid, and Wi-Fi
  secrets.
- MCP uses a pinned router certificate, requires HTTPS, and has no WAN
  listener.
- Admin MCP mode is not considered safe for unattended browsing or processing
  untrusted content. It is a deliberate administrator delegation.

## 9. Command and configuration safety

- Never invoke a shell with user-controlled input.
- Use direct process execution with fixed binary paths and separate arguments.
- Maintain an allowlist of binaries, subcommands, flags, file paths, interface
  names, and environment variables.
- Start child processes with a minimal explicit environment.
- Reject path traversal, symlinks, device files, and unexpected ownership.
- Write candidates in private directories and install them atomically.
- Use fixed templates or structured libraries; user input is data, never
  configuration language.
- Apply nftables rules to a project-owned table and use atomic ruleset loading.
- Run service-specific preflight checks before activation.

The privileged helper must not expose generic operations such as “run command,”
“write file,” “restart arbitrary service,” or “load supplied nftables script.”

## 10. Least privilege and process isolation

`routerd` runs as a dedicated unprivileged user. It can read only the state
required for the UI and communicate with `router-applyd` through a protected
Unix socket.

`router-applyd` currently runs as root because it configures interfaces,
nftables, sysctls, and system services. Its authority is reduced at the
application boundary rather than by a completed Linux capability/seccomp
profile. It:

- Accepts local requests only.
- Verifies Unix peer credentials.
- Uses a small, versioned request schema.
- Applies size, duration, and concurrency limits.
- Has no network listener.
- Produces structured, redacted results.

Filesystem, namespace, capability, and syscall confinement beyond the current
OpenRC user/permission boundary remains a release-hardening item. The retained
root requirement must not be described as eliminated.

## 11. Network policy

- WAN management is blocked independently by nftables and by application-layer
  local-destination validation.
- nftables policy is generated deterministically from a typed model.
- Established/related behavior is explicit.
- Invalid packets are dropped.
- Interface role changes require commit-confirmed rollback.
- WAN port forwards are forbidden; WireGuard is the only accepted new WAN
  ingress.
- Source/destination ranges, overlapping networks, broadcast addresses, and
  reserved values are validated.
- dnsmasq binds only to selected LAN interfaces.
- Cloudflare integration remains disabled. Firmware verification is fail-closed
  unless a trusted signing key is provisioned.

IPv6 must have policy parity with IPv4 before it is enabled. If unsupported in a
release, it is disabled or explicitly blocked at every relevant boundary.

## 12. Secret handling

Secrets include:

- Administrator password verifier
- PPPoE credentials
- WireGuard private and preshared keys
- Cloudflare API and tunnel tokens
- TLS private keys
- Session and CSRF tokens
- Backup-encryption material

Requirements:

- Never log secrets or include them in metrics, panic reports, URLs, or audit
  diffs.
- API read responses return presence/status or a redacted placeholder, not the
  stored value.
- Secret updates use write-only fields.
- Store secret files with owner-only permissions and explicit ownership.
- Keep secret lifetimes in memory short and avoid unnecessary copies.
- Do not place production secrets in environment files, source control, image
  layers, test fixtures, or command-line arguments visible in process listings.
- Backup exports must be encrypted with a reviewed standard and library.

Never invent encryption schemes. Local encryption at rest does not protect
against a root attacker unless keys are anchored outside the filesystem, so the
UI and documentation must not overstate its guarantees.

## 13. Snapshots and rollback security

- Snapshot creation and restore are privileged, authenticated operations.
- Snapshots are immutable, checksummed, versioned, and bounded by retention.
- Audit metadata identifies who initiated a change, when, and which fields
  changed; secret values are excluded.
- Restore validates schema compatibility and all component preflights.
- A snapshot from a newer incompatible version is rejected safely.
- Failed apply and interrupted boot recover to the last known-good revision.
- Disruptive changes require confirmation from the new configuration before
  commit.

Checksums detect accidental corruption; they do not make locally writable
snapshots trusted against root compromise.

## 14. Updates and supply chain

- Pin Go modules, frontend packages, Alpine release branches, and build tools.
- Commit lockfiles and verify checksums.
- Generate an SBOM for release images.
- Run dependency and vulnerability scanning in CI.
- Build releases in an isolated, documented pipeline.
- Sign project packages, manifests, and release metadata.
- Keep signing keys outside source control and CI logs.
- Verify signatures before installation; TLS alone is insufficient.
- Never use Alpine's `--allow-untrusted` in installation or update paths.
- Create a pre-update snapshot and test boot/health before declaring success.
- Publish supported-version and security-update windows before public release.

### 14.1 Go Compiler & Binary Hardening

All Go executables (`routerd`, `router-applyd`, `minimalrouter-mcp`) must be compiled using strict hardening flags:

- **Static Binary Compilation**: `CGO_ENABLED=0` to eliminate C-library dependency attack surfaces.
- **No Foreign Dynamic Loader**: Release binaries are pure-Go static ELF files
  and do not require glibc compatibility packages on musl-based Alpine. A
  static-PIE build may replace this only after it is produced by a pinned musl
  toolchain and executed in the Alpine release test.
- **Symbol & Path Strip**: `-trimpath -ldflags="-s -w"` to strip local filesystem paths, build flags, and debugging symbols.
- **Checksum Verification**: `GOSUMDB` enabled to verify all Go module dependencies against the global checksum database.

## 15. Logging and auditing

Record:

- Successful and failed login events without password data
- Session creation, revocation, and recovery
- Configuration transaction actor, revision, outcome, and redacted diff
- Backup/export, restore, update, and factory reset events
- Privileged-helper operations and failures
- Security-relevant service state changes

Logs use UTC timestamps, bounded retention, restrictive permissions, and safe
rotation. The UI escapes all log-derived text. Export requires re-authentication
and redaction.

## 16. Development security requirements

Every security-sensitive change requires:

- Threat review covering affected trust boundaries
- Unit tests for validation and authorization
- Negative tests for malformed and adversarial input
- Integration tests for the real Linux component
- Rollback and interrupted-apply tests
- Review by someone other than the author before release

CI must include:

- Go formatting, vetting, tests, race tests where practical, and static analysis
- Frontend lint, type checking, tests, and dependency audit
- Secret scanning
- Dependency vulnerability scanning
- Generated-file and migration consistency checks
- Image/package scanning
- End-to-end authentication, CSRF, authorization, apply, and rollback tests

No release ships with unresolved critical or high-severity findings unless the
risk is documented, shown not to affect the shipped product, and explicitly
accepted.

## 17. Security release gates

Version 1 cannot be released until:

- WAN management is proven blocked in the test matrix.
- Authentication, session rotation, CSRF, rate limiting, and recovery are
  covered by end-to-end tests.
- The privileged helper exposes only allowlisted typed operations.
- All supported configuration changes pass apply/rollback fault injection.
- Backups containing secrets are encrypted.
- Updates and project artifacts are signature-verified.
- Default services, ports, accounts, and file permissions are documented and
  tested.
- An independent review or focused penetration test has been completed.
- A private vulnerability-reporting process and response owner exist.

## 18. Reporting a vulnerability

Do not place vulnerability details in a public issue. Use the repository's
private GitHub security-advisory reporting channel. If that channel is not yet
enabled, contact the repository owner privately and wait for a secure reporting
method.

Before a public release, this section must contain an actively monitored
security contact and a documented response target.

## 19. References

- [OWASP Authentication Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html)
- [OWASP Session Management Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html)
- [OWASP CSRF Prevention Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Cross-Site_Request_Forgery_Prevention_Cheat_Sheet.html)
- [OWASP REST Security Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/REST_Security_Cheat_Sheet.html)
- [RFC 9106: Argon2](https://www.rfc-editor.org/rfc/rfc9106.html)
- [Go Argon2id package](https://pkg.go.dev/golang.org/x/crypto/argon2)
- [nftables atomic ruleset operations](https://wiki.nftables.org/wiki-nftables/index.php/Operations_at_ruleset_level)
- [Alpine package management](https://wiki.alpinelinux.org/wiki/Apk)
