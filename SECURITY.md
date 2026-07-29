# Security policy

Minimal Router OS is a network security boundary. Security defects can affect
every device behind the router, so the project treats secure defaults, least
privilege, recovery, and honest limitation reporting as core requirements.

The project is currently **early alpha**. No version is supported as an
unattended production firewall unless a future release explicitly states that
it has reached that level and publishes the corresponding validation evidence.

## Supported versions

There is no stable supported release yet.

| Version | Security support |
|---|---|
| `main` / early-alpha builds | Best-effort development fixes; no SLA |
| Unofficial forks or modified images | Not supported by this project |

Security fixes may require incompatible configuration or installation changes
while the project remains in alpha.

## Reporting a vulnerability

Do not disclose a vulnerability in a public issue, discussion, pull request,
commit message, screenshot, or log attachment.

Preferred reporting method:

1. Use GitHub's **Report a vulnerability** / private security advisory feature
   for this repository.
2. Include the affected commit or version, impact, reproduction steps, and a
   minimal proof of concept.
3. Remove real credentials, keys, public IP addresses, hostnames, MAC addresses,
   device names, and private network inventory.
4. State whether the issue is exploitable from WAN, LAN, an authenticated admin
   session, local console, or only after root access.

When private vulnerability reporting is unavailable, open a public issue that
contains only a request for private contact. Do not include technical details.

The maintainer will acknowledge reports when practical, assess severity, prepare
a fix, and coordinate disclosure. There is currently no guaranteed response or
remediation time.

## Security objectives

The project aims to:

- deny unsolicited WAN traffic by default;
- keep management unavailable directly from WAN;
- require WireGuard before remote management;
- keep packet forwarding in the Linux kernel rather than the web application;
- separate the unprivileged management process from privileged network changes;
- validate configuration at multiple trust boundaries;
- generate deterministic service configuration rather than executing arbitrary
  shell fragments;
- snapshot and roll back disruptive changes;
- avoid default credentials and require an administrator password during setup;
- redact secrets from normal API responses, logs, diagnostics, and audit events;
- keep optional network-facing features disabled until explicitly configured;
- fail closed when a feature or required runtime adapter is unavailable.

These are design objectives and tested properties in specific environments, not
a guarantee that the software contains no vulnerabilities.

## Trust boundaries

| Component or zone | Trust assumption | Security expectation |
|---|---|---|
| WAN | Untrusted | Default deny; no web management |
| LAN clients | Partially trusted | Only required DHCP, DNS, ICMP, and authenticated management paths |
| Admin browser | Authenticated but exposed to hostile web content | HTTPS, secure cookies, same-origin checks, CSRF protection, bounded sessions |
| `routerd` | Network-facing and potentially compromisable | Runs unprivileged; no arbitrary command execution or direct service-file writes |
| `router-applyd` | Highly privileged local helper | No network listener; typed, bounded, allowlisted operations over a protected Unix socket |
| SQLite state | Sensitive local state | Restrictive ownership, transactions, bounded access, secret-aware exports |
| Build and release pipeline | High trust | Pinned dependencies, CI, static analysis, secret scanning, checksums, and future signed releases |

## Secure defaults

The default and first-run configuration is intended to provide:

- WAN input policy `drop`;
- stateful firewalling and NAT only where generated policy permits it;
- management HTTPS on the selected LAN address, not on WAN;
- SSH, UPnP, plaintext management, Cloudflare integrations, Wi-Fi AP, Squid,
  QoS, and WireGuard disabled until explicitly configured;
- IPv6 disabled and blocked until it has policy parity with IPv4;
- no WAN port forwards in the current secure appliance profile;
- DNS and DHCP bound to the selected LAN interface;
- unique per-device TLS material rather than an image-wide private key;
- no shipped administrator password.

## Authentication and browser security

The management plane uses:

- Argon2id password hashing with random salts;
- server-side opaque sessions;
- `Secure`, `HttpOnly`, and `SameSite=Strict` cookies in production;
- idle and absolute session expiration;
- per-source and global login rate limiting;
- CSRF tokens for state-changing browser requests;
- same-origin and JSON content-type checks;
- optional TOTP two-factor authentication;
- session revocation after password or TOTP changes;
- restrictive security headers and host/destination validation.

Authentication errors should not reveal whether a password, session, or TOTP
component was specifically correct.

## Privileged operations

API handlers and the UI must never:

- execute user-controlled text through a shell;
- accept arbitrary command names, arguments, service names, or file paths;
- write Linux service configuration directly;
- load a caller-supplied nftables program;
- restart arbitrary services;
- treat AI-generated output as trusted administrator authority.

All configuration changes must follow the typed pipeline:

