# Design System

- Status: Accepted
- Direction: Apple × Swiss
- Last updated: 2026-07-24

## 1. Design intent

Minimal Router OS should feel calm, obvious, and trustworthy. The interface
combines two complementary traditions:

- **Apple-inspired product design:** approachable controls, strong hierarchy,
  generous space, excellent defaults, and progressive disclosure.
- **Swiss graphic design:** precise grids, disciplined typography, restrained
  color, direct language, and information that can be scanned quickly.

This is an original design system inspired by those principles. It must not
copy Apple screens, trademarks, icons, fonts, or proprietary assets.

The visual reference selected for the overall tone is the
[Apple × Swiss Premium Minimalist UI](https://blink.new/p/apple-swiss-premium-ui-09h3wg29).
The product structure remains specific to a router and follows `PROJECT.md`.

## 2. Product experience

The interface should communicate:

- The internet is working—or exactly what needs attention.
- Important changes are safe and reversible.
- Common tasks do not require networking knowledge.
- Advanced details exist, but never dominate the first view.
- The router is a dependable household appliance, not an enterprise cockpit.

The user should be able to answer these questions within five seconds of
opening the dashboard:

1. Am I online?
2. Is the connection healthy?
3. How many devices are connected?
4. Are WireGuard and Cloudflare running?
5. Is the router itself healthy?

## 3. Design principles

### 3.1 Status before statistics

Show a clear human status such as **Connected**, **Needs attention**, or
**Offline** before showing IP addresses, rates, or diagnostic codes.

### 3.2 One obvious primary action

Each page has at most one emphasized primary action. Secondary actions are
quiet; destructive actions are separated and never visually compete with safe
actions.

### 3.3 Progressive disclosure

Show the smallest useful summary first. Put technical values, logs, and
component-specific details behind “Details” or an expandable section.

### 3.4 Precision without density

Use a strict grid and aligned values, but preserve breathing room. Empty space
is part of the interface, not space waiting for another card.

### 3.5 Color has meaning

Blue means action or selection. Green means healthy. Amber means degraded or
waiting. Red means failed, dangerous, or destructive. Do not use semantic
colors as decoration.

### 3.6 Motion explains state

Motion may connect an action to its result or clarify a transition. It must not
decorate a dashboard that is otherwise idle.

### 3.7 Safe change is visible

Configuration changes visibly move through validation, snapshot, apply,
verification, and commit. Rollback is presented as a protection, not as a
cryptic failure.

## 4. Visual direction

The default appearance is light, neutral, and softly layered:

- Warm-neutral light gray application canvas
- White or lightly translucent surfaces
- Near-black typography
- Hairline separators
- One blue interaction accent
- Sparse, high-confidence status colors
- Large numeric values only when they answer a real user question
- Rounded geometry with restrained shadows

Dark mode is supported from the beginning and follows the operating-system
preference. It uses graphite surfaces rather than pure black and keeps the same
information hierarchy.

Avoid:

- Glass effects that reduce readability
- Neon gradients
- Heavy drop shadows
- Decorative 3D illustrations
- Multiple competing accent colors
- Gauge clusters, donut charts, and “analytics wall” layouts
- Tiny uppercase text for important content
- Monospace typography for ordinary labels or body copy

## 5. Layout

### 5.1 Desktop shell

- Minimum supported width: `1024px`
- Content maximum width: `1440px`
- Persistent sidebar: `240px`
- Main content padding: `32px` at standard desktop widths
- Twelve-column content grid with `16px` gutters
- Top page header remains visually light and is not a second navigation bar

```text
┌──────────────────────┬───────────────────────────────────────────────────┐
│ Minimal Router       │ Dashboard                           ● Connected    │
│                      │ A concise summary of your network                  │
│ ● Dashboard          ├───────────────────────────────────────────────────┤
│   Internet           │                                                   │
│   LAN & DHCP         │ ┌───────────────────────────────────────────────┐ │
│   Static Leases      │ │ Internet                                     │ │
│   Firewall           │ │ ● Connected               924 ↓   96 ↑ Mbps  │ │
│   WireGuard          │ │ Public IP · PPPoE · uptime                   │ │
│   Cloudflare         │ └───────────────────────────────────────────────┘ │
│                      │                                                   │
│   Backup & Restore   │ ┌───────────────┐ ┌───────────────┐ ┌──────────┐ │
│   Updates            │ │ 12 devices    │ │ WireGuard On  │ │ CF Ready │ │
│   System             │ └───────────────┘ └───────────────┘ └──────────┘ │
│                      │                                                   │
│ v1.0 · Help          │ ┌────────────────────────┐ ┌────────────────────┐ │
│                      │ │ Router health          │ │ Recent change      │ │
│                      │ │ CPU · RAM · Disk       │ │ Snapshot protected │ │
│                      │ └────────────────────────┘ └────────────────────┘ │
└──────────────────────┴───────────────────────────────────────────────────┘
```

The shell uses a quiet sidebar and a spacious main canvas. Cards align to the
grid; they do not all need the same height.

### 5.2 Tablet

At `768–1023px`:

- Sidebar collapses to `72px` icons with accessible labels/tooltips.
- Main padding becomes `24px`.
- Twelve columns become six.
- The internet status card remains full width.

### 5.3 Mobile

Below `768px`:

- Sidebar becomes an off-canvas navigation drawer.
- Main padding becomes `16px`.
- Cards use one column.
- Tables become stacked records or horizontally scroll only when the data
  cannot be represented safely another way.
- Primary actions may be sticky near the bottom, above safe-area insets.
- Touch targets are at least `44 × 44px`.

The first-run wizard must work completely at `360px` width.

## 6. Navigation

Primary navigation order:

1. Dashboard
2. Internet
3. LAN & DHCP
4. Static Leases
5. Firewall
6. WireGuard
7. Cloudflare

Lifecycle navigation is visually separated:

8. Backup & Restore
9. Updates
10. System

Navigation requirements:

- Use a simple line icon plus a text label.
- Selected navigation uses a soft blue background, blue icon, and dark label.
- Do not rely on color alone; selection also has weight and shape.
- Show no numeric badges unless the number requires action.
- A red or amber status marker appears only when that section needs attention.
- Keep “Help” and version/build information at the bottom.

## 7. Dashboard information architecture

The dashboard contains no more than six primary regions.

### 7.1 Internet hero

The largest region answers whether the router is online.

Show:

- Connected / Connecting / Degraded / Offline
- PPPoE status
- Public IP address
- Current download and upload rate
- Connection uptime
- One contextual action such as **View internet settings**

Do not show a large speed-test dial. Speed test is an explicit action on the
Internet page.

### 7.2 Connected devices

Show:

- Online device count
- A small preview of the most recently active devices
- Wired/WireGuard distinction only when useful
- **View all devices**

Do not imply Wi-Fi radio data when the appliance does not control Wi-Fi.

### 7.3 Secure connectivity

WireGuard and Cloudflare appear as two compact status cards:

- Service state
- Number of active WireGuard peers, if known
- Cloudflare DDNS/Tunnel state
- Last successful check

### 7.4 Router health

Show CPU, memory, disk, temperature when available, and uptime. Use horizontal
meters or plain values; do not use circular gauges.

Only warn when a measured value crosses a documented operational threshold.

### 7.5 Traffic

Default view shows current download/upload values. One restrained 24-hour line
or area chart is allowed when real samples exist.

- Two lines maximum
- Download: blue
- Upload: neutral violet or graphite
- No gradients that obscure data
- No chart animation on page load
- Accessible textual summary accompanies the chart

If reliable history does not exist, omit the chart rather than display
placeholder data.

### 7.6 Recent change

Show:

- Last configuration change
- Result
- Snapshot protection state
- Relative time and exact time in details
- **View snapshots**

This reinforces the product's rollback promise without turning the dashboard
into an audit log.

## 8. Color system

### 8.1 Light theme

```css
:root {
  --color-canvas: #f5f5f7;
  --color-sidebar: rgba(246, 246, 248, 0.92);
  --color-surface: #ffffff;
  --color-surface-raised: #ffffff;
  --color-surface-muted: #f0f0f2;

  --color-text: #1d1d1f;
  --color-text-secondary: #6e6e73;
  --color-text-tertiary: #86868b;
  --color-separator: rgba(60, 60, 67, 0.18);
  --color-separator-strong: rgba(60, 60, 67, 0.29);

  --color-accent: #007aff;
  --color-accent-hover: #006ee6;
  --color-accent-soft: rgba(0, 122, 255, 0.10);

  --color-success: #248a3d;
  --color-success-soft: #e8f5eb;
  --color-warning: #b25000;
  --color-warning-soft: #fff1db;
  --color-danger: #d70015;
  --color-danger-soft: #ffe9ec;
  --color-info: #007aff;
}
```

### 8.2 Dark theme

```css
@media (prefers-color-scheme: dark) {
  :root {
    --color-canvas: #161617;
    --color-sidebar: rgba(28, 28, 30, 0.92);
    --color-surface: #242426;
    --color-surface-raised: #2c2c2e;
    --color-surface-muted: #323234;

    --color-text: #f5f5f7;
    --color-text-secondary: #aeaeb2;
    --color-text-tertiary: #8e8e93;
    --color-separator: rgba(235, 235, 245, 0.16);
    --color-separator-strong: rgba(235, 235, 245, 0.28);

    --color-accent: #0a84ff;
    --color-accent-hover: #409cff;
    --color-accent-soft: rgba(10, 132, 255, 0.18);

    --color-success: #30d158;
    --color-success-soft: rgba(48, 209, 88, 0.14);
    --color-warning: #ff9f0a;
    --color-warning-soft: rgba(255, 159, 10, 0.14);
    --color-danger: #ff453a;
    --color-danger-soft: rgba(255, 69, 58, 0.14);
    --color-info: #64d2ff;
  }
}
```

Color values are starting tokens, not a substitute for contrast testing. Text,
icons, controls, focus rings, and charts must meet the accessibility
requirements in section 15.

## 9. Typography

Use the platform system stack:

```css
font-family:
  -apple-system,
  BlinkMacSystemFont,
  "Segoe UI",
  Inter,
  Helvetica,
  Arial,
  sans-serif;
```

Do not bundle Apple's SF fonts. Apple devices may select them through the
system stack; other platforms receive a high-quality local or open fallback.

Type scale:

| Role | Size / line height | Weight | Use |
|---|---:|---:|---|
| Display | `40/44` | 650 | First-run success and exceptional hero moments |
| Page title | `32/38` | 650 | One per page |
| Section title | `20/26` | 600 | Card groups and page sections |
| Card title | `15/20` | 600 | Card labels |
| Body | `15/22` | 400 | Standard content |
| Supporting | `13/18` | 400 | Secondary metadata |
| Caption | `12/16` | 500 | Compact labels, never critical instructions |
| Data large | `32/36` | 600 | Important current values |

Rules:

- Use sentence case.
- Use tabular numerals for changing network and system values.
- Align units to values consistently.
- Monospace is reserved for IP addresses, CIDRs, ports, key fingerprints, and
  diagnostic identifiers.
- Avoid all-caps labels except very short, nonessential status tags.
- Keep body text lines below roughly 70 characters.

## 10. Spacing and geometry

Base unit: `4px`.

Spacing tokens:

```text
4, 8, 12, 16, 20, 24, 32, 40, 48, 64
```

Standard use:

- `8px`: icon-to-label and compact inline gaps
- `12px`: control groups
- `16px`: grid gutter and compact card padding
- `24px`: standard card padding
- `32px`: page section separation
- `48px`: major page rhythm

Radii:

- Small controls and tags: `8px`
- Inputs and buttons: `10px`
- Standard cards: `16px`
- Hero cards and modals: `20px`
- Pills: full radius, used sparingly

Shadows:

```css
--shadow-card: 0 1px 2px rgba(0, 0, 0, 0.04);
--shadow-raised:
  0 8px 24px rgba(0, 0, 0, 0.08),
  0 1px 2px rgba(0, 0, 0, 0.04);
```

Most cards use a separator and the card shadow. Raised shadow is reserved for
menus, popovers, and modals.

## 11. Iconography

- Use one open-source line-icon family, initially Lucide.
- Default icon size: `18px`; sidebar: `19px`; empty states: up to `32px`.
- Stroke width: `1.75–2px`.
- Icons support labels; they do not replace labels for primary navigation or
  ambiguous actions.
- Do not use emoji as product icons.
- Do not copy or redistribute SF Symbols.

Recommended semantic icons:

- Dashboard: layout dashboard
- Internet: globe
- LAN: network
- Static leases/devices: monitor or laptop
- Firewall: shield
- WireGuard: route or link
- Cloudflare: cloud
- Backup: archive
- Updates: circle arrow
- System: settings

## 12. Components

### 12.1 Cards

- One purpose per card.
- Title at the top left; optional quiet action at the top right.
- Standard padding `24px`.
- Avoid nested cards. Use sections and separators inside a card instead.
- A whole card is clickable only when its role is obvious and keyboard
  accessible.

### 12.2 Buttons

Variants:

- Primary: filled blue, one per page region
- Secondary: neutral filled or bordered
- Quiet: text/icon action
- Destructive: red, used only near a destructive decision

Buttons use direct verbs: **Save changes**, **Add lease**, **Restore snapshot**.
Avoid generic labels such as **Submit**, **OK**, or **Yes**.

Loading buttons preserve their width, show a compact progress indicator, and
remain labeled with the operation.

### 12.3 Inputs

- Labels are always visible above inputs.
- Supporting text explains formats before an error occurs.
- Validation appears next to the affected field and in a page summary when
  submission fails.
- Password and token fields never repopulate stored secret values.
- IP address, CIDR, port, and MAC inputs may use contextual formatting but
  remain ordinary keyboard-accessible text fields.

### 12.4 Toggles

Toggles apply immediate, reversible state only. A group of related network
changes that requires validation uses a form with **Save changes**, not multiple
instant toggles.

The label states the feature, not the current value. Status text may clarify:

```text
Cloudflare Tunnel          [ on ]
Running · checked 1 minute ago
```

### 12.5 Tables and lists

Use tables for comparable structured data such as leases, devices, firewall
rules, snapshots, and peers.

- Left-align text.
- Right-align numeric traffic values.
- Keep primary identity in the first column.
- Put row actions in a final menu.
- Use sticky headers for long tables.
- Provide a meaningful empty state.
- Do not show columns that are mostly empty.

### 12.6 Status

Status combines a dot or icon, text, and when useful a short explanation:

```text
● Connected
PPPoE session active
```

Never use only a colored dot. Avoid “healthy,” “good,” or percentages when a
more precise state is available.

### 12.7 Alerts

- Information: neutral or blue
- Success: brief, nonblocking confirmation
- Warning: visible but non-alarming
- Error: describes what failed and what the system did

An apply failure should say:

> Changes could not be verified. The previous working configuration was
> restored.

It should not expose raw service output on the main screen. Diagnostic details
are available separately and redacted.

### 12.8 Modals and confirmation sheets

Use a modal only when the user must decide before continuing. Routine editing
belongs on a page or side sheet.

Destructive confirmation includes:

- Exact target
- Consequence
- Recovery availability
- Destructive button with a specific verb

Typed confirmations are reserved for factory reset or similarly irreversible
operations.

## 13. Safe-change interaction

Saving a network change opens a compact transaction panel:

```text
Applying changes

✓ Validated
✓ Snapshot created
● Updating firewall and network
○ Verifying connectivity
○ Committing

Keep this page open. The router will restore the previous configuration
automatically if verification fails.
```

Requirements:

- Progress comes from actual backend transaction states.
- Do not use fake time-based progress bars.
- Safe pages may close the panel after success.
- Lockout-prone changes show a persistent confirmation countdown.
- The countdown displays the exact rollback time and a **Keep changes** action.
- Loss of the browser connection does not cancel rollback protection.
- Rollback completion explains that the previous state is active again.

## 14. First-run wizard

The wizard is the most important product flow.

### Structure

1. Welcome
2. Select/confirm WAN
3. Enter PPPoE credentials
4. Create administrator password
5. Review
6. Install and verify
7. Success

Use a single centered panel, maximum width `640px`. Show a quiet step count such
as **Step 2 of 5**, not a large decorative progress illustration.

Principles:

- One question per step where practical.
- Preselect the safe inferred answer.
- Explain WAN and LAN in plain language.
- Password rules appear before submission.
- The review page summarizes interfaces, LAN address, and DHCP.
- The install step shows real state.
- The success page emphasizes `https://192.168.1.1` and offers one primary
  action: **Open router dashboard**.

## 15. Accessibility

Target WCAG 2.2 AA.

- Text and interactive controls meet required contrast ratios.
- Keyboard navigation covers every action.
- Visible focus uses a `2px` accent ring with `2px` offset.
- Touch targets are at least `44 × 44px`.
- Status is never communicated through color alone.
- Form errors are associated with their controls and announced.
- Dynamic apply/rollback state uses appropriate live regions without excessive
  announcements.
- Charts have textual summaries.
- Tables have proper headers and captions.
- Page titles and landmarks are correct.
- Motion respects `prefers-reduced-motion`.
- Dark mode is tested independently, not produced by simple color inversion.
- Zoom to 200% does not lose actions or meaning.

## 16. Motion

Timing:

- Hover/focus feedback: `120–160ms`
- Small state transitions: `160–220ms`
- Modal/sheet entrance: up to `240ms`

Use a standard ease-out curve:

```css
--ease-out: cubic-bezier(0.22, 1, 0.36, 1);
```

Do not animate:

- Continuously changing traffic numbers
- Charts on every refresh
- Healthy status dots
- Decorative background elements

A connecting or applying state may use one restrained spinner or progress
indicator. Reduce or remove movement when requested by the operating system.

## 17. Content design

Tone is calm, direct, and useful.

Prefer:

- **Internet connection lost**
- **Checking PPPoE credentials**
- **Previous settings restored**
- **This port forward is available from the internet**

Avoid:

- **Critical error!**
- **Something went wrong**
- **Operation executed successfully**
- Unexplained acronyms
- Friendly language that minimizes a real security risk

Technical details remain available, but plain language comes first:

```text
Internet connection could not be established.
PPPoE authentication was rejected by the provider.

Details: pppd exit code 19
```

## 18. Security-sensitive UX

- Never display saved passwords, tokens, or private keys.
- Secret fields show **Configured** and allow replacement.
- A newly generated secret is shown only when the workflow requires it, with a
  clear one-time warning.
- Backup export, password change, factory reset, and sensitive recovery require
  re-authentication.
- WAN exposure is labeled in plain language and visually distinct from LAN-only
  access.
- Port forwarding warns that a service will become reachable from the internet.
- Firewall rule ordering and effect are previewed before apply.
- Audit diffs redact secrets.
- Copy buttons provide accessible confirmation without exposing values in logs.

## 19. Implementation rules

- Implement tokens as CSS custom properties.
- Build accessible Svelte primitives before assembling pages.
- Keep page components independent from raw API response shapes.
- Use semantic HTML before adding ARIA.
- Avoid a large visual component framework unless an ADR demonstrates a clear
  benefit.
- No external fonts, icon CDNs, analytics scripts, or runtime design
  dependencies are loaded by the appliance UI.
- Compile all required UI assets into the appliance.
- Respect Content Security Policy without `unsafe-inline`.
- Preserve useful content when JavaScript is slow; loading skeletons must match
  real layout and appear only when needed.
- Use actual backend status; never invent optimistic “healthy” values.

Initial reusable primitives:

- App shell and responsive navigation
- Page header
- Card and section
- Button and icon button
- Text, password, IP/CIDR, select, and checkbox inputs
- Toggle
- Status label
- Alert banner
- Data table and empty state
- Modal and confirmation sheet
- Transaction progress panel
- Confirmation countdown
- Inline traffic value and accessible mini-chart

## 20. Page templates

### Dashboard

Summary-first responsive grid defined in section 7.

### Settings page

Page title and explanation, followed by one or more white grouped sections.
Primary save action appears once after the editable region.

### Collection page

Title, count, primary add action, filter/search when justified, then table or
list. Empty states explain both purpose and next action.

### Detail page

Identity and status first, configuration second, diagnostics collapsed,
destructive actions in a separated final section.

### Dangerous operation

Dedicated narrow panel with consequence, recovery information,
re-authentication, and a specific destructive action.

## 21. Design QA checklist

Before a UI change is complete:

- The primary question and action are obvious.
- The page contains no metric without a user decision attached to it.
- Terminology matches `PROJECT.md` and the API.
- Light and dark themes are verified.
- Desktop, tablet, `360px` mobile, and 200% zoom are verified.
- Keyboard, focus order, labels, errors, and announcements are verified.
- Loading, empty, partial, offline, applying, rollback, and error states exist.
- No secret appears in UI snapshots, browser history, telemetry, or logs.
- Lockout-prone changes expose commit-confirmed protection.
- Visual tokens are used instead of one-off values.
- Reduced motion is respected.

## 22. Definition of success

The design succeeds when a person without networking expertise can install the
router, understand its state, make a common change, and recover from a mistake
without reading external documentation.

It should feel premium because it is clear and dependable—not because it is
visually elaborate.
