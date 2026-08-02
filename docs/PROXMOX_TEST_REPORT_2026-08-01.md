# Proxmox target-host test report — 2026-08-01

This report records the first successful owner-Proxmox pilot in which Minimal
Router carried real Internet traffic and was compared directly with the existing
pfSense fallback router.

The values below are sanitized operational evidence supplied from the target
environment. Credentials, public addresses, hostnames, MAC addresses, bridge
names, and other private deployment data are intentionally omitted.

## Scope and outcome

Minimal Router successfully became the active router, provided Internet access,
remained stable during the observed test window, completed load and network
checks without packet loss, accepted a real WireGuard connection from a phone,
and was then returned to the existing pfSense router through the planned
automatic rollback path.

Observed continuous Minimal Router runtime during this pilot was approximately
**27 minutes**.

This is meaningful target-host evidence, but it is not a production-readiness
claim. The appliance-managed Dynamic DNS path still requires a fresh target-host
validation after the No-IP implementation, and longer soak/fault/security tests
remain outstanding.

## Environment labels

For this dated report only, the tested virtual machines are identified by their
local Proxmox VM IDs because those IDs are part of the supplied benchmark record:

- Minimal Router candidate: VM 108
- pfSense fallback/baseline: VM 106

Do not copy additional private Proxmox inventory, bridge assignments, MAC
addresses, ISP credentials, public addresses, or DDNS credentials into this
repository.

## Kernel finding: linux-lts required for the validated PPPoE path

The initial Alpine candidate used `linux-virt`. During the real PPPoE bring-up,
that running kernel did not provide the PPPoE kernel module required by the
appliance. The guest was switched to Alpine `linux-lts`; the required PPPoE
module became available and the real WAN test succeeded.

Observed memory data also showed that moving to `linux-lts` did not imply RAM
usage proportional to the larger kernel package on disk:

- clean `linux-lts` boot measurement: approximately **73 MB** RAM used;
- post-load/test measurement: **172 MB** RAM used.

For the validated Proxmox installation path, `linux-lts` is therefore the
recommended guest kernel. Installers now also require the actual `pppoe` module
to load successfully, so a future kernel with equivalent support can pass based
on capability rather than package name alone.

## Performance and connectivity comparison

| Test | Minimal Router VM 108 | pfSense VM 106 |
|---|---:|---:|
| Download | **570 Mbps** | 543 Mbps |
| Upload | **327 Mbps** | 318 Mbps |
| Packet loss, 600 packets | **0%** | **0%** |
| Ping to 1.1.1.1 | 2.77 ms | **1.94 ms** |
| Ping to 8.8.8.8 | 8.54 ms | **7.61 ms** |
| DNS, 200 queries | **12.65 ms, 200/200** | 13.00 ms, 200/200 |
| CPU 100% load test | **0% packet loss; dashboard 30/30** | Not recorded |
| RAM after test | **172 MB** | Guest agent unavailable |

### Interpretation

- Both routers completed the 600-packet loss test with zero observed loss.
- Minimal Router measured about 5.0% higher download throughput and about 2.8%
  higher upload throughput in this single test sample.
- pfSense measured about 0.83 ms lower latency to 1.1.1.1 and about 0.93 ms
  lower latency to 8.8.8.8 in this sample.
- Minimal Router DNS latency was about 0.35 ms lower while both systems
  completed all 200 DNS queries.
- Under the recorded 100% CPU test, Minimal Router still observed zero packet
  loss and the dashboard responded successfully on all 30 checks.
- The 172 MB RAM value is an observed post-test measurement, not a guaranteed
  maximum or minimum sizing requirement.

These are single target-host observations. They should not be generalized into
vendor-wide performance claims without repeated controlled runs.

## Functional result

Confirmed during this pilot:

- the Minimal Router VM booted in the target Proxmox environment;
- Alpine `linux-lts` supplied the PPPoE kernel support needed by the tested
  guest;
- real PPPoE/Internet access worked through the candidate router;
- packet forwarding remained functional during the observed load test;
- DNS completed 200/200 recorded queries;
- dashboard responsiveness remained available during the CPU stress check;
- no packet loss was observed in the 600-packet comparison test;
- memory remained modest after the exercised workload;
- a real WireGuard phone connection reached the Minimal Router dashboard;
- the planned automatic fallback to pfSense completed successfully.

## WireGuard result — PASS for external handshake and dashboard access

WireGuard was enabled on Minimal Router. After Dynamic DNS was provisioned
manually on the Proxmox side for the test, the phone successfully connected from
the external network and the Minimal Router dashboard was opened through the
WireGuard tunnel.

