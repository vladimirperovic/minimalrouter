# Dashboard layout

Use two adjacent cards for settings that divide naturally into Configuration
and Preferences. Stack them on narrow screens. Retain every supported field,
action, validation step, and recovery flow when reorganizing a page.

On LAN & DHCP, pair WAN with LAN, then DHCP with DNS. Each card retains
its separate save action and validation flow. Keep local DNS records and
device tables on full rows.

On Gateway Quality, pair quality history with public-IP history, diagnostics
with service recovery, and monitoring targets with automatic recovery. Keep
the reading and keyboard order consistent; stack these pairs on narrow screens.

Keep large device lists, usage tables, firewall rules and audit tables full
width. Do not turn dense tables into cards or squeeze them into a settings
column. WireGuard peers are the exception: each device has a card containing
all of its existing actions, in at most two columns.

Noema and Studio are independent of Light and Dark. Shared panels must work
in all four combinations. Theme changes must preserve unsaved form drafts.

Traffic insights use UTC hourly/daily byte aggregates, retained for 32 days;
monthly accounting and its retention control remain available. The three
panels share a single snapshot and period. Distribution means share by device,
not inferred application categories. The peak is an average over a collection
interval, not a line-speed measurement. Samples are taken every five minutes.
Disabling accounting deletes the byte history. There is no historical backfill.

Firewall activity uses aggregate input/forward packet counters collected every
minute, independently of per-device accounting. The chart covers the most
recent 24 hours; active connections is a current conntrack measurement.
Observation chains bracket the existing default-deny chains without changing
their verdicts. The privileged helper exposes only sanitized aggregates from
a fixed read operation. Boot/table generations and collection gaps establish
new baselines. At most 25 hours of minute aggregates remain stored.

Missing samples are gaps, never fabricated zeros. A successful idle sample is
zero. Request failures and stale collections must be distinguishable from
healthy or empty data. Demo data belongs only in the explicit demo build.

Scrollable panels must have bounded height so long histories never stretch
a neighbouring card. Public-IP history supports keyboard scrolling. Sticky
table headers use opaque theme surfaces. Keep status text at 4.5:1 contrast
and preserve warning/off labels. Align the desktop toolbar and pages to one
centred 1480px measure, including on ultrawide displays. Device identities
stay on one desktop line with full names available through their title;
keep common actions adjacent and scroll dense columns within the table.

Overview uses Active devices: addresses with a positive routed-byte delta
recorded within the last ten minutes, sorted by latest collection time. The
collector samples every five minutes; Last seen labels collection time, not
a connectivity probe. Quiet and local-only devices may be absent. Use the
appliance clock, include recent activity across month boundaries, and expire
old observations even when no newer history arrives. Disabled accounting or
a failed request shows an unavailable state, never a zero-connected claim.
View all devices leads to the complete LAN/DHCP Known devices list, with its
leases, history, reservations and actions preserved.
