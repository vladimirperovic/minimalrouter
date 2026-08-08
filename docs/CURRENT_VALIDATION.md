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

## Isolated-lab evidence — 2026-08-06

A dedicated Proxmox lab (ISP simulator + router + LAN client, all isolated on
`vmbr-lab-*` bridges) validated device-compatibility behavior against *different
ISP-side configurations*, without touching the router config.
Full lab topology, VM inventory, deploy procedure and check commands:
[`LAB.md`](LAB.md).

| Scenario (ISP side) | Router behavior | Result |
|---|---|---|
| PPPoE with **CHAP** auth | auto-negotiated (client uses `noauth`, answers whatever the peer requires) | PASS |
| PPPoE with **PAP** auth | same client path, PAP secrets from the same credential bundle | PASS |
| ISP assigns **private/CGNAT** WAN address (10.250.0.2) | egress firewall must not block router's own traffic | PASS after fix (below) |
| Router reboot with PPPoE session | dnsmasq up before wg1, pppd reconnects (`persist`) | PASS |

### Fixes validated in lab and applied to the repository

1. **Output-chain anti-leak rule.** The old static private-range drop
   (`oifname "ppp*" ip saddr { 10.0.0.0/8, ... } drop`) killed the router's own
   DNS/NTP/pings whenever an ISP assigns a private or CGNAT address to the WAN
   interface. Replaced with a fib check so only non-local source addresses are
   dropped (`internal/services/nftables.go`, output chain):
   `oifname "ppp*" fib saddr type != local drop`
   NOTE: `fib saddr . iif oif` (used in the forward chain) is **not supported in
   the output hook** by the kernel; the forward chain keeps the valid
   `fib saddr . iif oif` form.
2. **PAP support.** `PPPoEConfigBundle` now carries `PapSecrets` generated from
   the same credential material as `ChapSecrets`; applyd writes both
   `/etc/ppp/chap-secrets` and `/etc/ppp/pap-secrets` (0600), and
   `pppoe-wan.initd` preflight-checks both files. The pppd client uses `noauth`
   so it answers PAP, CHAP or MSCHAPv2 depending on what the peer demands.
3. **pppoe-wan restart hygiene.** `rc-service pppoe-wan` (start/restart) honors
   the pidfile; a killed pppd needs stop+start if the pidfile is stale.

## Isolated-lab evidence — 2026-08-08 (torture scenarios 18–25 PASS)

The dedicated lab (ISP simulator 150 + router 108 + LAN client 154 on
`vmbr-lab-*` bridges; scenario suite in `scripts/lab/scenarios/`, run via
`sh lab-run.sh <scenario>`) validated the update/rollback and power-loss paths
end to end:

| Scenario | Result |
|---|---|
| 18/19 — WireGuard wg0/wg1 recovery after endpoint blackhole (keepalive + fib anti-leak) | **PASS** |
| 20 — extraLAN (10.78.0.0/24) isolation | **PASS** |
| 21 — full router reboot: LAN/DHCP/DNS/PPPoE/firewall back, runtime not hybrid | **PASS** |
| 22 — routerd+applyd crash: initd respawn, PPPoE session survives | **PASS** |
| 23 — power loss at each of the 5 fault-hook phases (pre-privileged-apply → pre-canonical-ack): cold boot converges, policy-drop kept | **PASS** |
| 24 — signed update 9.9.8→9.9.9 with runtime verification + rollback | **PASS** |
| 25 — interrupted update (`poweroff -f` mid-activate): cold boot to last-good, **no brick** | **PASS** |

Product bug found and fixed in the working tree (not yet deployed to the lab):

- **Squid proxy unusable from LAN.** The generated nftables output chain dropped
  every packet from the squid UID to the LAN zone *before* the
  established/related accept, including the *responses* to LAN clients that dial
  the proxy. Fixed by accepting the reply direction first
  (`meta skuid squid oifname "<lan>" ct original ip daddr <lan-ip> accept`),
  keeping the Squid-initiated egress cut. Unit test updated
  (`internal/services/scenario_security_test.go`). Signed payload 9.9.10
  prepared; stage/activate pending.

Scenarios 26–30 currently fail for harness/scenario reasons (root fs fill too
small, API rejects live LAN interface changes by design, incomplete LAN-IP
patch, DDNS expectation predates static-only validation) — see `docs/LAB.md`,
section "Torture-lab evidence (2026-08-08)" for the breakdown and fixes.

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
