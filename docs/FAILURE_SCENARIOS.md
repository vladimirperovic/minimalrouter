# Failure scenario matrix

This document records logical failure scenarios derived from the current runtime code. It distinguishes automated guarantees from tests that still require an owner Proxmox VM or physical networking.

The expected invariant is always one of these outcomes:

1. the new configuration is fully active and verified;
2. the previous confirmed configuration is fully restored; or
3. the appliance fails closed with forwarding disabled and requires local console recovery.

A mixed, silently accepted state is never an acceptable outcome.

## Power loss and process restart

| Scenario | Required result | Coverage |
|---|---|---|
| Power loss while no change is active | Restore confirmed LAN, firewall, DNS, WireGuard and optional local services from `last-good.json` | Automated startup reconciliation |
| Power loss after `/run` is cleared | Regenerate nftables and WireGuard runtime files; recreate kernel interfaces | Automated startup reconciliation |
| Power loss during an unconfirmed LAN/topology change | Reapply the previous confirmed configuration, then clear the pending marker | Automated decision tests; Proxmox destructive test remains |
| Pending marker exists but `last-good.json` is absent | Disable forwarding, stop routing services, refuse startup and do not guess a configuration | Automated decision test and fail-closed startup path |
| `last-good.json` is corrupt, invalid or unexpectedly empty | Disable forwarding, stop routing services and refuse to expose the apply socket | Automated decision tests and fail-closed startup path |
| Runtime restore fails halfway | Disable IPv4 forwarding, remove WireGuard, stop routing-related services and exit | Code path; Proxmox fault injection remains |
| ISP is unavailable during boot | Restore LAN/firewall/management and start PPPoE asynchronously; do not require a live PPP session for local recovery | Startup code and VM test required |
| DDNS provider is unavailable during boot | Start `inadyn` for retry without blocking local router recovery on a forced external update | Startup code and VM test required |
| Repeated reboot | Reconciliation remains idempotent and leaves one instance of each interface/rule | Proxmox repeated-reboot test required |

## WireGuard

| Scenario | Required result | Coverage |
|---|---|---|
| Invalid WireGuard configuration | Temporary validation interface is deleted; active interface is untouched | Existing preflight cleanup |
| Failure creating `wg0` | Apply fails and previous configuration is restored | Existing apply rollback plus VM fault injection |
| Failure during `wg setconf` | Candidate interface is deleted | Existing deferred cleanup |
| Failure assigning address | Candidate interface is deleted | Existing deferred cleanup |
| Failure setting MTU or link up | Candidate interface is deleted | Existing deferred cleanup |
| Failure installing one peer route | Candidate interface and its routes are deleted | Existing deferred cleanup |
| WireGuard disabled while stale interface exists | Stale interface is removed and verified absent | Existing activation logic |
| Power loss with WireGuard enabled | Recreate interface, configuration, address, MTU and routes from confirmed state | Startup reconciliation |
| Power loss while WireGuard enable is unconfirmed | Restore the prior confirmed enabled/disabled state | Startup decision logic |
| WAN MTU is too large or too small | Clamp WireGuard MTU to 576–1420; PPPoE 1492 yields 1412 | Automated unit test |
| Peer has duplicate/conflicting routes | Reject in canonical validation before activation | Existing validation; expand when route policy changes |
| External client cannot handshake after reboot | Local interface remains available; record as degraded WAN test, not silent success | Proxmox/external-network test required |

## Firewall and forwarding

| Scenario | Required result | Coverage |
|---|---|---|
| Candidate nftables syntax is invalid | Preflight fails before active table replacement | Existing preflight |
| Active table replacement fails | Netlink batch is atomic; rollback restores confirmed table | Existing apply/rollback and namespace test |
| Crash before nftables activation | Confirmed table is regenerated on startup | Startup reconciliation |
| Crash after nftables activation but before persistence | `last-good` wins on reboot; unconfirmed candidate is discarded | Startup decision logic; VM destructive test remains |
| Startup reconciliation cannot verify forwarding or local firewall | Disable forwarding and exit | Fail-closed startup path |
| Firewall table disappears while process remains running | Current code detects it only on apply/verification; runtime watchdog is not yet implemented | Open follow-up |
| Kernel forwarding unexpectedly turns off | Current traffic stops safely; runtime watchdog is not yet implemented | Open follow-up |

## PPPoE and WAN

