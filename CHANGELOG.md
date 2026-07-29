# Changelog

All notable user-visible changes will be documented in this file.

The project follows the principles of [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)
and intends to adopt semantic versioning when the first stable release is
published. During early-alpha development, compatibility may change between
commits.

## [Unreleased]

### Added

- Professional public-project README and evidence-based platform comparison.
- Authentic dashboard screenshot produced from the current React build with
  synthetic documentation data only.
- Community contribution, support, conduct, documentation-index, and maintainer
  release-process documentation.
- GitHub issue and pull-request templates, CODEOWNERS, Dependabot, CodeQL, and
  secret-scanning workflows.
- Repository-hygiene CI that rejects runtime data, credentials, databases,
  private keys, backups, packet captures, and generated appliance images.
- Local-only `prepare-public-root.sh` workflow for creating and full-history
  scanning a one-commit public candidate without adding a remote or publishing.
- CI coverage for Go tests with the race detector, dashboard builds, and a clean
  Alpine installation/first-run wizard smoke test.
- Authenticated dashboard logs section with redacted audit events.
- Split-tunnel WireGuard client profiles and unique IPv4 `/32` peer validation.
- Regression coverage for authenticated TOTP disable behavior.

### Changed

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

- TOTP disable now decodes the request body before verifying the current
  administrator password, then verifies the TOTP code and revokes sessions.
- The clean-install wizard readiness test now models installer-reconciled Linux
  interface names instead of attempting an unsupported live LAN role change.
- Distribution builds create all staging subdirectories before copying binaries
  and dashboard assets.

### Security

- Removed tracked runtime configuration, snapshots, and private development
  handoff/session files from the reviewed public tree.
- Expanded ignore rules and CI enforcement for credentials, databases, backups,
  packet captures, VM disks, and generated appliance images.
- The original development repository must remain private because old commits,
  pull requests, issues, workflow logs, and artifacts are outside the reviewed
  public boundary.
- Public release remains blocked until a brand-new private repository receives
  the verified one-commit candidate, passes full-history secret scanning, and is
  explicitly approved by the owner for a separate visibility change.

### Known limitations

- Early alpha; not supported as an unattended production firewall.
- IPv6, VLAN, multi-WAN, high availability, and signed recovery/update workflows
  are not stable release features.
- Physical dual-NIC, real ISP PPPoE, external WAN scanning, signed recovery-media
  boot, and independent penetration testing remain release evidence gaps.

## Release policy

A release entry must include:

- supported installation targets;
- upgrade and rollback instructions;
- known security limitations;
- exact validation evidence;
- checksums and signatures for distributed artifacts;
- compatibility-breaking changes.
