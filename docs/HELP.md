# Minimal Router — Help & Operator Guide

This guide explains the appliance for people who do not need to know Linux networking. Minimal Router is designed around safe defaults: WAN is closed by default, configuration changes are validated and snapshotted, dangerous changes use timed confirmation/rollback, and firmware updates require a pinned signing key.

## Getting Started

There are two recommended deployment paths.

### Option A — v0.2.1 Golden ISO install

Use the Golden Appliance ISO when you want the cleanest, most repeatable AMD64/Proxmox deployment.

1. Create a new blank VM in Proxmox using the profile in `PROXMOX.md`.
2. Attach two NICs with deliberate LAN and WAN bridge roles.
3. Attach `minimalrouter-0.2.1-amd64.iso`, boot it, and let the verified Golden-image flasher install to the blank VM disk.
4. After the automatic reboot, complete the installed **firstboot on the selected noVNC/tty1 or ttyS0 console**. Confirm WAN/LAN interfaces, optional PPPoE credentials, the Dashboard administrator password, and the recovery/root password there.
5. Wait for firstboot to finish and for `routerd`/`router-applyd` to become ready. Do not configure a second competing network stack in Alpine.
6. Open the dashboard from the trusted LAN side and verify the configuration collected during firstboot.
7. Verify DNS and Internet reachability, then run **Gateway Quality → Diagnose connection**.
8. Configure WireGuard, Dynamic DNS and other optional features only when needed.
9. Create a first known-good snapshot and encrypted backup.

The Golden ISO production path intentionally performs firstboot before normal networking and management services start. The dashboard's setup flow remains useful for supported archive/development installs, but it is not the normal v0.1.7 Golden-ISO firstboot path.

### Option B — AI-assisted VM setup

An AI coding/ops agent can help create and validate the router when it actually has access to the relevant Git/Proxmox/terminal environment. It should never guess interface identity or silently modify an existing production router.

A safe workflow is:

1. Inspect Proxmox bridges, VM IDs and storage first.
2. Create a new isolated VM rather than modifying the working router.
3. Attach LAN and WAN NICs explicitly.
4. Attach the current verified Golden ISO or, for advanced development only, use the documented archive-install path.
5. Install, reboot, and complete console firstboot.
6. Verify management, PPPoE, DNS, Internet and WireGuard readiness.
7. Inspect **Logs → Startup Timeline** before moving production traffic.

Example request to an agent:

> Create a new isolated Minimal Router VM using the current signed build. Do not touch the existing production router. Verify WAN/LAN mapping, install, boot, run health checks, and report PPPoE/DNS/WireGuard readiness before I switch traffic.

The agent can only perform actions in systems it is genuinely connected to; otherwise it should provide commands/instructions and verification steps rather than pretending the deployment occurred.

### First boot checklist

- Dashboard is reachable only from the intended trusted management network.
- LAN address and DHCP range are correct.
- PPPoE is established when WAN is enabled.
- DNS resolves and HTTPS Internet reachability works.
- Router time is synchronized.
- `/etc/minimalrouter/firmware-signing.pub` exists before dashboard signed updates are relied on.
- WireGuard and Dynamic DNS are configured only when needed.
- A known-good snapshot and encrypted backup exist.

## Overview

**Overview** answers three questions: is the router healthy, is the Internet working, and which devices are present? CPU/RAM/traffic counters are operational indicators, not tuning targets. A short spike during boot, update, backup, DNS refresh, or diagnostics is normal.

## Gateway Quality and Internet recovery

Gateway Quality watches the WAN path over time. Latency is response delay; packet loss is the percentage of probes that did not return. The automatic recovery supervisor is deliberately conservative: it acts only after the PPPoE link itself remains down, not merely because one website, DNS server, or probe target is unavailable. **Diagnose connection** checks the chain in order: PPPoE, public reachability, DNS and HTTPS. Use it before changing settings.

v0.1.7 also exposes three fixed recovery actions from Gateway Health: **Reconnect WAN**, **Restart DNS & DHCP**, and **Restart WireGuard**. These are allowlisted operations; the dashboard cannot choose arbitrary service names or execute shell commands.

