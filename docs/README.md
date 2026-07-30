# Documentation

Minimal Router OS is early-alpha networking software. Read the status and safety
notes before installation, development, or hardware testing.

## Start here

- [`../README.md`](../README.md) — project overview, current status, screenshot,
  capabilities, development outline, and evidence summary.
- [`INSTALLATION.md`](INSTALLATION.md) — controlled Alpine lab installation,
  verification, first-run setup, and rollback precautions.
- [`../SECURITY.md`](../SECURITY.md) — security policy, trust boundaries, secure
  defaults, vulnerability reporting, and release gates.
- [`../CONTRIBUTING.md`](../CONTRIBUTING.md) — contribution workflow for code,
  documentation, design, testing, and review.
- [`TROUBLESHOOTING.md`](TROUBLESHOOTING.md) — safe diagnostic sequence and
  privacy-preserving support evidence.

## Architecture and product direction

- [`../PROJECT.md`](../PROJECT.md) — product scope and principles.
- [`../ARCHITECTURE.md`](../ARCHITECTURE.md) — control-plane/data-plane split,
  configuration lifecycle, trust boundaries, and repository structure.
- [`../DESIGN.md`](../DESIGN.md) — dashboard design system and interaction rules.
- [`../ROADMAP.md`](../ROADMAP.md) — current development priorities and release
  gates.
- [`adr/README.md`](adr/README.md) — architecture decision records, including the isolated IoT zone and fixed-device schedule boundary.

## Installation, operation, and support

- [`INSTALLATION.md`](INSTALLATION.md) — generic controlled-lab installation.
- [`PROXMOX.md`](PROXMOX.md) — Proxmox VM preparation and current limitations.
- [`TROUBLESHOOTING.md`](TROUBLESHOOTING.md) — local-console-first diagnostics.
- [`../SUPPORT.md`](../SUPPORT.md) — support scope, issue requirements, and
  diagnostic redaction.
- [`../PRIVACY.md`](../PRIVACY.md) — local data, optional integrations, backups,
  telemetry status, and privacy-safe reporting.

There is currently no signed release ISO. Treat installation as a controlled lab
procedure and keep console access plus an established-router rollback path.

## Development and testing

- [`DEVELOPMENT.md`](DEVELOPMENT.md) — local Go/dashboard development and test
  commands.
- [`TESTING.md`](TESTING.md) — test layers, integration requirements, failure
  testing, and network safety expectations.
- [`../api/openapi.yaml`](../api/openapi.yaml) — versioned REST API contract.
- [`MCP.md`](MCP.md) — Model Context Protocol integration and its security
  boundary.

## Community and project process

- [`../GOVERNANCE.md`](../GOVERNANCE.md) — decision-making, security-sensitive
  review, project scope, and release authority.
- [`../MAINTAINERS.md`](../MAINTAINERS.md) — active maintainers, responsibilities,
  access progression, and bus-factor status.
- [`../CODE_OF_CONDUCT.md`](../CODE_OF_CONDUCT.md) — expected community behavior
  and enforcement.
- [`../CHANGELOG.md`](../CHANGELOG.md) — notable changes and current limitations.
- [`RELEASE_PROCESS.md`](RELEASE_PROCESS.md) — maintainer process for producing a
  clean repository, validating the exact release commit, and performing an
  owner-reviewed release cutover.

## Comparisons and evidence

- [`COMPARISON.md`](COMPARISON.md) — scope and resource comparison with pfSense
  and OpenWrt. It is not a claim of feature or security parity.
- [`SECURITY_REVIEW.md`](SECURITY_REVIEW.md) — dated security-review evidence and
  remaining validation gates.
- [`RESOURCE_AND_HARDWARE_TEST.md`](RESOURCE_AND_HARDWARE_TEST.md) — dated VM,
  resource, power-loss, rollback, and synthetic data-path measurements.

The evidence documents record specific test environments and dates. They do not
turn the current early-alpha tree into a production recommendation.

## Documentation rules

- Use synthetic examples and documentation-reserved IP/MAC ranges.
- Never include credentials, private keys, public IP addresses, real hostnames,
  real device names, pfSense exports, packet captures, backups, or household
  inventory.
- Mark measurements with environment, date, method, units, and limitations.
- Distinguish implemented behavior, measured evidence, planned work, and
  unsupported features.
- Update documentation, OpenAPI, migration notes, and screenshots in the same
  pull request as the behavior change.
- Prefer authoritative upstream sources for external facts.
- Keep recovery and rollback instructions beside every disruptive operation.