```text
input → validation → model → generation → preflight → snapshot
      → apply → verification → commit or rollback
```

`router-applyd` currently runs as root because it configures interfaces,
firewall rules, sysctls, and system services. Its authority is reduced at the
application boundary, but a complete capability, namespace, or seccomp profile
is not yet a finished release feature.

## Secret handling

Never commit or publish:

- PPPoE usernames or passwords;
- administrator passwords or hashes;
- session identifiers or CSRF tokens;
- WireGuard private or preshared keys;
- Cloudflare or other provider tokens;
- Wi-Fi or Squid passwords;
- encrypted-backup passwords or plaintext backups;
- runtime SQLite databases, configuration files, or snapshots;
- real pfSense XML exports;
- packet captures containing private traffic;
- screenshots or logs containing public IPs, hostnames, MAC addresses, device
  names, QR codes, or network inventory.

Runtime state belongs under the appliance data directory and must not be stored
in the source repository.

If a secret is committed, removing the latest file is not sufficient. Rotate the
secret. If the repository also contains private pull requests, issues, workflow
logs, or artifacts, rewriting Git history may still be insufficient for safe
publication. Preserve that repository privately and publish from a brand-new,
reviewed repository instead.

## Backups and diagnostics

Backup exports can contain credentials and private keys. They must be encrypted,
handled as secrets, and never attached to public issues.

Diagnostics and logs should be metadata-focused and redacted. A bug report
should include only the minimum information required to reproduce the issue.

## AI and MCP boundary

AI output is untrusted input. A prompt injection or compromised client must not
become router administrator authority by default.

- MCP access is read-only unless the operator deliberately enables an
  administrator mode.
- State-changing API requests still require normal authorization and validation.
- Configuration returned to tools must redact secrets.
- The MCP process must not listen on WAN.
- Unattended administrator-mode AI control is not a supported secure deployment.

## Supply-chain security

The public repository uses or plans to use:

- pinned Go modules and a committed `go.sum`;
- a locked frontend dependency graph;
- CI for tests, race detection, linting, production builds, repository hygiene,
  and clean Alpine installation;
- CodeQL analysis;
- Dependabot update pull requests;
- current-tree and full-history secret scanning;
- checksums for distributed test artifacts;
- signed release and recovery artifacts before stable production claims.

A CI pass does not replace code review, threat analysis, hardware testing, or
external security assessment.

## Out-of-scope or incomplete protections

The current project does not claim complete protection against:

- persistent physical access;
- compromised firmware, bootloader, hypervisor, kernel, or root account;
- malicious hardware;
- denial of service that saturates the WAN link;
- traffic analysis by the ISP;
- LAN Layer-2 attacks such as rogue DHCP or ARP spoofing;
- unsupported IPv6 traffic, VLAN topologies, multi-WAN, or high availability;
- supply-chain compromise outside the project's verified build inputs;
- weak administrator operational practices;
- undiscovered implementation defects.

Disk encryption, Secure Boot enforcement, signed recovery media, production
update rollback, stronger `router-applyd` confinement, and independent
penetration testing remain release work.

## Public repository release gates

Before a private development tree is published as a new public repository:

- preserve the complete original development repository as a private archive;
- export only the reviewed source tree into a brand-new repository with one root
  commit and no inherited pull requests, issues, tags, workflow logs, or artifacts;
- rotate every credential that appeared in private history or metadata;
- verify that no runtime state, internal handoff material, private remote URL, or
  real network inventory is present;
- pass a current-tree and full-history secret scan;
- pass repository-hygiene checks, Go race tests, vet, dashboard lint/build,
  CodeQL, and a clean Alpine installation test on the exact release commit;
- review the rendered README, screenshot, license, support policy, contribution
  guide, security policy, and comparison claims;
- keep the new repository private until all checks pass and the owner explicitly
  approves the separate visibility change.

The maintainer procedure is documented in
[`docs/RELEASE_PROCESS.md`](docs/RELEASE_PROCESS.md).

## Production-readiness gates

Public source availability does not make the router production-ready. Before a
future release is recommended as a household production router, the project must
also:

- verify PPPoE, DHCP, DNS, NAT, WireGuard, reboot reconciliation, backup restore,
  rollback, and recovery on supported physical hardware;
- perform independent external IPv4 and IPv6 scanning from an unrelated network;
- run fault injection for full disk, read-only filesystem, service crash,
  interrupted transaction, and corrupted snapshot;
- publish measured sustained throughput, latency, memory, thermals, and failure
  behavior on reference hardware;
- boot and verify signed recovery media;
- complete an independent focused penetration test;
- document known limitations without comparison-based security claims.

## Security acknowledgements

Responsible reporters may be credited in release notes with their permission.
The project does not currently operate a bug bounty program.
