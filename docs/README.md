# Documentation

Minimal Router OS is early-alpha networking software. This private repository is
the owner's active deployment-development line. Use it only in a controlled lab
or guarded pilot with local console access and pfSense ready for rollback.

## Start here

- [`../README.md`](../README.md) — private project overview.
- [`CURRENT_VALIDATION.md`](CURRENT_VALIDATION.md) — current shared-engine and
  owner-Proxmox evidence plus remaining gates.
- [`PROXMOX_TEST_REPORT_2026-08-01.md`](PROXMOX_TEST_REPORT_2026-08-01.md) —
  sanitized real PPPoE/Internet/performance/WireGuard/fallback pilot.
- [`PROXMOX_AI_HANDOFF.md`](PROXMOX_AI_HANDOFF.md) — required continuation guide
  for another operator working with the existing Proxmox VM.
- [`PROXMOX.md`](PROXMOX.md) — current Proxmox baseline and next test order.
- [`INSTALLATION.md`](INSTALLATION.md) — Alpine `linux-lts` and PPPoE kernel
  preflight plus controlled install steps.
- [`DYNAMIC_DNS.md`](DYNAMIC_DNS.md) — No-IP default, Cloudflare compatibility,
  apply lifecycle and safe diagnostics.
- [`STORAGE_PRESSURE.md`](STORAGE_PRESSURE.md) — bounded local state and pressure
  behavior.
- [`APPLIANCE_HEALTH.md`](APPLIANCE_HEALTH.md) — aggregate appliance health.
- [`RECOVERY.md`](RECOVERY.md) — local recovery and rollback.
- [`../SECURITY.md`](../SECURITY.md) — threat model and secure defaults.

## Current owner-Proxmox state

The 2026-08-01 pilot demonstrated real PPPoE/Internet through Minimal Router,
570/327 Mbps in the recorded throughput sample, 0% loss across 600 packets,
200/200 DNS queries, 172 MB post-load RAM, external phone WireGuard handshake
plus dashboard access, and successful pfSense fallback in approximately 93
seconds.

The real PPPoE attempt also established that the tested Alpine `linux-virt`
kernel did not provide the required PPPoE module; switching the guest to
**`linux-lts`** solved it. A clean LTS boot used approximately 73 MB RAM in the
recorded session. Current installers fail closed unless `modprobe pppoe` works.

The deployment uses **No-IP**. MinimalRouter now supports No-IP natively through
`inadyn`; the remaining DDNS gate is a fresh target-host proof that the appliance
itself updates the No-IP record and follows a later public-IP change without the
manual Proxmox-host workaround used during the initial WireGuard test.

## Existing VM rule

The live Proxmox node, VM ID, bridge names, addresses and credentials are not in
Git. Any future operator must begin with `PROXMOX_AI_HANDOFF.md`, perform
read-only discovery and stop when VM identity or bridge purpose is ambiguous.

Do not start every VM, do not place a second DHCP server on the production LAN,
and do not perform a real gateway cutover without an independent rollback path.

## Installation, operation and recovery

- [`INSTALLATION.md`](INSTALLATION.md)
- [`PROXMOX.md`](PROXMOX.md)
- [`PROXMOX_AI_HANDOFF.md`](PROXMOX_AI_HANDOFF.md)
- [`PROXMOX_TEST_REPORT_2026-08-01.md`](PROXMOX_TEST_REPORT_2026-08-01.md)
- [`DYNAMIC_DNS.md`](DYNAMIC_DNS.md)
- [`STORAGE_PRESSURE.md`](STORAGE_PRESSURE.md)
- [`STORAGE_PRESSURE_TEST_PLAN.md`](STORAGE_PRESSURE_TEST_PLAN.md)
- [`APPLIANCE_HEALTH.md`](APPLIANCE_HEALTH.md)
- [`RECOVERY.md`](RECOVERY.md)
- [`FAILURE_SCENARIOS.md`](FAILURE_SCENARIOS.md)
- [`TROUBLESHOOTING.md`](TROUBLESHOOTING.md)

## Development and testing

- [`DEVELOPMENT.md`](DEVELOPMENT.md)
- [`TESTING.md`](TESTING.md)
- [`CURRENT_VALIDATION.md`](CURRENT_VALIDATION.md)
- [`../api/openapi.yaml`](../api/openapi.yaml)
- [`MCP.md`](MCP.md)

Automated evidence does not replace the remaining repeated PPPoE/reboot,
MinimalRouter-managed No-IP, backup/restore, external-scan, destructive fault,
thermal and seven-day soak tests.

## Evidence and comparisons

- [`PROXMOX_TEST_REPORT_2026-08-01.md`](PROXMOX_TEST_REPORT_2026-08-01.md) —
  current sanitized target-host evidence.
- [`RESOURCE_AND_HARDWARE_TEST.md`](RESOURCE_AND_HARDWARE_TEST.md) — historical
  VM/memory/power-loss/network measurements.
- [`SECURITY_REVIEW.md`](SECURITY_REVIEW.md) — security review.
- [`COMPARISON.md`](COMPARISON.md) — scope comparison.

## Documentation rules

- Use synthetic examples and documentation-reserved addresses.
- Never include Proxmox host/node names, VM IDs, raw VM configs, live credentials,
  keys, tokens, backups, packet captures, real addresses/hostnames, MACs or
  household inventory.
- Mark measurements with date, environment, method, units and limitations.
- Distinguish implementation, automated evidence, private target-host evidence,
  planned work and unsupported features.
- Keep recovery and rollback instructions beside disruptive operations.
