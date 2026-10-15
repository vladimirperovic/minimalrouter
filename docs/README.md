# Documentation

Minimal Router OS is currently **Beta (v0.3.3)**. The preferred AMD64/Proxmox
first-install path is the Golden Appliance ISO. Release notes for the current
release are in [`releases/v0.3.3.md`](releases/v0.3.3.md); every earlier release
is summarised in [`CHANGELOG.md`](../CHANGELOG.md) and on the GitHub Releases page.

## Install and operate

- [`INSTALLATION.md`](INSTALLATION.md) — Golden ISO install, Proxmox VM profile,
  archive install on Alpine, restoring your own settings
- [`HELP.md`](HELP.md) — what each dashboard page does
- [`WEB-UPDATE.md`](WEB-UPDATE.md) — when a release can be installed from the
  dashboard and when it needs the full installer
- [`RECOVERY.md`](RECOVERY.md) — recovery console, snapshots, password reset,
  factory reset
- [`TROUBLESHOOTING.md`](TROUBLESHOOTING.md) — safe diagnostics
- [`DNS_FILTER.md`](DNS_FILTER.md) — DNS categories, device profiles and DNS
  risk alerts
- [`DYNAMIC_DNS.md`](DYNAMIC_DNS.md) — No-IP and Cloudflare DDNS
- [`MCP.md`](MCP.md) — the read-only AI client bridge

## Status and evidence

- [`CURRENT_VALIDATION.md`](CURRENT_VALIDATION.md) — what is proven and what is not
- [`FAILURE_SCENARIOS.md`](FAILURE_SCENARIOS.md) — failure contract, appliance
  health and storage pressure

## Architecture and development

- [`../ARCHITECTURE.md`](../ARCHITECTURE.md) — control/data-plane design
- [`../PROJECT.md`](../PROJECT.md) — scope, principles, roadmap and comparison
  with pfSense/OpenWrt
- [`../DESIGN.md`](../DESIGN.md) — dashboard design and page layout rules
- [`DECISIONS.md`](DECISIONS.md) — architecture decision records
- [`CONFIGURATION-ADMISSION.md`](CONFIGURATION-ADMISSION.md) — live admission and
  offline migration
- [`GOLDEN-IMAGE.md`](GOLDEN-IMAGE.md) — ISO build/flasher/firstboot design;
  mandatory before installer changes
- [`DEVELOPMENT.md`](DEVELOPMENT.md) — development workflow and the dashboard
- [`TESTING.md`](TESTING.md) — automated tests and manual release gates
- [`../AGENTS.md`](../AGENTS.md) — rules for AI/automated contributors
- [`../api/openapi.yaml`](../api/openapi.yaml) — API contract

## Security and releases

- [`../SECURITY.md`](../SECURITY.md) — threat model and vulnerability reporting
- [`../PRIVACY.md`](../PRIVACY.md) — privacy policy
- [`RELEASE_SECURITY.md`](RELEASE_SECURITY.md) — signed tags, payload signatures,
  Golden ISO and attestations
- [`RELEASE_PROCESS.md`](RELEASE_PROCESS.md) — maintainer release process

## Documentation rule

Keep examples synthetic. Never publish real credentials, keys, backups, public
addresses, private hostnames, MAC addresses, VM inventory or household devices.
Dated lab reports and earlier release notes are kept in the git history rather
than in this tree.
