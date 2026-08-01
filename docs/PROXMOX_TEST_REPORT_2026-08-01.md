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
checks without packet loss, and was then returned to the existing pfSense router
through the planned automatic rollback path.

Observed continuous Minimal Router runtime during this pilot was approximately
**27 minutes**.

This is meaningful target-host evidence, but it is not a production-readiness
claim. WireGuard client connectivity and Cloudflare DDNS were not successfully
validated in this run, and longer soak/fault/security tests remain outstanding.

## Environment labels

For this dated report only, the tested virtual machines are identified by their
local Proxmox VM IDs because those IDs are part of the supplied benchmark record:

- Minimal Router candidate: VM 108
- pfSense fallback/baseline: VM 106

Do not copy additional private Proxmox inventory, bridge assignments, MAC
addresses, ISP credentials, or public addresses into this repository.

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
- real Internet access worked through the candidate router;
- packet forwarding remained functional during the observed load test;
- DNS completed 200/200 recorded queries;
- dashboard responsiveness remained available during the CPU stress check;
- no packet loss was observed in the 600-packet comparison test;
- memory remained modest after the exercised workload;
- the planned automatic fallback to pfSense completed successfully.

## WireGuard result

WireGuard was enabled on Minimal Router, but **no handshake with the phone was
observed** during this test window.

Therefore this run proves only that the configured WireGuard service/interface
could be enabled; it does **not** prove:

- Internet-origin UDP reachability to the WireGuard port;
- correctness of the phone peer configuration;
- endpoint/DDNS correctness;
- successful cryptographic handshake;
- tunnel routing, DNS, LAN access, or full-tunnel Internet access;
- recovery of a working tunnel after PPPoE reconnect or reboot.

WireGuard remains an open target-host validation item.

## Cloudflare DDNS result

DDNS was **not confirmed working** in this pilot.

Minimal Router currently implements Cloudflare DDNS through Alpine `inadyn`; it
is not a generic DynDNS/No-IP/redirectme.net client. The dashboard fields are:

- `Hostname`: the full DNS record, for example `router.example.com`;
- `Zone`: the DNS **zone name**, for example `example.com`, not the hexadecimal
  Cloudflare Zone ID;
- `API token`: a scoped Cloudflare API token permitted to read the selected zone
  and edit its DNS records.

A successful target-host DDNS validation still needs to record all of the
following without exposing secrets:

1. `inadyn --check-config` succeeds;
2. the bounded one-shot update succeeds;
3. the OpenRC `inadyn` service remains healthy;
4. an external DNS lookup returns the current public IPv4 address after the
   update/TTL window;
5. a later public-IP change is reflected automatically.

See [`CLOUDFLARE_DDNS.md`](CLOUDFLARE_DDNS.md) for the configuration contract and
safe local-console diagnostics.

## Automatic rollback / pfSense fallback

The rollback exercise succeeded:

- pfSense returned to the active routing role and had Internet connectivity;
- Minimal Router VM 108 was shut down and isolated;
- the measured transition took approximately **93 seconds**.

This is the first recorded target-host evidence for the intended operational
fallback path. It does not replace transaction-level rollback, A/B software
rollback, backup restore, or abrupt-power recovery tests; those are distinct
failure modes.

## Gates closed by this pilot

This run materially reduces uncertainty for:

- basic target-Proxmox boot and routing;
- real Internet forwarding in the owner's environment;
- a first real target-host throughput/latency/DNS comparison with pfSense;
- packet-loss behavior in the recorded 600-packet sample;
- management responsiveness during the recorded CPU stress test;
- observed post-test memory use;
- operational fallback from Minimal Router to the known-good pfSense VM.

## Gates still open

The following are still required before an unattended production recommendation:

1. repeated guest/Proxmox reboot cycles with stable WAN/LAN identity;
2. longer PPPoE stability and explicit disconnect/reconnect/reboot recovery;
3. successful WireGuard handshake and traffic from an unrelated external
   network;
4. successful Cloudflare DDNS update and later public-IP-change verification;
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
known-good rollback path available while WireGuard, Cloudflare DDNS, recovery,
external scanning, destructive storage tests, and soak testing remain open.
