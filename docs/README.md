# Documentation

Minimal Router OS is early-alpha router software. Start with the short guides
below; detailed reports are kept mainly as engineering evidence.

## Start here

- [`../README.md`](../README.md) — project overview
- [`CURRENT_VALIDATION.md`](CURRENT_VALIDATION.md) — what is proven and what is not
- [`INSTALLATION.md`](INSTALLATION.md) — Alpine installation, including offline mode
- [`PROXMOX.md`](PROXMOX.md) — recommended VM baseline and pilot procedure
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
- [`../api/openapi.yaml`](../api/openapi.yaml) — API contract
- [`adr/README.md`](adr/README.md) — architecture decisions

## Security and releases

- [`../SECURITY.md`](../SECURITY.md) — threat model and vulnerability reporting
- [`FAILURE_SCENARIOS.md`](FAILURE_SCENARIOS.md) — failure-state contract
- [`RELEASE_PROCESS.md`](RELEASE_PROCESS.md) — release process
- [`RELEASE_SECURITY.md`](RELEASE_SECURITY.md) — signed-release requirements
- [`../PRIVACY.md`](../PRIVACY.md) — privacy policy

## Evidence and historical reports

- [`PROXMOX_TEST_REPORT_2026-08-01.md`](PROXMOX_TEST_REPORT_2026-08-01.md) — first real PPPoE/WireGuard/pfSense fallback pilot
- [`RESOURCE_AND_HARDWARE_TEST.md`](RESOURCE_AND_HARDWARE_TEST.md) — historical resource tests
- [`SECURITY_REVIEW.md`](SECURITY_REVIEW.md) — dated security review
- [`COMPARISON.md`](COMPARISON.md) — scope comparison with pfSense/OpenWrt

Dated reports are preserved for traceability. Use `CURRENT_VALIDATION.md` for the
current project status rather than reading every historical report.

## Documentation rule

Keep examples synthetic. Never publish real credentials, keys, backups, public
addresses, private hostnames, MAC addresses, VM inventory or household devices.
