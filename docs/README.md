# Documentation

Minimal Router OS is early-alpha networking software. Use it only in a controlled
lab or guarded pilot with local console access and an established-router rollback
path.

## Start here

- [`../README.md`](../README.md) — project overview and current status.
- [`CURRENT_VALIDATION.md`](CURRENT_VALIDATION.md) — latest automated validation,
  benchmark ranges, and remaining manual gates.
- [`FAILURE_SCENARIOS.md`](FAILURE_SCENARIOS.md) — power, process, storage, IPC,
  network, update, backup, and recovery failure contract.
- [`INSTALLATION.md`](INSTALLATION.md) — controlled Alpine installation.
- [`PROXMOX.md`](PROXMOX.md) — Proxmox VM preparation, safe boot, and pilot rules.
- [`../SECURITY.md`](../SECURITY.md) — threat model, reporting, and secure defaults.
- [`RECOVERY.md`](RECOVERY.md) — local recovery and rollback procedures.
- [`TROUBLESHOOTING.md`](TROUBLESHOOTING.md) — local-console-first diagnostics.

## Architecture and product direction

- [`../PROJECT.md`](../PROJECT.md) — scope and product principles.
- [`../ARCHITECTURE.md`](../ARCHITECTURE.md) — control/data-plane split and
  configuration lifecycle.
- [`../DESIGN.md`](../DESIGN.md) — dashboard design system.
- [`../ROADMAP.md`](../ROADMAP.md) — evidence-driven release gates.
- [`adr/README.md`](adr/README.md) — architecture decisions.

## Installation, operation, and recovery

- [`INSTALLATION.md`](INSTALLATION.md)
- [`PROXMOX.md`](PROXMOX.md)
- [`RECOVERY.md`](RECOVERY.md)
- [`FAILURE_SCENARIOS.md`](FAILURE_SCENARIOS.md)
- [`DEVICE_PROFILES.md`](DEVICE_PROFILES.md)
- [`RELEASE_SECURITY.md`](RELEASE_SECURITY.md)
- [`TROUBLESHOOTING.md`](TROUBLESHOOTING.md)
- [`../SUPPORT.md`](../SUPPORT.md)
- [`../PRIVACY.md`](../PRIVACY.md)

There is no stable signed ISO. Keep pfSense or another known-good router ready for
rollback and begin on an isolated LAN plus a test/NAT WAN.

## Development and testing

- [`DEVELOPMENT.md`](DEVELOPMENT.md) — Go and dashboard workflow.
- [`TESTING.md`](TESTING.md) — test layers, failure injection, security, performance,
  and manual Proxmox gates.
- [`FAILURE_SCENARIOS.md`](FAILURE_SCENARIOS.md) — expected outcomes and evidence
  status for disruptive scenarios.
- [`CURRENT_VALIDATION.md`](CURRENT_VALIDATION.md) — latest CI/deep-validation
  result and control-plane benchmark ranges.
- [`../api/openapi.yaml`](../api/openapi.yaml) — REST API contract.
- [`MCP.md`](MCP.md) — MCP boundary and security requirements.

The current automated baseline includes Go race/vet/vulnerability checks,
frontend lint/unit/build/E2E, clean Alpine installation and update rollback,
crash recovery, fuzzing, security analysis, ARM64 QEMU smoke tests, network
namespace validation, and performance benchmarks.

## Evidence and comparisons

- [`RESOURCE_AND_HARDWARE_TEST.md`](RESOURCE_AND_HARDWARE_TEST.md) — dated historical
  VM, memory, power-loss, and virtual network measurements.
- [`SECURITY_REVIEW.md`](SECURITY_REVIEW.md) — dated security review and remaining
  production gates.
- [`COMPARISON.md`](COMPARISON.md) — scope comparison with pfSense and OpenWrt.

Dated reports are preserved as historical evidence. New repository-wide status is
recorded in `CURRENT_VALIDATION.md`; target-host results should be added as a new
dated report rather than rewriting old measurements.

## Community and project process

- [`../CONTRIBUTING.md`](../CONTRIBUTING.md)
- [`../GOVERNANCE.md`](../GOVERNANCE.md)
- [`../MAINTAINERS.md`](../MAINTAINERS.md)
- [`../CODE_OF_CONDUCT.md`](../CODE_OF_CONDUCT.md)
- [`../CHANGELOG.md`](../CHANGELOG.md)
- [`RELEASE_PROCESS.md`](RELEASE_PROCESS.md)

## Documentation rules

- Use synthetic examples and documentation-reserved addresses.
- Never include credentials, keys, tokens, backups, packet captures, real public
  addresses, hostnames, MAC addresses, VM inventory, or household devices.
- Mark measurements with date, environment, method, units, and limitations.
- Distinguish implementation, automated evidence, target-host evidence, planned
  work, and unsupported features.
- Update documentation, OpenAPI, migrations, and screenshots with behavior changes.
- Keep recovery and rollback instructions beside disruptive operations.
