# Proxmox target-host test report — 2026-08-01

This private report records sanitized owner-Proxmox evidence. Live credentials,
public/private addresses, hostnames, MAC addresses, bridge assignments and other
deployment identifiers are intentionally omitted.

## Outcome

Minimal Router successfully became the active router, established real PPPoE and
Internet access, remained stable for approximately **27 minutes**, completed
network/load checks, accepted a real WireGuard phone connection, and then returned
to the known-good pfSense router through the planned fallback path.

## Kernel finding

The initial Alpine guest used `linux-virt`. The real PPPoE path exposed that the
running kernel did not provide the PPPoE module required by the appliance. After
switching to **Alpine `linux-lts`**, the required module was available and PPPoE
worked.

Observed memory:

- clean `linux-lts` boot: approximately **73 MB** RAM used;
- after the exercised workload: **172 MB** RAM used.

The validated Proxmox path therefore uses `linux-lts`; installers also require
`modprobe pppoe` to succeed so capability, not package name alone, is the gate.

## Performance comparison

| Test | Minimal Router | pfSense |
|---|---:|---:|
| Download | **570 Mbps** | 543 Mbps |
| Upload | **327 Mbps** | 318 Mbps |
| Packet loss, 600 packets | **0%** | **0%** |
| Ping to 1.1.1.1 | 2.77 ms | **1.94 ms** |
| Ping to 8.8.8.8 | 8.54 ms | **7.61 ms** |
| DNS, 200 queries | **12.65 ms, 200/200** | 13.00 ms, 200/200 |
| CPU 100% load | **0% packet loss; dashboard 30/30** | Not recorded |
| RAM after test | **172 MB** | Guest agent unavailable |

These are single-session observations, not general vendor performance claims.

## WireGuard — PASS for external handshake and dashboard access

After Dynamic DNS was provisioned manually on the Proxmox side, the phone
connected from the external network through WireGuard and the Minimal Router
dashboard opened through the tunnel.

This validates for the pilot:

- Internet-origin reachability to the WireGuard UDP endpoint;
- successful peer handshake;
- relevant WAN/firewall handling;
- tunnel routing sufficient for dashboard management;
- management through WireGuard without exposing the dashboard directly on WAN.

Repeated recovery after PPPoE reconnect/reboot and broader traffic/throughput
cases remain separate tests.

## Dynamic DNS

The deployment uses **No-IP**. The old appliance DDNS implementation was
Cloudflare-only and therefore did not match the actual deployment. Manual
Proxmox-side DDNS enabled the successful WireGuard test, proving that the
hostname/endpoint concept works.

The repository now implements provider-aware `inadyn` DDNS:

- No-IP through native `no-ip.com`;
- Cloudflare retained for compatibility;
- new configs default to No-IP;
- old configs without a provider retain Cloudflare semantics;
- provider-specific credential validation;
- provider switch requires a new secret;
- existing config-check, one-shot verification, OpenRC restart/health check and
  rollback lifecycle is retained.

Remaining target proof: deploy this build and verify that **MinimalRouter itself**
updates No-IP and follows a later public-IP change without a host-side workaround.

## Fallback

The fallback exercise succeeded:

- pfSense returned to the active routing role with Internet connectivity;
- the Minimal Router candidate was shut down and isolated;
- measured transition was approximately **93 seconds**.

The safety timer was armed on the Proxmox host before cutover and did not depend
on the remote management connection. The management connection did drop during
the gateway transition while the host-side failsafe remained independent.

## Remaining gates

Before unattended production use, still validate:

1. repeated guest/host reboots with stable WAN/LAN identity;
2. repeated real PPPoE disconnect/reconnect/authentication/reboot recovery;
3. MinimalRouter-managed No-IP update, service health and public-IP-change
   propagation;
4. WireGuard recovery after PPPoE reconnect/reboot;
5. longer throughput/packet-rate/CPU/IRQ/latency/jitter/thermal testing;
6. external IPv4/IPv6 scan;
7. encrypted backup restore into a fresh VM;
8. full-disk/inode/read-only/service-crash/power-loss tests on disposable state;
9. at least seven days of continuous operation;
10. owner-qualified signed recovery media and independent security review.

See [`DYNAMIC_DNS.md`](DYNAMIC_DNS.md), [`PROXMOX.md`](PROXMOX.md) and
[`PROXMOX_AI_HANDOFF.md`](PROXMOX_AI_HANDOFF.md).
