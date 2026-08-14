# Central appliance health

Minimal Router OS exposes one authenticated, read-only appliance-health summary instead of requiring an operator to infer health from unrelated dashboard cards.

## States

The aggregate state is one of:

- **Healthy** — measured core signals are within normal operating bounds.
- **Warning** — the router is operating, but one or more conditions need attention.
- **Degraded** — an enabled/configured subsystem is unavailable or a resource is near exhaustion.
- **Recovery required** — canonical runtime state cannot be trusted until explicit reconciliation succeeds. This has the highest severity.
- **Unknown** — a signal cannot be measured reliably. Unknown is never silently converted into Healthy.

## Signals

The health endpoint combines read-only evidence from:

- configuration transaction/recovery state;
- storage pressure and durable-write availability;
- memory usage;
- conntrack usage;
- kernel time synchronization;
- WAN/PPPoE connection and gateway-quality monitoring;
- OpenRC state and supervised child processes for `routerd` and `router-applyd`;
- the protected `router-applyd` Unix socket;
- configured dnsmasq DNS/DHCP service state;
- configured PPPoE OpenRC service state;
- configured WireGuard interface state;
- signed-update trust and pending update state;
- age of the latest successful encrypted backup export visible in retained audit metadata.

The health collector never reads PPPoE passwords, WireGuard private keys, Cloudflare tokens, administrator credentials, backup payloads, or other secrets.

## API

`GET /api/v1/health` requires an authenticated session and returns the aggregate state plus individual checks. It uses the same security-header and session middleware as the rest of the management API.

Health collection is observational only. It does not restart services, reconnect PPPoE, apply firewall rules, modify WireGuard, rotate secrets, or automatically remediate failures.

## Dashboard

Overview displays one central health banner (`web/src/components/HealthBanner.tsx`). By default it lists only the checks that need attention; "Show all checks" expands the full list. The banner refreshes independently every 15 seconds so it remains useful while other dashboard sections are open and closed.

Two other surfaces are driven by the same endpoint rather than by configuration:

- the **DNS** status chip reflects the measured `dns_dhcp` check instead of being unconditionally green;
- the **notification bell** reports the actual number of non-healthy checks, and its label names them.

Storage pressure additionally has its own Overview chip and, at warning and critical levels, an inline banner — so an operator sees the problem before a configuration save starts returning HTTP 507.

When the health endpoint itself cannot be read the banner says so explicitly. Unknown is never rendered as healthy.

## Thresholds

Current resource thresholds are deliberately simple and explicit:

- storage: Warning at 80%, Degraded at 90% (where durable writes are blocked by the storage-pressure layer);
- memory: Warning at 80%, Degraded at 90%;
- conntrack: Warning at 75%, Degraded at 90%;
- encrypted backup age: Warning after 7 days, Degraded after 30 days.

These are management-health thresholds, not automatic remediation triggers.

## Validation boundary

Automated tests cover aggregate severity ordering and deterministic resource/recovery states. Linux service/process facts are also exercised by the existing clean-Alpine and supervision workflows. Real Proxmox, ISP PPPoE, NIC behavior, destructive full-disk/inode exhaustion, read-only filesystem handling, external scans and sustained hardware soak remain production gates before claiming appliance readiness.
