# Dashboard design

Minimal Router uses a calm, minimal appliance UI: clear hierarchy, restrained
color and technical detail only where it helps the operator make a decision.

## Principles

- **Status before statistics.** Say online, degraded or offline before showing raw numbers.
- **One clear action.** Avoid competing primary buttons.
- **Progressive detail.** Keep the overview concise; configuration belongs in dedicated sections.
- **Semantic color only.** Blue = action/selection, green = healthy, amber = warning, red = failure/destructive.
- **No fake telemetry.** Missing data is shown as unavailable, never simulated.
  A status element must be driven by a measurement, not by the presence of
  configuration: a service being *enabled* is not evidence that it is *working*.
  A failed probe is drawn as a gap, never as a zero.
- **Safe changes are visible.** Confirmation, rollback and recovery states must be obvious.

## Current desktop shell

The dashboard uses:

- persistent numbered navigation on the left;
- compact service-status chips across the top;
- a wide Overview canvas centered in the remaining space;
- light neutral background, white cards, thin borders and limited shadows;
- dark mode using the same hierarchy.

The Overview is intentionally ordered as:

0. the appliance-health banner, which is the single answer to "does this router
   need attention?" and the only element allowed to summarise overall state;
1. appliance/Internet status and PPPoE state;
2. public IP, uptime, MTU, revision and update trust;
3. storage, conntrack and time-sync chips;
4. gateway latency, packet loss and PPPoE uptime;
5. CPU, memory and disk;
6. live bandwidth and gateway-quality charts;
7. searchable connected-device table.

## Status chips

Top-bar chips provide quick state for Firewall, WireGuard, DHCP, DNS, Dynamic DNS,
Cloudflare Tunnel and gateway quality. Counts are shown only when useful, such as
active/total WireGuard peers or current DHCP leases.

Healthy services are quiet green; disabled services are neutral; degraded states
use amber/red only when attention is needed.

## Cards and charts

Cards use plain values rather than gauges. Large numbers are reserved for values
that are useful at a glance.

Charts should remain simple:

- at most two lines per chart;
- no decorative gradients or animation;
- bandwidth and gateway quality are separate visual questions;
- empty history shows a clear waiting/unavailable state.

## Connected devices

The device view should support quick scanning and actions without becoming a
full network-management table. v0.1.6 combines hostname/IP/MAC and lease data
with **Online**, **Last seen**, and **New** state derived from bounded DHCP and
accounting evidence. Static leases are clearly marked, Wake-on-LAN remains
available where applicable, and a device can be paused from Internet access for
15 minutes, 1 hour, or until resumed.

Search filters by device name, IP or MAC.

## Navigation

Current sections are:

1. Overview
2. Gateway Quality
3. LAN & DHCP
4. Firewall
5. WireGuard
6. Dynamic DNS
7. Traffic
8. Squid Proxy
9. DNS Filter
10. Wi-Fi AP
11. Recovery
12. Security
13. Logs

Features that are disabled or unavailable should stay explicit rather than being
hidden or presented as active.

## Responsive behavior

Below desktop width the status row may reduce secondary chips. On mobile v0.1.6
uses a fixed top-right navigation control: opening the menu reveals navigation
behind a pushed/scaled foreground page rather than replacing the app with an
unrelated mobile layout. The menu closes with the same control, Escape, an
exposed-page click, or section navigation, and the previous scroll position is
restored when appropriate.

On mobile:

- content becomes one column;
- cards keep readable padding;
- tables may scroll horizontally when stacking would lose important network data;
- startup diagnostics use a horizontal, scrollable milestone sequence;
- controls need comfortable touch targets;
- the first-run and recovery flows must remain fully usable.

## Typography and accessibility

Use the system/Inter-style sans-serif stack already defined by the dashboard.
Do not bundle proprietary fonts.

Requirements:

- readable contrast in light and dark themes;
- visible keyboard focus;
- labels not dependent on color alone;
- semantic HTML for tables/forms/status messages;
- reduced motion respected where motion exists.

## Implementation rule

The React source should keep presentation in CSS rather than runtime inline style
objects. CI enforces this boundary. Visual changes should preserve the current
information hierarchy unless there is a clear usability reason to change it.