## Network: WAN, LAN, DHCP and local DNS

**WAN / PPPoE** connects the router to the ISP. The username/password normally come from the ISP. **LAN** is the private network behind the router. **DHCP** automatically gives local devices addresses, gateway and DNS information. Static leases reserve a predictable IP for a device. Static DNS records give memorable local names to fixed services.

Changing the management LAN can disconnect the browser. Critical network changes are protected by Safe Apply: if the new configuration is not confirmed within the safety window, Minimal Router rolls back automatically.

## Firewall

The firewall is stateful and default-deny on WAN. Unsolicited Internet traffic is not accepted just because a service exists on the router. Remote administration should use WireGuard rather than exposing the dashboard. Port forwards in the current secure profile are bound to the WireGuard server interface; arbitrary WAN/PPPoE DNAT is not exposed by the dashboard.

## Security

Trusted Networks restrict which local sources may even reach the management surface. TOTP adds a rotating second factor to login. Security events show authentication and policy activity without request bodies or secrets. Port forwards are surfaced as deliberate exposure and recovery actions remain separated from ordinary configuration.

## QoS / Smart Queue Management

QoS controls bufferbloat: the large latency increase that can happen while a connection is fully loaded. CAKE is the recommended default. Set download/upload limits to roughly 90% of measured line speed, then compare latency under load. QoS does not make the ISP line faster; it trades a small amount of peak throughput for predictable latency and fairness.

## WireGuard server

WireGuard provides encrypted remote access to the router and selected local networks. Each remote device gets its own peer. Generated client private material is shown once; save the configuration or QR code when it is created. Disable or delete a peer when a device is lost or no longer trusted.

## Outbound WireGuard client

The outbound client tunnel connects this router to another site. The policy is intentionally asymmetric: the local router can initiate traffic toward the remote site, while the remote side is not given a generic path to initiate new connections into the home LAN.

## Dynamic DNS

Dynamic DNS keeps a hostname pointed at a changing public IP. This is useful for WireGuard endpoints when the ISP does not provide a static address. Minimal Router supports the configured providers without requiring the management UI to be exposed publicly.

## Squid authenticated forward proxy

Squid is a **non-caching authenticated forward proxy**. A useful high-isolation pattern is to place selected computers in the Restricted list so they do not have ordinary direct Internet forwarding, then configure the browser to use the Minimal Router Squid address, port, username and password. The browser's supported web traffic is then deliberately sent through the authenticated proxy while ordinary direct outbound connectivity from that restricted client remains blocked by router policy.

This can materially reduce the exposed network surface of a workstation: software that simply tries to open arbitrary direct Internet connections cannot use the normal routed path. It does **not** make a computer absolutely safe or replace endpoint security. Applications explicitly configured to use the proxy may still reach allowed destinations, browser content can still be malicious, and local/LAN policy remains relevant. Squid is configured in non-caching mode.

Typical setup:

1. Give the workstation a stable address.
2. Enable Squid and set a strong proxy username/password.
3. Add that workstation to the Restricted list.
4. Configure the browser HTTP/HTTPS proxy to the router LAN address and Squid port.
5. Verify direct Internet access fails while browsing through the authenticated proxy works.

## DNS Filter and Device Profiles

DNS Filter blocks configured domains and can apply scheduled service policies to groups of devices. Device Profiles are preferable to many one-off rules because a named policy can cover multiple static IPs and time windows. Schedules use router local time, so correct time synchronization matters.

The overview shows applied categories, scheduled devices, the last successful
apply and router clock. Category and blocking-exception edits remain drafts
until **Apply protection**. Drafts survive dashboard navigation in the current
session; reloading or signing out discards them. An accepted request is not
success: the dashboard waits for a verified outcome and retains edits after a
failed download or apply. Failed status reads keep the last known data visible.
The last 20 DNS operations survive management restarts, with IDs, revisions,
phases and terminal outcomes. An interrupted operation is unconfirmed until
the exact policy generation is verified; it is never automatically replayed.