This validates, for this pilot:

- Internet-origin reachability to the configured WireGuard UDP endpoint;
- a successful cryptographic handshake with the phone peer;
- the relevant WAN/firewall path for WireGuard;
- tunnel routing sufficient to reach the router management dashboard;
- management access through WireGuard rather than exposing the dashboard on the
  WAN.

Still not proven by this one session are long-term tunnel stability, recovery
after repeated PPPoE reconnects/reboots, and all possible LAN/full-tunnel client
routing cases.

## Dynamic DNS result and No-IP correction

The user deployment uses **No-IP**. The earlier Cloudflare-only appliance DDNS
implementation did not match that deployment.

During the successful WireGuard check, Dynamic DNS was provisioned manually on
the Proxmox side. With that working DDNS endpoint in place, the phone WireGuard
connection succeeded and the dashboard was reachable. This is useful evidence
that the external hostname/endpoint concept and WireGuard path work, but it is
not evidence that the old appliance-managed Cloudflare-only path worked.

Minimal Router has now been changed to support provider-aware Dynamic DNS via
Alpine `inadyn`:

- **No-IP** via the native `no-ip.com` inadyn provider;
- **Cloudflare** retained for backward compatibility;
- new configurations default to No-IP while old configurations with an empty
  provider value retain legacy Cloudflare semantics;
- provider credentials remain redacted through the existing secret field;
- changing provider requires a new credential;
- the apply pipeline still performs config check, one-shot real update, service
  restart/health verification, and transaction rollback on failure.

A fresh target-host validation of **MinimalRouter-managed No-IP** still needs to
record, without exposing secrets:

1. `inadyn --check-config` succeeds;
2. the bounded one-shot update succeeds;
3. the OpenRC `inadyn` service remains healthy;
4. an external DNS lookup resolves the expected No-IP hostname/current public
   IPv4 after the update window;
5. WireGuard connects using that hostname without any host-side manual DDNS
   workaround;
6. a later public-IP change is reflected automatically.

See [`DYNAMIC_DNS.md`](DYNAMIC_DNS.md) for the No-IP/Cloudflare configuration
contract and safe diagnostics.

## Automatic rollback / pfSense fallback

The rollback exercise succeeded:

- pfSense returned to the active routing role and had Internet connectivity;
- Minimal Router VM 108 was shut down and isolated;
- the measured transition took approximately **93 seconds**.

The failsafe timer was armed on the Proxmox host before the gateway cutover, so
its execution did not depend on the ChatGPT/Codex management connection. The
management connection did in fact drop during the routing transition while the
host-side timer remained independent.

This is the first recorded target-host evidence for the intended operational
fallback path. It does not replace transaction-level rollback, A/B software
rollback, backup restore, or abrupt-power recovery tests; those are distinct
failure modes.

## Gates closed by this pilot

This run materially reduces uncertainty for:

- basic target-Proxmox boot and routing;
- real PPPoE and Internet forwarding in the owner's environment;
- `linux-lts` as the validated Alpine PPPoE kernel path;
- a first real target-host throughput/latency/DNS comparison with pfSense;
- packet-loss behavior in the recorded 600-packet sample;
- management responsiveness during the recorded CPU stress test;
- observed post-test memory use;
- real external WireGuard handshake and management dashboard access;
- operational fallback from Minimal Router to the known-good pfSense VM.

## Gates still open

The following are still required before an unattended production recommendation:

1. repeated guest/Proxmox reboot cycles with stable WAN/LAN identity;
2. longer PPPoE stability and explicit disconnect/reconnect/reboot recovery;
3. appliance-managed No-IP update plus automatic public-IP-change verification;
4. WireGuard recovery after repeated PPPoE reconnect/reboot and broader traffic
   checks where required;
5. independent external IPv4 and IPv6 scans;
6. encrypted backup restore into a fresh VM;
7. full-disk, inode-exhaustion, and read-only-filesystem tests;
8. service/helper crash and abrupt host power-loss exercises on persistent
   storage;
9. sustained throughput, packet rate, IRQ/CPU behavior, latency/jitter, memory,
   and thermal measurements over longer runs;
10. at least seven days of continuous operation with bounded logs, WAL, history,
    and snapshots;
11. owner-qualified signed installation/recovery media;
12. an independent focused security review before an unattended production
    claim.

## Current recommendation

The successful pilot supports continuing controlled target-host validation. It
does not yet justify removing the pfSense fallback. Keep console access and the
known-good rollback path available while the new appliance-managed No-IP path,
reboot/reconnect recovery, external scanning, destructive storage tests, backup
restore, and soak testing remain open.
