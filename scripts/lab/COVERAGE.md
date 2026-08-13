# Lab coverage assessment

This is risk coverage, not line or branch coverage. A scenario counts only
when it exercises an observable failure contract and leaves the lab converged.

## Current estimate

- Core supported control-plane and network behavior: approximately **83%**.
- Whole-appliance, real-field failure space: approximately **72%**.
- Safely revalidated on the corrected topology: use
  `agent-run-next.sh status`; historical results from the old host-routed API
  harness are not accepted as proof.

The 150 scenarios strongly cover PPPoE/WAN faults, LAN/DHCP/DNS, firewall and
authentication boundaries, WireGuard, configuration transactions, rollback,
storage pressure, updates, optional services and API validation.

## Largest remaining gaps

1. IPv6 WAN/LAN, router advertisements, DHCPv6 and IPv6 firewall policy.
2. Real Wi-Fi radio/driver behavior, interference, roaming and regulatory
   channel constraints (scenario 150 currently checks validation only).
3. Real DDNS and Cloudflare success/renewal paths, including provider outages
   and credential rotation.
4. TLS certificate expiry, renewal, clock skew and trust-chain rotation.
5. Multi-hour and multi-day soak tests for memory, file descriptors, SQLite,
   conntrack, DHCP leases and repeated reconciliation.
6. Larger client/peer scale: dozens of DHCP clients, WireGuard peers and
   concurrent administrators.
7. Sustained throughput, bufferbloat and QoS fairness under bidirectional load.
8. Physical failure modes: disk corruption/wear, NIC resets, link flaps,
   unexpected power loss timing and thermal throttling on target hardware.
9. Upgrade/rollback matrices across several released versions and corrupted or
   partially downloaded artifacts.
10. Browser end-to-end coverage of every dashboard mutation, accessibility and
    mobile/responsive operation against the live appliance.

Percentages should rise only when a gap is represented by a deterministic test
and that test passes repeatedly on the isolated VM150/151/153/154 topology.