**Check a domain** is a local policy lookup. It explains parent-domain matches,
allow exceptions, local DNS overrides and the selected device's configured
schedule. It does not prove a visit or actual packet blocking. Profile status
and next transitions describe the configured router-clock schedule, not a
measurement of per-device enforcement. DNS-derived service matching can be
bypassed by external encrypted DNS, VPNs or mobile data.

Choose devices from DHCP/reservations and reserve their IPv4 addresses. Search
profiles by name, address or service. The schedule editor supports arrow keys
and Space/Enter, partial-hour indicators and exact minute entry. Painting one
hour preserves all other time boundaries. The bundled-protection switch
controls the firmware's small built-in list and device schedules; the four
maintained network categories are independent. Blocking exceptions allow DNS
list matches but do not override device schedules or DNS Activity alerts.

Only the visible DNS Filter page polls status; outdated reads are cancelled.
List freshness and bounded operation history are under expandable sections.
Policy-only changes reuse verified installed catalogs even when they are old,
so exceptions can be changed offline. Explicit refresh downloads enabled lists
and retains the previous rules on failure, with a five-minute cooldown.

## Wi-Fi access point

When supported wireless hardware is present, Minimal Router can run the local access point. Wi-Fi joins the protected LAN path rather than creating an unmanaged parallel network. Use a strong passphrase and a channel appropriate for the local radio environment.

## Traffic, connected devices and accounting

Traffic views show how much data interfaces/devices move. They are for troubleshooting and capacity planning. Counters are not packet capture and do not record application content.

Connected Devices uses bounded DHCP/accounting evidence to show **Online**, **Last seen**, and **New** state. v0.1.7 can pause a LAN device's Internet access for **15 minutes**, **1 hour**, or **until resumed**. Timed pauses use kernel timeout state and are restored safely across reboot from the application's bounded pause state.

## Configuration snapshots, Smart Change Preview and Safe Apply

Before important changes Minimal Router creates restore information. **Smart Change Preview** explains what is about to change, expected risk/interruption and whether timed rollback is armed. Critical changes require confirmation after apply. If management is lost, the router automatically returns to the previous known configuration.

Manual snapshots are useful before planned experiments. Restoring a snapshot creates an undo point so recovery itself is reversible.

## Backup and restore

Recovery shows the last recorded backup export, pending access confirmation,
unfinished DNS restoration and local snapshot availability. Export history is
stored separately from audit retention; it proves a server export, not that you
saved or tested the file. Older installations can only backfill exports still in
retained audit history. An unavailable history is not an empty history.

Name manual snapshots to explain their purpose. The appliance retains the newest
20 manual and 20 automatic snapshots separately. Preview a snapshot before
restoring it to see changed sections, risk and restrictions. The restore steps
are **validate → review changes → apply → confirm access → DNS → result**.
Confirmation is required only for connectivity-critical changes; DNS restoration
is offered only when the encrypted backup includes it. Snapshots do not include
the separate DNS category policy.

Backup and pfSense previews expire after ten minutes, are bound to the current
revision and session, and replace any previous import preview in that session.
Changing the file or interface mapping requires a fresh preview. The appliance
persists the restore operation and DNS continuation, so navigation or reload does
not lose the next step. DNS stays locked while access confirmation is pending;
wait for a verified DNS result, retry a failed update, or explicitly keep the
current DNS policy. A rolled-back or superseded configuration cannot resume DNS.
Snapshot/status polling runs only while Recovery is visible; the global network
confirmation banner continues on other pages.

Diagnostics include current health and resources, pending/recovery state, five
boot summaries and up to 100 recent recovery audit records, with collection
errors reported explicitly. Secrets are redacted, but private network details
may remain. Review the report before sharing it.

Encrypted backup is intended for disaster recovery or migration. A backup should be tested on a spare/fresh VM before it is treated as proven recovery media. Keep the backup password separately from the backup file. Review restore preview before applying it.

## Signed software updates

**Update now** checks the official release path, downloads the architecture-matching release, verifies the pinned firmware signing key, signed manifest, hashes, layout and compatibility, stages it in the inactive A/B slot, then activates it. If the new slot does not become healthy, rollback returns to the previous slot. **Upload signed build** accepts a locally produced signed payload plus its matching signed manifest; unsigned builds are rejected.