| Scenario | Required result | Coverage |
|---|---|---|
| Invalid peer configuration | `pppd dryrun` fails before installation | Existing preflight |
| Wrong ISP credentials during a normal apply | Verification times out and restores previous confirmed configuration | Existing verification/rollback; real ISP test required |
| ISP outage during boot | Local management boots; PPPoE service retries without blocking applyd | Startup policy; VM test required |
| PPPoE drops after successful boot | OpenRC/pppd should reconnect; bounded reconnect/watchdog evidence is still required | Open follow-up/Proxmox test |
| `ppp0` comes up without address/default route during apply | Verification fails and rolls back | Existing verification |
| MTU mismatch causes black-hole traffic | WireGuard MTU is derived; real path-MTU and MSS evidence remains required | Partial automated, real ISP test required |

## DHCP and DNS

| Scenario | Required result | Coverage |
|---|---|---|
| Invalid dnsmasq candidate | `dnsmasq --test` rejects it before replacement | Existing preflight |
| dnsmasq restart fails after files are installed | Rollback restores previous files and service state | Existing apply rollback; VM injection required |
| Power loss after config write before restart | Startup regenerates confirmed files and restarts dnsmasq | Startup reconciliation |
| dnsmasq is stopped after healthy boot | No continuous watchdog currently restarts it | Open follow-up |
| Full disk prevents atomic file replacement | Apply fails; previous file survives rename protocol | Existing atomic write behavior; disk-full VM test required |
| DNS upstream unavailable | DHCP/local management should remain; external resolution degrades | Proxmox/network test required |

## LAN, Wi-Fi and management lockout

| Scenario | Required result | Coverage |
|---|---|---|
| LAN address/topology change is not confirmed | Preserve old address during confirmation and restore old config on timeout/reboot | Existing commit-confirm plus startup recovery |
| Power loss during provisional LAN change | `last-good` is restored before routerd starts | Startup reconciliation and OpenRC socket readiness gate |
| Wrong LAN interface selected | Provisional interface changes are rejected; local recovery console remains | Existing policy/recovery |
| Wi-Fi bridge creation fails halfway | Apply rolls back; startup failure disables forwarding | Existing rollback plus fail-closed boot |
| hostapd fails to start | Verification/apply fails and restores previous topology | Existing rollback |
| Management listener would bind to WAN | Host/destination checks reject it; external scan still required | Existing API policy and manual scan |

## Update, persistence and recovery

| Scenario | Required result | Coverage |
|---|---|---|
| Power loss during A/B activation or rollback | Durable operation journal deterministically completes old or new slot | Automated interruption tests |
| Corrupt update journal | Fail closed and reject unsafe state | Automated corruption/fuzz tests |
| Concurrent update operations | Serialize with operation lock | Automated tests |
| Last transaction response cannot be persisted | Return failure rather than silently claim durable success | Existing apply behavior |
| `last-good.json` cannot be persisted after apply | Restore previous configuration | Existing rollback path |
| Pending marker cannot be persisted | Restore previous configuration | Existing rollback path |
| Pending marker cannot be removed after confirmation | Return failure; next boot restores confirmed state | Existing behavior plus startup recovery |
| Recovery DB mutation fails midway | SQLite transaction rolls back and sessions/config remain consistent | Existing failure-injection tests |
| Backup is truncated, wrong-password or incompatible | Reject before replacing canonical state | Existing restore validation; fresh-VM rehearsal required |

## Resource and long-duration failures

| Scenario | Required result | Coverage |
|---|---|---|
| Disk becomes full | Atomic writes fail without replacing confirmed file; router must remain manageable or fail closed | Proxmox test required |
| Filesystem becomes read-only | Apply fails and confirmed runtime remains; reboot path must fail closed if regeneration is impossible | Proxmox test required |
| `router-applyd` is killed | OpenRC restart policy and startup reconciliation must restore confirmed state | Proxmox test required |
| `routerd` is killed | Packet forwarding continues; management returns after service restart | Proxmox test required |
| Memory pressure/OOM | No partial configuration should be accepted; service restart evidence required | Proxmox stress test required |
| Logs fill the disk | Log rotation and bounded-growth evidence are still required | Open release gate |
| Seven-day operation | No unbounded memory, file, connection, lease or log growth | Proxmox soak test required |

## Remaining highest-priority follow-ups

1. Add a lightweight local runtime watchdog for nftables, dnsmasq, WireGuard and forwarding drift after a healthy boot.
2. Add Proxmox fault injection for process kill, reboot, forced power-off, full disk and read-only filesystem.
3. Test PPPoE reconnect and path MTU with the real ISP.
4. Test WireGuard recovery from an unrelated external network.
5. Restore an encrypted backup into a fresh VM and verify all services.
6. Run an external WAN scan after every firewall/topology change.
7. Record seven-day CPU, RAM, disk, logs, packet loss and reconnect behavior.
