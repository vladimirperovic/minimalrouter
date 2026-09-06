# Documentation

Minimal Router OS is currently **Beta (v0.1.7)**. The preferred AMD64/Proxmox
first-install path is the Golden Appliance ISO.

## Start here

- [`../README.md`](../README.md) — project overview and v0.1.7 quick start
- [`ISO_INSTALLATION.md`](ISO_INSTALLATION.md) — preferred Golden ISO installation
- [`PROXMOX.md`](PROXMOX.md) — recommended VM baseline and pilot procedure
- [`GOLDEN-IMAGE.md`](GOLDEN-IMAGE.md) — exact build/flasher/firstboot design; mandatory for installer changes
- [`CURRENT_VALIDATION.md`](CURRENT_VALIDATION.md) — what is proven and what is not
- [`INSTALLATION.md`](INSTALLATION.md) — installation index plus advanced archive install
- [`SEEDING.md`](SEEDING.md) — restoring your own settings onto a fresh install
- [`RECOVERY.md`](RECOVERY.md) — recovery and rollback
- [`TROUBLESHOOTING.md`](TROUBLESHOOTING.md) — safe diagnostics

## Networking features

- [`DYNAMIC_DNS.md`](DYNAMIC_DNS.md) — No-IP and Cloudflare DDNS
- [`DEVICE_PROFILES.md`](DEVICE_PROFILES.md) — DNS Filter/device profiles
- [`APPLIANCE_HEALTH.md`](APPLIANCE_HEALTH.md) — aggregate health states
- [`STORAGE_PRESSURE.md`](STORAGE_PRESSURE.md) — disk-pressure behavior

## Architecture and development

- [`../ARCHITECTURE.md`](../ARCHITECTURE.md) — control/data-plane design
- [`../PROJECT.md`](../PROJECT.md) — scope and product principles
- [`DEVELOPMENT.md`](DEVELOPMENT.md) — development workflow
- [`TESTING.md`](TESTING.md) — automated tests and manual release gates
- [`../AGENTS.md`](../AGENTS.md) — AI/automated contributor rules
- [`../api/openapi.yaml`](../api/openapi.yaml) — API contract
- [`adr/README.md`](adr/README.md) — architecture decisions

## Security and releases

- [`../SECURITY.md`](../SECURITY.md) — threat model and vulnerability reporting
- [`RELEASE_SECURITY.md`](RELEASE_SECURITY.md) — signed tags, payload signatures, Golden ISO and attestations
- [`RELEASE_PROCESS.md`](RELEASE_PROCESS.md) — maintainer release process
- [`releases/v0.1.7.md`](releases/v0.1.7.md) — current v0.1.7 release notes
- [`releases/v0.1.6.md`](releases/v0.1.6.md) — previous v0.1.6 release notes
- [`releases/v0.1.5.md`](releases/v0.1.5.md) — earlier v0.1.5 release notes
- [`releases/v0.1.4.md`](releases/v0.1.4.md) — earlier v0.1.4 release notes
- [`FAILURE_SCENARIOS.md`](FAILURE_SCENARIOS.md) — failure-state contract
- [`../PRIVACY.md`](../PRIVACY.md) — privacy policy

## Evidence and historical reports

- [`PROXMOX_TEST_REPORT_2026-08-01.md`](PROXMOX_TEST_REPORT_2026-08-01.md) — first real PPPoE/WireGuard/pfSense fallback pilot
- [`LAB.md`](LAB.md) — isolated Proxmox lab evidence
- [`RESOURCE_AND_HARDWARE_TEST.md`](RESOURCE_AND_HARDWARE_TEST.md) — historical resource tests
- [`SECURITY_REVIEW.md`](SECURITY_REVIEW.md) — dated security review
- [`COMPARISON.md`](COMPARISON.md) — scope comparison with pfSense/OpenWrt

Dated reports are preserved for traceability. Use `CURRENT_VALIDATION.md` for the
current project status.

## Documentation rule

Keep examples synthetic. Never publish real credentials, keys, backups, public
addresses, private hostnames, MAC addresses, VM inventory or household devices.
