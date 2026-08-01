# Current validation status — 2026-08-01

This document is the private repository source of truth for shared-engine status,
private owner-Proxmox evidence and remaining deployment gates. Live credentials,
addresses, MACs, bridge assignments, raw VM configuration and household inventory
remain outside Git.

## Shared runtime status

The private repository follows the public `vladimirperovic/minimalrouter` engine.
This branch adds the same provider-aware Dynamic DNS and PPPoE-kernel safeguards
to both repositories:

- No-IP DDNS through Alpine `inadyn` native `no-ip.com` provider;
- Cloudflare DDNS retained for backward compatibility;
- No-IP is the default provider for new configurations;
- old configs with no provider value retain legacy Cloudflare semantics;
- provider-specific credential validation and tests;
- provider switching requires a fresh secret;
- existing transactional DDNS check/update/restart/rollback behavior is reused;
- `pppoe` is now an explicit required kernel module;
- Alpine installers fail closed when `modprobe pppoe` fails and identify
  `linux-lts` as the validated Proxmox path.

## Owner Proxmox pilot — 2026-08-01

A controlled real-WAN pilot successfully carried Internet traffic through
Minimal Router for approximately **27 minutes** and recorded:

| Test | Minimal Router | pfSense |
|---|---:|---:|
| Download | **570 Mbps** | 543 Mbps |
| Upload | **327 Mbps** | 318 Mbps |
| Packet loss, 600 packets | **0%** | **0%** |
| Ping 1.1.1.1 | 2.77 ms | **1.94 ms** |
| Ping 8.8.8.8 | 8.54 ms | **7.61 ms** |
| DNS, 200 queries | **12.65 ms, 200/200** | 13.00 ms, 200/200 |
| CPU 100% load | **0% loss; dashboard 30/30** | Not recorded |
| RAM after load | **172 MB** | Guest agent unavailable |

The fallback to pfSense succeeded in approximately **93 seconds**. The candidate
Minimal Router VM was shut down and isolated and pfSense returned with Internet
connectivity.

## Alpine kernel / PPPoE — PASS with linux-lts

The initial guest used Alpine `linux-virt`. The real PPPoE attempt exposed that
the running kernel did not provide the required PPPoE module. After switching to
**Alpine `linux-lts`**, the module became available and real PPPoE/Internet
forwarding worked.

Observed RAM:

- clean `linux-lts` boot: approximately **73 MB**;
- after the exercised test: **172 MB**.

The validated Proxmox path therefore uses `linux-lts`; the stronger invariant is
that `modprobe pppoe` must succeed.

## WireGuard — PASS for external phone connection

WireGuard is no longer an unvalidated basic-connectivity item. With Dynamic DNS
provisioned manually on the Proxmox side, a phone on an external network
successfully established WireGuard and opened the Minimal Router dashboard
through the tunnel.

This proves for the pilot:

- external UDP reachability to the WireGuard endpoint;
- successful handshake;
- relevant WAN/firewall path;
- tunnel routing sufficient for router management.

Repeated WireGuard recovery after PPPoE reconnect/reboot and broader traffic
cases remain open.

## Dynamic DNS — No-IP implemented, target proof pending

The actual deployment uses **No-IP**, not Cloudflare. The earlier Cloudflare-only
appliance path therefore did not match the deployment. Manually provisioned DDNS
on the Proxmox side enabled the successful WireGuard test and proves that the
hostname/endpoint model works.

This branch now implements No-IP inside MinimalRouter itself. The remaining DDNS
proof is to deploy the build and verify:

1. `inadyn --check-config` succeeds;
2. the bounded one-shot update succeeds;
3. OpenRC `inadyn` remains healthy;
4. an external resolver returns the current public IPv4;
5. WireGuard connects using the No-IP hostname without any host-side workaround;
6. a later public-IP change is propagated automatically.

See [`DYNAMIC_DNS.md`](DYNAMIC_DNS.md).

## Automated evidence

The synchronized engine retains the established automated baseline:

- Go race/vet/vulnerability tests;
- TypeScript lint/unit/build and Playwright E2E;
- clean Alpine install/update/rollback;
- crash recovery and privileged-operation failure cases;
- fuzzing and coverage;
- ARM64/QEMU smoke;
- isolated WAN-router-LAN namespace networking;
- CodeQL, secret scan, gosec, shell/workflow checks;
- bounded storage regression coverage;
- central appliance-health validation;
- performance benchmarks.

These do not replace target-host PPPoE/NIC, thermal, power-loss, restore,
external-scan or long-duration evidence.

## Private-data boundary

Tracked files must never contain:

- PPPoE credentials;
- admin/TOTP/session secrets;
- WireGuard private or preshared keys;
- No-IP DDNS Keys/passwords or Cloudflare tokens;
- real public/private deployment addresses or hostnames;
- MAC addresses, Proxmox node/bridge/VM inventory or raw `qm config` output;
- SQLite/WAL runtime state or privileged recovery metadata;
- backups, packet captures or raw logs.

Use ignored local paths only when temporary deployment material is required:

```text
private/runtime/
private/secrets/
private/backups/
```

## Remaining owner-Proxmox gates

Before an unattended production recommendation:

1. repeated guest/Proxmox reboots with stable WAN/LAN identity;
2. repeated real ISP PPPoE disconnect/reconnect/authentication/reboot recovery;
3. MinimalRouter-managed No-IP update and later public-IP-change propagation;
4. WireGuard recovery after PPPoE reconnect/reboot;
5. longer packet-rate/throughput/CPU/IRQ/latency/jitter/loss/thermal testing;
6. external IPv4 and IPv6 scan;
7. encrypted backup restore into a fresh VM;
8. full-disk, inode, read-only-filesystem, service/helper crash and abrupt
   power-loss exercises on disposable persistent state;
9. at least seven days continuous operation with bounded logs/WAL/history and
   stable memory/thermal behavior;
10. owner-qualified signed installation/recovery media and independent security
    review.

## Current recommendation

Continue as a controlled Proxmox pilot with console access and pfSense ready for
rollback. Real PPPoE on `linux-lts`, Internet forwarding, the recorded load and
performance sample, external WireGuard dashboard access and operational fallback
have now been demonstrated.

The highest-value next test is the new **MinimalRouter-managed No-IP** path,
followed by repeated PPPoE/reboot recovery and longer soak/fault/security tests.

See:

- [`PROXMOX_TEST_REPORT_2026-08-01.md`](PROXMOX_TEST_REPORT_2026-08-01.md)
- [`DYNAMIC_DNS.md`](DYNAMIC_DNS.md)
- [`PROXMOX_AI_HANDOFF.md`](PROXMOX_AI_HANDOFF.md)
- [`PROXMOX.md`](PROXMOX.md)
- [`INSTALLATION.md`](INSTALLATION.md)
- [`RECOVERY.md`](RECOVERY.md)
