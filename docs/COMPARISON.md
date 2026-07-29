# Minimal Router OS compared with pfSense and OpenWrt

This document is a scope and resource comparison, not a claim of security,
feature, or support parity.

Minimal Router OS is early alpha software with limited deployment history.
pfSense and OpenWrt are mature projects used on a wide range of production
networks. A smaller code and service footprint can reduce complexity, but it
does not by itself prove that a system is safer, faster, or more reliable.

## Summary

| Area | Minimal Router OS | pfSense | OpenWrt |
|---|---|---|---|
| Current maturity | Early alpha development project | Mature production firewall platform | Mature embedded router distribution |
| Primary goal | Focused home/small-office appliance with a narrow UI and safe transaction model | Broad firewall, routing, VPN, HA, and package capabilities | Flexible embedded routing across a large hardware ecosystem |
| Base system | Alpine Linux | FreeBSD | Linux-based embedded distribution |
| Management | React dashboard and Go REST API | Mature web configurator | LuCI and UCI |
| Extensibility | Deliberately limited | Optional package system | Large package ecosystem |
| Production support | Community best effort | Community and commercial Netgate options | Community and vendor/device-dependent options |

## Resource guidance

The numbers below are not identical workload benchmarks.

| Metric | Minimal Router OS | pfSense |
|---|---|---|
| RAM | One ARM64 development VM measured about 140 MiB idle and about 203 MiB after setup/configuration work. 512 MiB is the tested development minimum; 1 GiB is recommended for comfortable headroom. | Netgate documents 1 GiB or more as the minimum. Packages, state count, VPN, IDS/IPS, ZFS, and traffic can require more. |
| Disk | The application payload is small, but 8 GiB is currently recommended for Alpine, logs, snapshots, packages, and upgrades. | Netgate documents 8 GB or more as the minimum. |
| CPU | The narrow service set is expected to produce low idle CPU usage, but no fair cross-platform CPU or throughput comparison has been published yet. | Requirements depend on throughput, VPN cryptography, packages, state count, and traffic features. |
| Dashboard runtime | Static assets served by the Go process; Node.js is not installed on the router. | Integrated web configurator and the services required by the platform. |

A lower measured memory footprint is expected because Minimal Router OS supports
far fewer features. It should not be interpreted as equivalent functionality at
a lower cost.

Official pfSense references:

- https://docs.netgate.com/pfsense/en/latest/hardware/minimum-requirements.html
- https://docs.netgate.com/pfsense/en/latest/hardware/size.html

## DNS filtering and ad blocking

Minimal Router OS includes a basic global DNS sinkhole/blocklist in the same
configuration model and dashboard as DHCP and DNS. This is useful for simple
network-wide blocking without installing a second application.

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

| Capability | Minimal Router OS — current alpha | pfSense |
|---|---|---|
| Stateful firewall and NAT | Focused generated nftables policy | Comprehensive and mature |
| PPPoE | Implemented | Mature |
| DHCP and DNS | Implemented with dnsmasq | Mature resolver/forwarder and DHCP capabilities |
| WireGuard | Implemented for the focused remote-access workflow | Supported with broader configuration options |
| WAN web management | Intentionally unavailable | Configurable by the administrator, though secure deployment normally avoids direct exposure |
| Port forwarding | Intentionally rejected in the current secure profile | Fully supported |
| VLANs | Not yet a stable workflow | Mature |
| Multi-WAN | Not implemented | Mature |
| High availability | Not implemented | CARP and established HA workflows |
| IPv6 | Disabled and blocked until feature parity is complete | Mature IPv6 support |
| IDS/IPS | Not implemented | Available through packages such as Snort and Suricata |
| DNS blocklist | Basic integrated global sinkhole | Commonly provided through pfBlockerNG or another service |
| Wi-Fi AP | Optional, hardware-dependent, disabled by default | Usually delegated to separate access points; platform/hardware dependent |
| Configuration rollback | Transaction and commit-confirm design | Mature configuration backup and recovery workflows |
| Package ecosystem | No general ecosystem | Extensive optional package repository |
| Hardware/deployment history | Limited lab evidence | Extensive real-world deployment history |

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

Experiment with **Minimal Router OS** when the goal is to help develop a focused,
resource-efficient home-router appliance and the network can tolerate alpha
software, console access, and rollback to an established router.

Minimal Router OS should earn production trust through reproducible tests,
external review, hardware evidence, recovery drills, and stable releases—not
through comparison-table claims.