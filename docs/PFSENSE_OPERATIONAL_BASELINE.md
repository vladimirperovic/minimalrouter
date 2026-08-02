# pfSense operational lessons applied to Minimal Router OS

- Status: engineering baseline
- Date: 2026-07-31
- Scope: focused home and small-office IPv4 router appliance

This review uses mature pfSense operational practices as a checklist, not as a
feature-parity target. Minimal Router OS intentionally remains smaller and does
not copy platform-specific tunables without checking whether they make sense on
Alpine Linux, nftables, PPPoE, WireGuard, and Proxmox VirtIO.

## Controls already present

The current implementation already covers several failure classes commonly
addressed in mature pfSense deployments:

- default-deny WAN input and forwarding;
- stateful filtering with invalid-packet rejection;
- WAN bogon/private-source filtering and FIB source validation;
- LAN source anti-spoofing;
- LAN management anti-lockout unless the operator explicitly commits to
  WireGuard-only management;
- DNS rebinding protection in both the management HTTP boundary and dnsmasq;
- TCP MSS clamping for PPPoE paths;
- commit-confirm and verified rollback for disruptive changes;
- encrypted backup export, checksummed snapshots, and local recovery tools;
- WAN management closed by design, with WireGuard as the external entry path;
- client schedules enforced before established-state acceptance so expired
  sessions are cut instead of remaining active indefinitely.

## Changes adopted by this baseline

### Loose reverse-path filtering

Linux strict reverse-path filtering (`rp_filter=1`) is unsafe as a universal
router default when legitimate traffic can be asymmetric across PPPoE,
WireGuard, bridges, or future policy routes. The appliance now uses loose mode
(`rp_filter=2`) globally and by default. Interface-specific nftables anti-spoof
rules remain the stronger enforcement boundary.

### Explicit state-table ceiling and visibility

The appliance now loads `nf_conntrack` before applying sysctls and sets an
explicit ceiling of 131072 states for the supported 512 MiB-or-larger appliance
class. `/api/v1/system` reports current state count, maximum state count, and
percentage usage so pressure can be diagnosed before new connections fail.

The ceiling is intentionally conservative and must be validated under the
owner's real traffic, especially peer-to-peer workloads, large device counts,
or unusually high connection churn.

### Clock synchronization as a core service

TOTP authentication, TLS validity, weekly access schedules, audit ordering, and
signed-update verification require trustworthy time. Alpine installations now
include and enable Chrony with multiple pool sources, fast initial correction,
and RTC synchronization.

Chrony is configured as a client only:

- NTP server port disabled (`port 0`);
- remote command port disabled (`cmdport 0`);
- no LAN or WAN client allowlist;
- no additional router listener.

The runtime status reports whether the Linux kernel currently considers the
clock synchronized.

## Practices deliberately not copied

### Disabling checksum/segmentation offload by default

pfSense documentation includes important FreeBSD and virtual-NIC offload
troubleshooting guidance. Minimal Router OS runs Linux, where VirtIO offloads are
normally useful and disabling them globally would reduce throughput. Offload
settings remain a measured Proxmox/NIC troubleshooting variable, not a default
workaround.

### Broad package ecosystem and WAN port forwarding

The project does not adopt pfSense's general package model or arbitrary port
forwards. Both increase attack surface and configuration complexity. The current
secure profile keeps WireGuard as the only external entry point.

### Multi-WAN, HA, VLAN, and IPv6 parity

These require dedicated models, policy routing, health monitoring, recovery
semantics, and hardware testing. They must not be represented as solved by a
sysctl or firewall snippet.

## Remaining production gates

The following mature-firewall practices still require separate implementation
or target-hardware evidence:

- gateway-quality monitoring and alarm policy, including PPPoE reconnect loops;
- off-box automated encrypted backup with tested restore and retention;
- state-pressure warning thresholds in the dashboard and optional alerting;
- real Proxmox VirtIO/offload measurements and repeated interface-identity tests;
- external IPv4 scanning and prolonged state-exhaustion testing;
- real ISP PPPoE and WireGuard throughput/recovery testing;
- at least seven days of sustained operation and independent security review.

## References

- Netgate pfSense documentation: firewall rule best practices, anti-lockout,
  DNS rebinding protection, state sizing/diagnostics, NTP, backup and restore,
  gateway monitoring, and hardware offload troubleshooting.
- Linux kernel IP sysctl documentation for reverse-path filtering and conntrack.
- Alpine Linux 3.22 Chrony and OpenRC package documentation.

The exact external references used for this review are recorded in the pull
request description so the review can be repeated against future upstream
versions.
