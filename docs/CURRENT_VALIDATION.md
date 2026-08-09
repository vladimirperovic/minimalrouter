# Current validation status

This is the short source of truth for current evidence. Historical reports remain
in `docs/` for traceability. A code or scenario fix is not recorded as a real-lab
PASS until the corresponding hardware/Proxmox run has been repeated.

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

The tested Alpine `linux-virt` guest lacked the PPPoE module required by the real
WAN path. `linux-lts` provided it and the pilot succeeded. The supported pilot
preflight is therefore `modprobe pppoe`; failure is a hard stop.

The deployment uses No-IP. The successful external WireGuard pilot used a
manually provisioned hostname on the Proxmox side, so Minimal Router-managed
No-IP and later public-IP propagation still require a real-provider rerun.

## Isolated-lab evidence — 2026-08-06

A dedicated Proxmox lab (ISP simulator + router + LAN client on isolated
`vmbr-lab-*` bridges) validated:

- PPPoE CHAP negotiation: **PASS**
- PPPoE PAP negotiation: **PASS**
- private/CGNAT WAN address with safe router-local egress: **PASS after fix**
- reboot with PPPoE session recovery and correct dnsmasq/WireGuard ordering: **PASS**

The validated fixes include the nftables output-chain anti-leak fib check, PAP
secret generation/installation, and PPPoE restart hygiene. See [`LAB.md`](LAB.md)
for topology and evidence.

## Torture-lab evidence — 2026-08-08

Scenarios 18–25 were run end to end in the isolated Proxmox lab:

| Scenario | Result |
|---|---|
| 18/19 — WireGuard wg0/wg1 recovery after endpoint blackhole | **PASS** |
| 20 — extraLAN isolation | **PASS** |
| 21 — full reboot: LAN/DHCP/DNS/PPPoE/firewall recover, no hybrid runtime | **PASS** |
| 22 — routerd+applyd crash: initd respawn, PPPoE session survives | **PASS** |
| 23 — power loss at each transaction fault-hook phase | **PASS** |
| 24 — signed update 9.9.8→9.9.9 with verification + rollback | **PASS** |
| 25 — interrupted update mid-activate: cold boot to last-good, no brick | **PASS** |

The Squid LAN-reply firewall bug found during the next scenario set is fixed in
`main` and regression-tested, but the real Proxmox Squid scenario still needs a
fresh run before it is recorded as PASS.

## Final adversarial code audit — 2026-08-09

Focused hardening merged through PRs #58 and #60 after all required checks passed
on a branch updated to the then-current `main`:

- external child/provider diagnostics are control-character sanitized and
  bounded before crossing privileged audit/log boundaries;
- privileged child-process stdout/stderr capture is capped at 4 MiB and fails
  closed on overflow in addition to the existing execution timeout;
- `protocol: both` WireGuard tunnel port forwards are verified as the two real
  TCP and UDP nftables rules, avoiding false rollback of valid configuration;
- A/B slot staging is independent of a restrictive root umask: reviewed file
  modes are restored explicitly and staged directories are normalized to 0755;
- staged writable-file close failures are propagated rather than discarded;
- regression tests reproduce restrictive umask staging, both-protocol firewall
  verification, bounded command output, and external-output sanitization.

The final PR #60 head was rebased/merged onto the current `main` before release
gating. CI, Deep validation, CodeQL, Secret scan, Performance and Service
supervision all passed on that exact branch state. Deep validation included
interrupted-update recovery, fuzzing, ARM64/QEMU, binary/security inspection and
an isolated WAN/router/LAN namespace lab. CI included race tests, vet,
`govulncheck`, dashboard audit/unit/E2E and clean Alpine install/update/rollback.

## Scenarios 26–40: implementation status vs evidence

The scenario definitions for 26–30 have been corrected since the older lab
report:

- 26 now creates meaningful ENOSPC pressure dynamically;
- 27 and 28 now expect the deliberate rejection of unsafe live LAN topology
  changes instead of treating that safety policy as a product failure;
- 29 targets the corrected Squid reply-direction firewall behavior;
- 30 expects provider-aware DDNS verification failure and rollback when the
  provider cannot be reached.

Those corrections do **not** convert the old failed runs into PASS. They require
fresh isolated-Proxmox execution. Scenarios 31–40 also do not yet have committed
current result artifacts. The remaining real-lab/endurance evidence is tracked
explicitly in [issue #61](https://github.com/vladimirperovic/minimalrouter/issues/61).

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

Before recommending unattended production use, record real evidence for:

1. repaired scenarios 26–30 and scenarios 31–40;
2. repeated Proxmox/guest cold boots with stable WAN/LAN identity and no stale
   DHCP/networking delay;
3. repeated real PPPoE disconnect/reconnect and reboot recovery;
4. Minimal Router-managed No-IP update and later IP-change propagation;
5. WireGuard recovery after real PPPoE reconnect/reboot;
6. sustained throughput, packet rate, latency/loss and thermal behavior;
7. external IPv4/IPv6 scanning;
8. encrypted backup restore into a fresh VM;
9. at least seven days of stable unattended operation;
10. signed install/recovery media and independent security review.

The authoritative checklist for those remaining evidence gates is issue #61.
Repository-launch/UI settings that cannot be proved from code alone are tracked
separately in issue #11.

## Recommendation

The current tree is suitable for a **controlled Proxmox pilot** with console
access and pfSense/another known-good router ready for rollback. The code-side
release blockers discovered in the 2026-08-09 adversarial pass are fixed and
merged, but missing real-lab/endurance evidence means it is still premature to
call the project a supported unattended replacement for pfSense or OpenWrt.

Detailed historical evidence:
[`PROXMOX_TEST_REPORT_2026-08-01.md`](PROXMOX_TEST_REPORT_2026-08-01.md) and
[`LAB.md`](LAB.md).
