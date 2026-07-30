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
- Authentic dashboard screenshot produced from the current React build with
  synthetic documentation data only.
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
- CI coverage for Go tests with the race detector, dashboard builds, and a clean
  Alpine installation/first-run wizard smoke test.
- Authenticated dashboard logs section with redacted audit events.
- Split-tunnel WireGuard client profiles and unique IPv4 `/32` peer validation.
- Regression coverage for authenticated TOTP disable behavior.
- Operator-facing NIC inventory and timezone selection in first-run setup.
- Optional isolated IoT IPv4 zone on a dedicated port or one explicit 802.1Q VLAN.
- Separate tagged IoT DHCP pool, static reservations, and LAN-to-IoT firewall isolation.
- Timezone-aware fixed-device schedules enforced in nftables before generic state and forward accepts.
- Built-in YouTube and Steam DNS/IP service groups plus a weekday-evening and weekend dashboard template.
- Zero-dependency frontend unit tests for schedule construction and network input helpers.

### Changed

- README navigation now links directly to installation, documentation, security,
  contribution, roadmap, governance, privacy, support, and release information.
- Contribution requirements now explicitly cover licensing rights, privacy,
  isolated network testing, hardware evidence, OpenAPI changes, and migrations.
- Cloudflare integrations and Wi-Fi remain disabled by default and are
  explicitly disabled during first-run setup.
- The installer immediately loads required kernel modules and enables IPv4
  forwarding instead of waiting for reboot.
- WAN WireGuard rate limiting is applied per source rather than globally.
- The quick VM harness uses a generated one-time administrator password, stores
  it with mode `0600` only when needed, and suppresses password/CSRF values from
  serial logs.
- CodeQL analyzes private preparation branches without attempting an unavailable private-repository SARIF upload; upload enables automatically after the clean repository is public.
- The installer adds timezone data, enables chronyd, and loads the 802.1Q kernel module for deterministic schedule and VLAN operation.
- The legacy dashboard label “AdGuard Filter” is now the accurate “DNS Filter”; legacy per-device AdGuard placeholders remain rejected.

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
- The public repository was launched from the reviewed clean root; the original development history remains private.

### Known limitations

- Early alpha; not supported as an unattended production firewall.
- IPv6, general VLAN/switch automation, multi-WAN, high availability, and signed recovery/update workflows are not stable release features.
- IoT VLAN mode still requires a correctly configured external trunk and real-hardware validation. YouTube/Steam rules are best-effort DNS/IP classification rather than content inspection.
- Physical dual-NIC, real ISP PPPoE, external WAN scanning, signed recovery-media
  boot, and independent penetration testing remain release evidence gaps.
- Project maintenance currently has a bus factor of one and no response-time SLA.

## Release policy

A release entry must include:

- supported installation targets;
- upgrade and rollback instructions;
- known security limitations;
- exact validation evidence;
- checksums and signatures for distributed artifacts;
- compatibility-breaking changes.
