# 0004 — Isolated IoT zone and fixed-device schedules

- Status: Accepted
- Date: 2026-07-30
- Owners: project maintainers

## Context

Household users need a small number of understandable controls: isolate less
trusted IoT devices from the main LAN, and limit a child device to defined hours.
A general VLAN platform, transparent proxy, TLS inspection system, or full
parental-control product would substantially enlarge the attack and maintenance
surface.

A schedule shown only in the dashboard is not enforcement. Matching an arbitrary
DHCP address is also unsafe because it may move to another device. The router
therefore needs a stable reservation, explicit topology, deterministic generated
rules, and rollback for lockout-prone changes.

## Decision

Minimal Router supports one optional routed IPv4 IoT zone:

- `dedicated` mode owns one selected physical interface;
- `vlan` mode creates only the fixed project-owned `mr-iot` interface on one
  validated parent and VLAN ID;
- the zone has its own gateway, tagged dnsmasq DHCP pool, and static leases;
- nftables blocks LAN-to-IoT and IoT-to-LAN forwarding before accepting
  established traffic;
- router management and proxy services are not exposed on the IoT interface;
- IoT topology changes use the normal snapshot, apply, verify, commit-confirm,
  and rollback pipeline.

Device policy assignments must match a static DHCP reservation by zone, IPv4
address, and MAC address. Profiles contain full weekday names and either an
all-day window or a non-overnight `HH:MM` interval. Rules are generated before
the generic established-state and LAN/IoT-to-WAN accepts.

The first service groups are YouTube and Steam. dnsmasq populates volatile,
project-owned IPv4 nftables sets from reviewed domain groups. This classification
is explicitly best effort and is not TLS/content inspection.

The appliance timezone is stored in canonical configuration, validated against
installed zoneinfo, and atomically activated. `chronyd` is enabled by packaging
because wall-clock correctness affects schedules, TLS, TOTP, and audit events.

## Consequences

- A dedicated IoT port requires another NIC. VLAN mode requires a correctly
  configured managed-switch/access-point trunk outside the router.
- Same-L2 IoT clients are not isolated from each other by routed firewall rules.
- A provider domain or shared CDN change can cause false positives or false
  negatives in service-only access.
- Devices using randomized MAC addresses must disable that behavior for the
  selected network or they cannot retain the required reservation.
- IPv6 remains disabled; this decision does not introduce IPv6 schedule parity.
- Notifications, a general VLAN manager, and arbitrary service definitions are
  outside this decision.

## Alternatives considered

- **Device labels on the main LAN:** rejected because Layer-2 peers can bypass the
  router and no isolation boundary exists.
- **MAC matching only in nftables:** rejected because routed IP policy and DHCP
  ownership still require a stable address; MAC visibility also changes across
  routed boundaries.
- **Transparent proxy/TLS inspection:** rejected due to certificate interception,
  privacy, application breakage, privilege, and maintenance cost.
- **External parental-control cloud:** rejected as a mandatory dependency and
  privacy boundary; optional future integrations require a separate decision.
- **General VLAN and switch controller:** rejected for the current narrow scope.

## Validation

- unit validation rejects overlapping subnets, reused interfaces/MACs, unknown
  services, invalid timezones, overnight windows, and assignments without an
  exact reservation;
- generator tests prove tagged DHCP pools, nftables service sets, LAN↔IoT drops,
  weekday/weekend expressions, and rule ordering before established-state accept;
- clean Alpine CI must parse the generated dnsmasq and nftables configurations;
- reference-hardware testing must verify a dedicated port and managed-switch
  trunk, reboot reconciliation, rollback, schedule cutoff, DNS cache behavior,
  and daylight-saving transitions before production claims.
