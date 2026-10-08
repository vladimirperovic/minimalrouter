# Dashboard review for v0.2.0

Reviewed all 15 navigation pages and the Updates entry against their React
components and API data. This is a product/code review, with automated browser
coverage for the changes, not a claim that each optional service was exercised
on physical hardware. The priority is a reliable household-wide warning when
the router sees a risky domain; maintaining a child/device identity inventory
is not required.

| Sidebar item | Existing useful behavior | v0.2.0 result and next useful addition |
| --- | --- | --- |
| Overview | Appliance health, WAN/resources, traffic and recently active devices | Added a DNS risk summary and navigation to alerts. Later: a compact recent incident timeline combining WAN and security events. |
| Gateway Quality | Latency/loss/jitter history, public IP changes, connection diagnosis, targeted service restarts and conservative automatic WAN recovery | Already strong. Later: an outage-duration summary and exportable incident report. Keep recovery tied to verified outages. |
| LAN & DHCP | Uplink/LAN/DHCP settings, pool occupancy, known devices, reservations, local DNS and pause/resume | Corrected configured-versus-measured status labels, including upstream DNS encryption state. Later: DHCP exhaustion/conflict warnings; do not assume MAC/IP is a stable person identity. |
| Firewall | Default-deny posture, rules/presets, tunnel forwarding and aggregate allowed/blocked history | No new policy needed for monitoring. Later: per-rule counters and a rule-conflict preview, through the existing privileged boundary. |
| Security | Trusted networks, management access, TOTP and security audit events | Added DNS risk summary/link. Later: active administrator session inventory and per-session revocation. |
| DNS Filter | Upstream resolvers, a built-in global ad/tracker blocklist and scheduled service rules for static-address device profiles | Added opt-in network category blocking, maintained HaGeZi lists, subtree exceptions, local domain checks and verified activation/rollback. Fixed stale profile edits, lost minute precision and global-switch changes; preserved live service-set destinations. DNS Activity monitoring remains separate. |
| QoS / SQM | Measured qdisc state, CAKE/FQ-CoDel configuration, speed measurement and limit suggestions | Keep current functions. Later: compare idle versus loaded latency and show qdisc drop/backlog counters before proposing automatic tuning. |
| WireGuard | Peer provisioning/QR/download, handshake and byte counters, enable/disable, rename and outbound client tunnel | No immediate new control needed. Later: distinguish an idle peer from a failed path and add bounded reachability diagnostics. |
| DynDNS | Cloudflare/No-IP settings, updater state, last address/update time | Removed “In sync” claim based only on process-running state. Next priority: authoritative/public DNS verification with a clear last-success and provider-error display. |
| Wi-Fi AP | Optional local radio configuration, SSID/channel/security controls | Labels now say configuration enabled, not measured radio activity. Next priority: hardware capability detection, hostapd state and associated clients; relevant only when a local radio is present. |
| Traffic | Time history, per-address usage, monthly totals, busiest devices and shares | Keep byte accounting separate from browsing. Later: custom dates/export and thresholds. Address changes limit attribution; totals are not proof of screen time. |
| DNS Activity | Opt-in aggregates and in-memory recent names | Main addition: network-wide risk lists, reviewable daily alerts, exceptions, filters/pagination, list health and collector gaps. Also fixed disappearing device options, debounced search and refresh after actions. See `DNS_RISK_ALERTS.md`. |
| Squid Proxy | Optional authenticated LAN-only non-caching proxy settings | Labels now distinguish enabled configuration from a running listener. Next priority: measured process/listener state and a connection test. TLS inspection is not needed for this feature. |
| Recovery | Verified configuration snapshots, encrypted backup import/export, migration preview and diagnostics | Redesigned backup/restore workflows. New v2 backups use the dashboard password and include the network DNS policy; old separate passwords remain supported on import. Clearly separate portable backups, local snapshots, diagnostics and console repair. Later: snapshot/config diff preview and a measured backup-age reminder. |
| Logs | Redacted audit metadata, filters/export and boot timelines | Risk review/exception/update actions are metadata-only audit events; browsing domains stay in DNS Activity. Later: server-side time filtering and pagination beyond the current recent-event window. |
| Updates | One shared update dialog, signed uploads, compatibility reasons, progress and reconnect handling | Prepared 0.2.0 metadata and release notes. Preserve signature/architecture/rollback gates. Later: surface candidate release notes beside compatibility status. |

## Priorities after this release

1. Qualify full-list DNS memory/latency and device schedules on real appliances;
   add service-set occupancy and measured blocked-query counters.
2. Out-of-dashboard notifications with deduplication and operator-controlled
   destination/privacy settings. No external destination is configured here.
3. Measured runtime states for optional services and verified DynDNS success.
4. DHCP capacity/conflict diagnostics, WAN incident summaries and per-rule
   firewall counters.

Adding every proposed function at once would require unrelated networking and
hardware validation. This candidate implements DNS monitoring and opt-in
blocking, repairs profile editing, redesigns Recovery, fixes misleading status
claims and standardizes card gaps across all pages. Other items remain follow-up
work.

A subsequent [detailed DNS Filter review](DNS_FILTER_REVIEW.md) records concrete
profile-edit and enforcement-lifecycle findings, their evidence and the proposed
whole-network protection layout. Its implementation follow-up links to the
new controls and their documented limits in [DNS_FILTER.md](DNS_FILTER.md).
