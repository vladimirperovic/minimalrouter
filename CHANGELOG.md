# Changelog

All notable user-visible changes will be documented in this file.

The project follows the principles of [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)
and intends to adopt semantic versioning when the first stable release is
published. During early-alpha development, compatibility may change between
commits.

## [Unreleased]

### Added

- Professional public-project README and evidence-based platform comparison.
- Minimal Router SVG identity used by the dashboard and README.
- Authentic dashboard screenshot produced from the React build with synthetic
  documentation data only.
- Project governance, maintainership, privacy, contribution, support, conduct,
  documentation-index, and maintainer release-process documentation.
- Controlled Alpine lab installation and safe troubleshooting guides.
- GitHub issue templates for bugs, focused features, and privacy-safe hardware
  validation reports.
- Pull-request template, CODEOWNERS, Dependabot, CodeQL, secret scanning, and
  generated release-note configuration.
- Repository-hygiene CI that rejects runtime data, credentials, databases,
  private keys, backups, packet captures, and generated appliance images.
- Local-only `prepare-public-root.sh` workflow for creating and full-history
  scanning a one-commit public candidate without adding a remote or publishing.
- Authenticated dashboard logs section with redacted audit events.
- Split-tunnel WireGuard client profiles and unique IPv4 `/32` peer validation.
- Regression coverage for authenticated TOTP disable behavior.
- Deterministic WAN/LAN interface discovery using physical-device, carrier,
  link-state, and default-route signals with mandatory operator confirmation.
- Local `router-recovery` console for password/TOTP reset, session revocation,
  LAN repair, snapshot restore, and factory reset with undo snapshots.
- DNS Filter device profiles backed by dnsmasq-populated nftables destination
  sets and validated weekday/weekend schedules.
- Default Kids profile workflow for YouTube, Steam, and Wikipedia after `19:00`
  on weekdays and all day on weekends.
- Frontend unit tests and Playwright E2E coverage for critical profile and setup
  flows.
- Ed25519 release-manifest signing, SHA-256 checksums, SPDX SBOM generation,
  GitHub artifact provenance, and explicit A/B update activation/rollback.

### Changed

- README navigation now links directly to installation, recovery, DNS Filter,
  release security, contribution, roadmap, governance, privacy, and support.
- Contribution requirements explicitly cover licensing rights, privacy,
  isolated network testing, hardware evidence, OpenAPI changes, and migrations.
- The user-facing `AdGuard Filter` label is renamed to `DNS Filter`; the historic
  `adguard` JSON key remains for compatibility only.
- The first-run wizard now uses discovered interface choices and external CSS
  instead of a large inline-style implementation.
- Content Security Policy separates stylesheet sources from legacy style
  attributes, blocks script attributes, and narrows font/worker sources.
- `router-applyd` starts with no-new-privileges, disabled dumpability, fixed
  resource limits, a fixed executable path, and sanitized loader environment.
- Cloudflare integrations and Wi-Fi remain disabled by default and are
  explicitly disabled during first-run setup.
- The installer immediately loads required kernel modules and enables IPv4
  forwarding instead of waiting for reboot.
- WAN WireGuard rate limiting is applied per source rather than globally.
- The quick VM harness uses a generated one-time administrator password, stores
  it with mode `0600` only when needed, and suppresses password/CSRF values from
  serial logs.
- CodeQL analyzes private preparation branches without attempting an unavailable
  private-repository SARIF upload; upload enables automatically after the clean
  repository is public.

### Fixed

- TOTP disable decodes the request body before verifying the current
  administrator password, then verifies the TOTP code and revokes sessions.
- The clean-install wizard readiness test models installer-reconciled Linux
  interface names instead of attempting an unsupported live LAN role change.
- Distribution builds create all staging subdirectories before copying binaries
  and dashboard assets.

### Security

- Removed tracked runtime configuration, snapshots, and private development
  handoff/session files from the reviewed public tree.
- Expanded ignore rules and CI enforcement for credentials, databases, backups,
  packet captures, VM disks, and generated appliance images.
- Recovery credential changes revoke all sessions and never expose a network
  password-reset endpoint.
- Signed update staging rejects untrusted keys, unsafe paths, symlinks,
  non-regular files, hash mismatches, duplicate versions, and package-supplied
  executable hooks.
- Device-profile rules run before established-connection acceptance so expired
  streams are not grandfathered after a schedule closes.
- The original development repository must remain private because old commits,
  pull requests, issues, workflow logs, and artifacts are outside the reviewed
  public boundary.

### Known limitations

- Early alpha; not supported as an unattended production firewall.
- IPv6, VLAN, multi-WAN, high availability, and signed bootable recovery media
  are not stable release features.
- DNS-derived service classification can be bypassed by VPNs, proxies, private
  relays, tethering, or protocols that avoid the router resolver.
- Physical dual-NIC, real ISP PPPoE, external WAN scanning, signed recovery-media
  boot, and independent penetration testing remain release evidence gaps.
- Project maintenance currently has a bus factor of one and no response-time SLA.

## Release policy

A release entry must include:

- supported installation targets;
- upgrade and rollback instructions;
- known security limitations;
- exact validation evidence;
- Ed25519 manifests, SHA-256 checksums, SPDX SBOMs, and provenance for
  distributed artifacts;
- compatibility-breaking changes.
