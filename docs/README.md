# Documentation

Minimal Router OS is early-alpha networking software. Read the status and safety
notes before installation or development.

## Start here

- [`../README.md`](../README.md) — project overview, current status, screenshot,
  features, installation outline, and resource summary.
- [`../SECURITY.md`](../SECURITY.md) — security policy, trust boundaries, secure
  defaults, vulnerability reporting, and release gates.
- [`../CONTRIBUTING.md`](../CONTRIBUTING.md) — contribution workflow for code,
  documentation, design, testing, and review.
- [`DEVELOPMENT.md`](DEVELOPMENT.md) — local Go/dashboard development and test
  commands.
- [`TESTING.md`](TESTING.md) — test layers, integration requirements, and safety
  expectations.

## Architecture and product direction

- [`../PROJECT.md`](../PROJECT.md) — product scope and principles.
- [`../ARCHITECTURE.md`](../ARCHITECTURE.md) — control-plane/data-plane split,
  configuration lifecycle, trust boundaries, and repository structure.
- [`../DESIGN.md`](../DESIGN.md) — dashboard design system and interaction rules.
- [`../ROADMAP.md`](../ROADMAP.md) — current development priorities and release
  gates.
- [`adr/README.md`](adr/README.md) — architecture decision records, including
  superseded decisions preserved for context.

## Installation and deployment

- [`PROXMOX.md`](PROXMOX.md) — current Proxmox lab deployment guidance.
- [`RELEASE_PROCESS.md`](RELEASE_PROCESS.md) — maintainer-only process for
  producing a clean one-commit repository and performing an owner-reviewed
  release cutover.

There is currently no signed release ISO. Treat installation as a controlled lab
procedure and keep console access plus an established-router rollback path.

## API and integrations

- [`../api/openapi.yaml`](../api/openapi.yaml) — versioned REST API contract.
- [`MCP.md`](MCP.md) — Model Context Protocol integration and its security
  boundary.

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
  real device names, pfSense exports, packet captures, or household inventory.
- Mark measurements with environment, date, method, and limitations.
- Update documentation in the same pull request as behavior changes.
- Prefer links to authoritative upstream documentation for external facts.
