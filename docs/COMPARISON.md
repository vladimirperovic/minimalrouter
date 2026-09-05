# Minimal Router OS compared with pfSense and OpenWrt

This document is a scope and resource comparison, not a claim of security,
feature, or support parity.

Minimal Router OS is **Beta (v0.1.7)** software with limited deployment history.
pfSense and OpenWrt are mature projects used on a wide range of production
networks. A smaller code and service footprint can reduce complexity, but it
does not by itself prove that a system is safer, faster, or more reliable.

## Summary

| Area | Minimal Router OS | pfSense | OpenWrt |
|---|---|---|---|
| Current maturity | Beta / controlled pilot | Mature production firewall platform | Mature embedded router distribution |
| Primary goal | Focused home/small-office appliance with a narrow UI and safe transaction model | Broad firewall, routing, VPN, HA, and package capabilities | Flexible embedded routing across a large hardware ecosystem |
| Base system | Alpine Linux | FreeBSD | Linux-based embedded distribution |
| Management | React dashboard and Go REST API | Mature web configurator | LuCI and UCI |
| Extensibility | Deliberately limited | Optional package system | Large package ecosystem |
| Production support | Community best effort; no unattended-production claim | Community and commercial Netgate options | Community and vendor/device-dependent options |

## Resource guidance

The numbers below are not identical workload benchmarks.

| Metric | Minimal Router OS | pfSense |
|---|---|---|
| RAM | One ARM64 development VM measured about 140 MiB idle and about 203 MiB after setup/configuration work. 512 MiB is the tested development minimum; 1 GiB is recommended for comfortable headroom. | Netgate documents 1 GiB or more as the minimum. Packages, state count, VPN, IDS/IPS, ZFS, and traffic can require more. |
| Disk | The application payload is small, but 8 GiB is currently recommended for Alpine, logs, snapshots, packages, and upgrades. | Netgate documents 8 GB or more as the minimum. |
| CPU / throughput | A first owner-Proxmox comparison recorded 570/327 Mbps through Minimal Router and 543/318 Mbps through the pfSense fallback in one environment. This is one sample, not a general cross-platform benchmark. | Requirements depend on throughput, VPN cryptography, packages, state count, and traffic features. |
| Dashboard runtime | Static assets served by the Go process; Node.js is not installed on the router. | Integrated web configurator and the services required by the platform. |

A lower measured memory footprint is expected because Minimal Router OS supports
far fewer features. It should not be interpreted as equivalent functionality at
a lower cost.

Official pfSense references:

- https://docs.netgate.com/pfsense/en/latest/hardware/minimum-requirements.html
- https://docs.netgate.com/pfsense/en/latest/hardware/size.html

## DNS filtering and ad blocking

Minimal Router OS includes a basic global DNS sinkhole/blocklist plus scheduled
DNS Filter device profiles. This is useful for focused household policy without
installing a second application.

It is **not** a complete implementation of AdGuard Home. Current limitations
include the absence of its full query-log experience, client policies, extensive
filter management, and other advanced features.

On pfSense, administrators commonly add DNSBL capabilities with the optional
`pfBlockerNG` package or operate a separate DNS filtering service such as
AdGuard Home or Pi-hole. pfSense deliberately keeps many such capabilities in
its package ecosystem rather than its base installation.

Official pfSense package references:

- https://docs.netgate.com/pfsense/en/latest/packages/
- https://docs.netgate.com/pfsense/en/latest/packages/pfblocker.html

## Feature comparison

| Capability | Minimal Router OS — v0.1.7 Beta | pfSense |
|---|---|---|
| Stateful firewall and NAT | Focused generated nftables policy | Comprehensive and mature |
| PPPoE | Implemented; first real owner-Proxmox pilot passed, repeatability testing remains | Mature |
| DHCP and DNS | Implemented with dnsmasq | Mature resolver/forwarder and DHCP capabilities |
| WireGuard | Implemented for the focused remote-access workflow | Supported with broader configuration options |
| WAN web management | Intentionally unavailable | Configurable by the administrator, though secure deployment normally avoids direct exposure |
| Port forwarding | Supported only for the secure WireGuard-ingress profile; arbitrary WAN/PPPoE DNAT is not exposed | Fully supported |
| VLANs | Not yet a stable workflow | Mature |
| Multi-WAN | Not implemented | Mature |
| High availability | Not implemented | CARP and established HA workflows |
| IPv6 | Disabled and blocked until feature parity is complete | Mature IPv6 support |
| IDS/IPS | Not implemented | Available through packages such as Snort and Suricata |
| DNS Filter | Basic integrated sinkhole + scheduled device profiles | Commonly provided through pfBlockerNG or another service |
| Wi-Fi AP | Optional, hardware-dependent, disabled by default | Usually delegated to separate access points; platform/hardware dependent |
| Configuration rollback | Transaction, commit-confirm, snapshots and local recovery | Mature configuration backup and recovery workflows |
| Signed updates | Signed-manifest A/B staging/activation/rollback | Mature package/system update workflow |
| Package ecosystem | No general ecosystem | Extensive optional package repository |
| Hardware/deployment history | Limited CI/lab evidence plus one recorded owner-Proxmox pilot | Extensive real-world deployment history |

## Design differences

### Minimal Router OS

- uses a narrow typed configuration model;
- separates unprivileged `routerd` from privileged `router-applyd`;
- generates owned service configuration deterministically;
- uses preflight, snapshots, verification, and rollback for changes;
- keeps WAN management closed and uses WireGuard for remote access;
- favors a small number of integrated workflows over flexibility.

### pfSense

- covers substantially more firewall and routing scenarios;
- has a mature UI, documentation set, package system, and support ecosystem;
- supports production features such as VLANs, multi-WAN, IPsec, HA, advanced
  policy routing, extensive NAT, and larger deployment topologies;
- requires more resources partly because it provides a much broader platform.

### OpenWrt

- targets a large range of embedded router hardware;
- offers a mature package ecosystem and flexible UCI configuration;
- can have an extremely small footprint depending on the device and image;
- is a more appropriate comparison when hardware constraints and embedded Wi-Fi
  support are primary requirements.

## Choosing a platform

Choose **pfSense** when production maturity, broad firewall features, HA,
multi-WAN, VLAN-heavy environments, commercial support, or a large package
ecosystem are required.

Choose **OpenWrt** when broad embedded-device support, integrated wireless
hardware, and a mature lightweight Linux router ecosystem are most important.

Experiment with **Minimal Router OS** when the goal is to help validate a
focused, resource-efficient home-router appliance and the network can tolerate
Beta software, console access, controlled pilots, and rollback to an established
router.

Minimal Router OS should earn production trust through reproducible tests,
external review, hardware evidence, recovery drills, and stable releases—not
through comparison-table claims. See [`CURRENT_VALIDATION.md`](CURRENT_VALIDATION.md)
for the current evidence boundary.
