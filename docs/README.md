# Documentation

Minimal Router OS is early-alpha networking software. Use it only in a controlled
lab or guarded pilot with local console access and an established-router rollback
path.

## Start here

- [`../README.md`](../README.md) — project overview and current status.
- [`CURRENT_VALIDATION.md`](CURRENT_VALIDATION.md) — latest automated validation,
  target-host evidence and remaining manual gates.
- [`PROXMOX_TEST_REPORT_2026-08-01.md`](PROXMOX_TEST_REPORT_2026-08-01.md) — first
  successful owner-Proxmox PPPoE/Internet/performance/load/WireGuard/fallback
  pilot, including the Alpine `linux-lts` PPPoE finding.
- [`DYNAMIC_DNS.md`](DYNAMIC_DNS.md) — No-IP and Cloudflare DDNS configuration,
  credential model, apply lifecycle and safe diagnostics.
- [`INSTALLATION.md`](INSTALLATION.md) — controlled Alpine installation and PPPoE
  kernel preflight.
- [`PROXMOX.md`](PROXMOX.md) — Proxmox VM preparation, `linux-lts` baseline, safe
  boot and pilot rules.
- [`APPLIANCE_HEALTH.md`](APPLIANCE_HEALTH.md) — central Healthy / Warning /
  Degraded / Recovery required appliance status and its evidence sources.
- [`STORAGE_PRESSURE.md`](STORAGE_PRESSURE.md) — bounded local state, 80%/90%
  disk-pressure behavior, WAL maintenance and fail-closed durable writes.
- [`FAILURE_SCENARIOS.md`](FAILURE_SCENARIOS.md) — power, process, storage, IPC,
  network, update, backup and recovery failure contract.
- [`../SECURITY.md`](../SECURITY.md) — threat model, reporting and secure defaults.
- [`RECOVERY.md`](RECOVERY.md) — local recovery and rollback procedures.
- [`TROUBLESHOOTING.md`](TROUBLESHOOTING.md) — local-console-first diagnostics.

## Current target-host evidence

The 2026-08-01 owner-Proxmox pilot recorded real PPPoE/Internet forwarding,
570/327 Mbps in the recorded throughput sample, 0% loss in a 600-packet test,
200/200 DNS queries, 172 MB post-test RAM, dashboard availability during the CPU
stress sample, external WireGuard phone handshake plus dashboard access, and a
successful fallback to pfSense in approximately 93 seconds.

The pilot also established that the tested Alpine `linux-virt` guest did not
provide the PPPoE kernel module required by this appliance, while `linux-lts`
did. The validated Proxmox path therefore uses `linux-lts` and the installers
now fail closed unless `modprobe pppoe` succeeds.

The deployment uses No-IP. Provider-aware No-IP support has now been implemented
through Alpine `inadyn`; the remaining DDNS gate is a fresh target-host proof
that MinimalRouter itself updates No-IP and follows a later public-IP change.

## Installation, operation and recovery

- [`INSTALLATION.md`](INSTALLATION.md)
- [`PROXMOX.md`](PROXMOX.md)
- [`PROXMOX_TEST_REPORT_2026-08-01.md`](PROXMOX_TEST_REPORT_2026-08-01.md)
- [`DYNAMIC_DNS.md`](DYNAMIC_DNS.md)
- [`RECOVERY.md`](RECOVERY.md)
- [`FAILURE_SCENARIOS.md`](FAILURE_SCENARIOS.md)
- [`STORAGE_PRESSURE.md`](STORAGE_PRESSURE.md)
- [`STORAGE_PRESSURE_TEST_PLAN.md`](STORAGE_PRESSURE_TEST_PLAN.md)
- [`APPLIANCE_HEALTH.md`](APPLIANCE_HEALTH.md)
- [`DEVICE_PROFILES.md`](DEVICE_PROFILES.md)
- [`RELEASE_SECURITY.md`](RELEASE_SECURITY.md)
- [`TROUBLESHOOTING.md`](TROUBLESHOOTING.md)
- [`../SUPPORT.md`](../SUPPORT.md)
- [`../PRIVACY.md`](../PRIVACY.md)

There is no stable signed ISO. Keep pfSense or another known-good router ready
for rollback and begin on an isolated LAN plus a test/NAT WAN.

## Architecture and product direction

- [`../PROJECT.md`](../PROJECT.md) — scope and product principles.
- [`../ARCHITECTURE.md`](../ARCHITECTURE.md) — control/data-plane split and
  configuration lifecycle.
- [`../DESIGN.md`](../DESIGN.md) — dashboard design system.
- [`../ROADMAP.md`](../ROADMAP.md) — evidence-driven release gates.
- [`adr/README.md`](adr/README.md) — architecture decisions.

## Development and testing

- [`DEVELOPMENT.md`](DEVELOPMENT.md) — Go and dashboard workflow.
- [`TESTING.md`](TESTING.md) — test layers, failure injection, security,
  performance and manual Proxmox gates.
- [`FAILURE_SCENARIOS.md`](FAILURE_SCENARIOS.md) — expected outcomes and evidence
  status for disruptive scenarios.
- [`STORAGE_PRESSURE_TEST_PLAN.md`](STORAGE_PRESSURE_TEST_PLAN.md) — storage
  threshold, full-filesystem, history shedding, log rotation and recovery checks.
- [`CURRENT_VALIDATION.md`](CURRENT_VALIDATION.md) — latest CI/deep-validation and
  target-host results.
- [`../api/openapi.yaml`](../api/openapi.yaml) — REST API contract.
- [`MCP.md`](MCP.md) — MCP boundary and security requirements.

The automated baseline includes Go race/vet/vulnerability checks, frontend
lint/unit/build/E2E, clean Alpine installation and update rollback, crash
recovery, fuzzing, security analysis, ARM64 QEMU smoke tests, network namespace
validation, bounded-storage regression tests, central health aggregation and
performance benchmarks.

## Evidence and comparisons

- [`PROXMOX_TEST_REPORT_2026-08-01.md`](PROXMOX_TEST_REPORT_2026-08-01.md) — dated
  owner-target comparison with pfSense, PPPoE/kernel finding, load/memory,
  external WireGuard and rollback evidence.
- [`RESOURCE_AND_HARDWARE_TEST.md`](RESOURCE_AND_HARDWARE_TEST.md) — dated
  historical VM, memory, power-loss and virtual network measurements.
- [`SECURITY_REVIEW.md`](SECURITY_REVIEW.md) — dated security review and remaining
  production gates.
- [`COMPARISON.md`](COMPARISON.md) — scope comparison with pfSense and OpenWrt.

Dated reports are preserved as historical evidence. New repository-wide status
is recorded in `CURRENT_VALIDATION.md`.

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
  addresses, hostnames, MAC addresses, VM inventory or household devices.
- Mark measurements with date, environment, method, units and limitations.
- Distinguish implementation, automated evidence, target-host evidence, planned
  work and unsupported features.
- Update documentation, OpenAPI, migrations and screenshots with behavior changes.
- Keep recovery and rollback instructions beside disruptive operations.
