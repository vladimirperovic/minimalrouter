# Documentation

Minimal Router OS is early-alpha networking software. This private repository is the owner's active home-development line. Use it only in a controlled lab or guarded pilot with local console access and pfSense ready for rollback.

## Start here

- [`../README.md`](../README.md) — private project overview and current status.
- [`CURRENT_VALIDATION.md`](CURRENT_VALIDATION.md) — exact synchronized public baseline and remaining owner-Proxmox gates.
- [`PROXMOX_AI_HANDOFF.md`](PROXMOX_AI_HANDOFF.md) — required continuation guide for another AI agent operating the existing Proxmox VM.
- [`PROXMOX.md`](PROXMOX.md) — Proxmox VM preparation and pilot rules.
- [`STORAGE_PRESSURE.md`](STORAGE_PRESSURE.md) — bounded local state, 80% warning, 90% critical pressure, HTTP 507 behavior, WAL maintenance and log rotation.
- [`STORAGE_PRESSURE_TEST_PLAN.md`](STORAGE_PRESSURE_TEST_PLAN.md) — destructive/non-destructive validation plan for storage pressure.
- [`APPLIANCE_HEALTH.md`](APPLIANCE_HEALTH.md) — central Healthy / Warning / Degraded / Recovery required / Unknown health model.
- [`FAILURE_SCENARIOS.md`](FAILURE_SCENARIOS.md) — durable intent, IPC, power, storage, commit-confirm, firewall, PPPoE, WireGuard, update, and restore failure contract.
- [`INSTALLATION.md`](INSTALLATION.md) — controlled Alpine installation.
- [`../SECURITY.md`](../SECURITY.md) — threat model and secure defaults.
- [`RECOVERY.md`](RECOVERY.md) — local recovery, `RecoveryRequired`, canonical reconcile, and rollback procedures.
- [`TROUBLESHOOTING.md`](TROUBLESHOOTING.md) — local-console-first diagnostics.

## Existing Proxmox pilot

An owner-created Proxmox VM already exists, but its node, VM ID, bridges, addresses, and credentials are deliberately not stored in Git. Any future AI or engineer must begin with `PROXMOX_AI_HANDOFF.md`, perform read-only discovery, and stop when VM identity or bridge purpose is ambiguous.

Do not start every VM, do not place a second DHCP server on production LAN, and do not connect the candidate directly to the ISP until isolated tests and rollback rehearsal pass.

Never delete `last-transaction.json`, `pending-confirmation.json`, or `last-good.json` merely to clear an error. Preserve evidence, correct the underlying failure, and use typed canonical reconciliation or local recovery.

## Architecture and product direction

- [`../PROJECT.md`](../PROJECT.md)
- [`../ARCHITECTURE.md`](../ARCHITECTURE.md)
- [`../DESIGN.md`](../DESIGN.md)
- [`../ROADMAP.md`](../ROADMAP.md)
- [`adr/README.md`](adr/README.md)

## Installation, operation, and recovery

- [`INSTALLATION.md`](INSTALLATION.md)
- [`PROXMOX.md`](PROXMOX.md)
- [`PROXMOX_AI_HANDOFF.md`](PROXMOX_AI_HANDOFF.md)
- [`STORAGE_PRESSURE.md`](STORAGE_PRESSURE.md)
- [`STORAGE_PRESSURE_TEST_PLAN.md`](STORAGE_PRESSURE_TEST_PLAN.md)
- [`APPLIANCE_HEALTH.md`](APPLIANCE_HEALTH.md)
- [`RECOVERY.md`](RECOVERY.md)
- [`FAILURE_SCENARIOS.md`](FAILURE_SCENARIOS.md)
- [`DEVICE_PROFILES.md`](DEVICE_PROFILES.md)
- [`RELEASE_SECURITY.md`](RELEASE_SECURITY.md)
- [`TROUBLESHOOTING.md`](TROUBLESHOOTING.md)
- [`../SUPPORT.md`](../SUPPORT.md)
- [`../PRIVACY.md`](../PRIVACY.md)

## Development and testing

- [`DEVELOPMENT.md`](DEVELOPMENT.md)
- [`TESTING.md`](TESTING.md)
- [`FAILURE_SCENARIOS.md`](FAILURE_SCENARIOS.md)
- [`CURRENT_VALIDATION.md`](CURRENT_VALIDATION.md)
- [`../api/openapi.yaml`](../api/openapi.yaml)
- [`MCP.md`](MCP.md)

The synchronized shared application/runtime baseline is:

`vladimirperovic/minimalrouter@df99909a7b161b1a0bcc7149b9dfeaf6a2a51796`

That public baseline includes PR #28 and PR #29 and passed their final triggered Go, frontend, Playwright, clean-Alpine, Deep validation, CodeQL, secret-scan, performance, and OpenRC supervision workflows. See `CURRENT_VALIDATION.md` for exact commit IDs and the evidence boundary.

Public CI evidence does not replace real owner-Proxmox, PPPoE, NIC, full-disk/inode, read-only-filesystem, external-scan, restore, power-loss, or soak testing.

## Evidence and comparisons

- [`RESOURCE_AND_HARDWARE_TEST.md`](RESOURCE_AND_HARDWARE_TEST.md) — dated historical VM, memory, power-loss, and virtual network measurements.
- [`SECURITY_REVIEW.md`](SECURITY_REVIEW.md) — dated security review.
- [`COMPARISON.md`](COMPARISON.md) — scope comparison.

Dated reports are preserved as historical evidence. New Proxmox evidence must be written as a new private dated report and redacted before commit. Repository-wide current status belongs in `CURRENT_VALIDATION.md`.

## Community and project process

- [`../CONTRIBUTING.md`](../CONTRIBUTING.md)
- [`../GOVERNANCE.md`](../GOVERNANCE.md)
- [`../MAINTAINERS.md`](../MAINTAINERS.md)
- [`../CODE_OF_CONDUCT.md`](../CODE_OF_CONDUCT.md)
- [`../CHANGELOG.md`](../CHANGELOG.md)
- [`RELEASE_PROCESS.md`](RELEASE_PROCESS.md)

## Documentation rules

- Use synthetic examples and documentation-reserved addresses.
- Never include Proxmox hostnames, node names, VM IDs, raw VM configs, credentials, keys, tokens, backups, packet captures, real addresses, MAC addresses, or household device inventory.
- Mark measurements with date, environment, method, units, and limitations.
- Distinguish implementation, imported automated evidence, private target-host evidence, planned work, and unsupported features.
- Keep recovery and rollback instructions beside disruptive operations.
