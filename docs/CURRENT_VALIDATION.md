# Current validation status

This is the short source of truth for current evidence. Historical reports remain
in `docs/` for traceability.

## Current implementation

The repository currently includes:

- Alpine router packaging with `routerd` / `router-applyd` privilege separation
- nftables firewall/NAT, PPPoE, DHCP/DNS, WireGuard, QoS and optional Wi-Fi/Squid
- No-IP and Cloudflare DDNS through `inadyn`
- transactional config apply, confirmation, rollback and canonical recovery
- encrypted backups, snapshots and crash-safe A/B updates
- bounded storage, log rotation, conntrack/time/service health aggregation
- gateway quality history, live bandwidth, device search and Wake-on-LAN
- automated Go/frontend/security/Alpine/QEMU/network-namespace test suites

## Real Proxmox evidence — 2026-08-01

A controlled owner-Proxmox pilot carried real Internet traffic through Minimal
Router for about 27 minutes and then successfully returned to pfSense.

| Test | Minimal Router | pfSense |
|---|---:|---:|
| Download | **570 Mbps** | 543 Mbps |
| Upload | **327 Mbps** | 318 Mbps |
| Packet loss (600 packets) | **0%** | **0%** |
| Ping 1.1.1.1 | 2.77 ms | **1.94 ms** |
| Ping 8.8.8.8 | 8.54 ms | **7.61 ms** |
| DNS (200 queries) | **12.65 ms, 200/200** | 13.00 ms, 200/200 |
| CPU stress | **0% loss, dashboard 30/30** | — |
| RAM after test | **172 MB** | — |

Additional results:

- real PPPoE and Internet forwarding: **PASS**
- external phone WireGuard handshake: **PASS**
- dashboard access through WireGuard: **PASS**
- pfSense operational fallback: **PASS**, about 93 seconds

### PPPoE kernel requirement

The tested Alpine `linux-virt` guest lacked the PPPoE module required by the real
WAN path. `linux-lts` provided it and the pilot succeeded. The supported pilot
preflight is therefore:

```sh
modprobe pppoe
```

Failure is a hard stop. A clean `linux-lts` boot used about 73 MB RAM in the
recorded session.

### Dynamic DNS

The deployment uses No-IP. Provider-aware No-IP support is implemented in the
appliance, but the successful external WireGuard pilot used a manually
provisioned hostname on the Proxmox side.

Still required: prove that **Minimal Router itself** updates No-IP, resolves to
the current public IPv4 and follows a later public-IP change.

## Automated validation

Repository workflows cover:

- `go test -race`, `go vet`, vulnerability and security scans
- frontend lint/unit/build/Playwright E2E
- clean Alpine install and update/rollback lifecycle
- crash/recovery and transaction-state regression tests
- fuzzing, CodeQL, secret scanning and binary/shell checks
- ARM64 QEMU smoke tests
- isolated WAN-router-LAN DHCP/DNS/NAT/firewall testing
- storage-pressure and appliance-health regression tests
- control-plane benchmarks

These tests do not replace real ISP, NIC, thermal, power-loss or long-duration
validation.

## Remaining release gates

Before recommending unattended production use, record evidence for:

1. repeated Proxmox/guest reboots with stable WAN/LAN identity;
2. repeated real PPPoE disconnect/reconnect and reboot recovery;
3. MinimalRouter-managed No-IP update and later IP-change propagation;
4. WireGuard recovery after PPPoE reconnect/reboot;
5. sustained throughput, packet rate, latency/loss and thermal behavior;
6. external IPv4/IPv6 scanning;
7. encrypted backup restore into a fresh VM;
8. full-disk, inode, read-only-filesystem, process-crash and abrupt-power tests;
9. at least seven days of stable operation;
10. signed install/recovery media and independent security review.

## Recommendation

The current tree is suitable for a **controlled Proxmox pilot** with console
access and pfSense/another known-good router ready for rollback. It is not yet a
supported unattended replacement for pfSense or OpenWrt.

Detailed evidence: [`PROXMOX_TEST_REPORT_2026-08-01.md`](PROXMOX_TEST_REPORT_2026-08-01.md).