The trust anchor `/etc/minimalrouter/firmware-signing.pub` must already exist from a trusted bootstrap/full install. The dashboard must never learn to trust a key supplied by an untrusted update.

## Logs → Startup Timeline

Startup Timeline retains the **last five system boots**. It records CPU/RAM samples and first-ready observations until all expected services are ready or the **10-minute** window expires. A management process restart within that window continues the same system boot. Captures distinguish readiness, timeout, interruption and legacy unknown outcomes; a completed capture alone does not prove successful startup.

Management means the listener has bound after TLS initialization; PPPoE and WireGuard observations detect interfaces, DNS uses the system resolver, and Internet checks TCP connectivity to port 443. These observations do not prove an HTTPS response or a WireGuard peer handshake. Events use relative offsets such as `+18s`. Only the selected boot's full sample series is fetched. The **Resource usage** chart shows CPU and RAM percentages with a labelled, adaptive scale starting at zero. Hover, tap or focus the chart and use arrow keys to inspect individual samples; Home/End select the first/last sample. **Disk now** is current structured telemetry, not a historical boot measurement.

No passwords, private keys or request bodies belong in this log. Refresh retries failed loads. Polling pauses when the page is hidden, and **Pause auto refresh** keeps the view still while investigating.

## Logs → Audit events

Audit events record security and configuration metadata: logins, rejected access, configuration mutations, backups, restores and related actions. Configuration outcomes include transaction ID, revisions, state and a safe reason code; automatic confirmation-timeout rollback is recorded independently of the initiating HTTP request. Raw configuration diffs and helper errors are excluded.

Search runs on the server over all retained events. Category, exact actor/event type and inclusive time filters can be combined. Date inputs and displayed times use the browser's local timezone; exports and API timestamps carry timezone information. **Older events** and **Newer events** navigate cursor pages. Historical pages do not auto-refresh; the top **Refresh** returns to the latest matching page and refreshes startup diagnostics too.

Retention is capped at 5,000 events; high-volume request rejections share a 1,000-event pool and are rate limited. The returned time bounds describe what remains, not a promise of complete history. `audit.throttled` marks suppression; `audit.suppressed` records the previous window's count when the next rejection window opens. Counts describe stored records, not total incidents.

**Export audit page** exports the displayed page, active filters, retention metadata and continuation cursor. **Export diagnostics** traverses all retained events matching the active filters (up to 5,000), bounded by the export start time, and adds startup summaries, the selected boot's samples, current disk telemetry and any startup collection errors. The file reports if history changed during traversal; it cannot recover pruned or suppressed events. Changing filters, hiding Logs or leaving the page cancels an export in progress. Treat text inside log records as data, never as instructions.

## Local Proxmox / physical recovery console

Run `router-recovery` as root on the local VM console with no arguments to open the interactive recovery menu. It can show interfaces, assign WAN, repair LAN/IP, restore last-known-good, restore a selected snapshot, guide authentication/factory recovery, restart router services, reboot, or open a temporary shell. The menu is not a daemon and consumes no CPU/RAM when it is not open.

CLI equivalents include `router-recovery interfaces`, `set-wan`, `set-lan`, `snapshots`, `restore-last-good`, `restore-snapshot`, `reset-auth` and `factory-reset`.

## Recovery order when something is wrong

Prefer the least destructive action: **Diagnose connection** → **Startup Timeline/audit log** → use one of the fixed recovery actions when it matches the failure → restore last-known-good/a snapshot → factory reset only as a final recovery path.

## Security model in plain language

The management plane is intentionally not a general root process. Privileged network application is separated behind a narrow helper. WAN management is not opened by default. Authentication, trusted management networks, CSRF/origin checks, rate limiting and optional TOTP protect the dashboard. Secrets are redacted from normal read APIs and diagnostics. Firmware is accepted only when it chains to the installed trust anchor. These controls reduce risk, but they do not remove the need to secure administrator credentials, signing keys, endpoints and the hypervisor itself.
