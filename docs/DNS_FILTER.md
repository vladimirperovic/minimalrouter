# Network DNS protection

The DNS Filter page has two independent controls:

- **Network protection** uses maintained HaGeZi lists for threats (malware,
  phishing and scams), ads/trackers, adult content and gambling. Categories are
  initially off, including after an upgrade. Select categories and press
  **Apply protection**. Changing MAC/IP addresses does not affect this policy.
- **Advanced DNS & bundled protection** contains the existing upstream DNS
  editor and the legacy bundled-list/device-schedule switch. Existing settings
  are preserved. Device schedules remain optional and require reserved IPv4
  addresses; changing a profile never silently enables that global switch.

DNS Activity separately records queries and generates risk alerts. Monitoring
does not activate blocking. A notification exception does not unblock a domain.

## Lists and exceptions

The fixed sources are [HaGeZi](https://github.com/hagezi/dns-blocklists): TIF Mini,
Multi Light, NSFW and Gambling Mini, in domain-only wildcard format. Their data
is GPL-3.0 licensed by the provider and is downloaded at runtime, not bundled in
firmware. Compact lists do not cover every malicious or inappropriate site.

Enabled lists refresh daily, with up to an hour of scheduling delay and a
six-hour retry backoff. Manual refresh is limited to once every five minutes.
Downloads run as the unprivileged routerd process. The limits are 24 MiB and
300,000 accepted domains per source, 800,000 source entries combined, two disk
indexes per source (64 MiB each), and 512 KiB SQLite page caches. Sources are
fixed HTTPS URLs; the API does not accept arbitrary URLs or configuration text.
Empty, malformed, oversized or drastically shrunken replacements are rejected.
The currently installed policy and its matching indexes survive failures.

**Maintained lists & coverage** shows source links, accepted counts, successful
download times, stale lists, next refresh and update errors. Counts are source
entries, may overlap, and are not blocked-query counts. A conservative available
memory preflight can refuse a large selection. Real appliance memory and query
latency should be measured before enabling all categories on a small VM.

Blocking exceptions cover the named domain **and all subdomains**. They have an
optional reason and remain until removed. Exact-only or expiring blocking
exceptions are not offered. There is a 200-exception bound. Use the narrowest
domain that fixes an application. Changes take effect only after Apply.
These exceptions also override matching rules in the bundled ad/tracker list;
they do not bypass a device's service schedule.

**Check a domain** matches the installed catalog locally and identifies the
matching parent/list. It does not perform an Internet lookup. The result is a
policy decision, not evidence that a person visited a site or that an observed
query was blocked. A missing installed index is reported as unknown.

## Resolver and transaction behavior

The privileged helper streams and independently validates domain-only input,
writes one fixed root-owned drop-in, tests dnsmasq and restarts it. A local TXT
probe verifies that the running resolver loaded the exact policy revision.
The UI separates that evidence from configured category choices. A durable
activation marker and previous file support rollback after failure or helper
interruption. Pending network confirmation/recovery blocks DNS mutations too.

Blocked names and descendants return NXDOMAIN for all record types. More
specific exceptions forward through the configured upstreams. Local DNS records,
hosts entries and DHCP names take precedence, as documented by
[dnsmasq](https://thekelleys.org.uk/dnsmasq/docs/dnsmasq-man.html).
This is a queried-domain filter, not inspection of page content or arbitrary
CNAME chains. Category blocking does not create per-child identity.

The filter only sees lookups that reach the router. A device configured with
another resolver — even plain DNS such as `8.8.8.8` — skips every category,
exception and schedule; a live test resolved a blocked gambling domain that way.
The DNS Filter page therefore leads with **Force all devices through router
DNS**, which switches on the Firewall presets *Block direct external DNS* (UDP
and TCP 53) and *Block DNS-over-TLS* (TCP 853) for the whole LAN. Router DNS keeps
working. DNS-over-HTTPS, VPNs, direct IP connections and mobile data remain
outside that control.

Scheduled service sets have a 16,384-address bound and four-hour maximum
lifetime. Unchanged sets retain validated, unexpired IPv4 destinations through
atomic firewall table replacement; removed service sets are not restored.
Remaining time is preserved rather than reset. Set exhaustion can limit learned
coverage; per-set occupancy telemetry and real-device/DST testing remain future
work. Do not describe schedules as a high-assurance access-control boundary.

## Backup and recovery

Portable v2 `.mrbak` backups include the network policy and blocking exceptions,
but no downloaded lists or query history. Restore router configuration first,
confirm any pending connectivity change, then use **Restore DNS protection**.
The second phase downloads needed lists and verifies activation; check DNS Filter
for completion. Local SQLite configuration snapshots do not contain this separate
policy. Legacy v1 backups leave network category choices unchanged.

The policy/drop-in uses its own narrow helper socket so the published
bootstrap tools and installed integration files remain compatible with dashboard
updates. A rollback to older firmware preserves the native dnsmasq drop-in, but
older dashboards cannot edit the new category policy.

## Device profiles and parental control

Minimal Router OS calls this feature **DNS Filter**. The historical JSON key
`adguard` is retained only for backup and API compatibility; the project does
not embed or impersonate AdGuard Home.

For maintained category blocking and exceptions, see Network DNS protection.

### Built-in network-wide blocking

Enabling DNS Filter also activates the firmware's curated ad/tracker list
through dnsmasq, even without device profiles. The current list contains 16
domains, including advertising and analytics hosts; it is not a comprehensive
malware or adult-content database. It is supplied with firmware and does not
refresh automatically. Although a StevenBlack hosts downloader exists in the
source, the hardened appliance does not call it and rejects custom blocklist
URLs.

The separate DNS Activity risk monitor downloads maintained
adult, phishing, malware, fraud and gambling lists for local matching and
dashboard warnings. Those warnings do not automatically block the domain.

### What a device profile does

A profile binds one or more static LAN IPv4 addresses to selected managed
services and an access schedule. The router resolver places IPv4 addresses
returned for those services into bounded nftables sets. The firewall then
allows or blocks matching traffic for the profile addresses.

Profiles are applied before established-connection acceptance, so an existing
stream is cut when its permitted window closes instead of remaining open until
the application disconnects.

### Kids profile and visual scheduler

In the dashboard, select **Add device profile** to open the optional schedule editor.

The editor contains:

- a seven-day grid, Monday through Sunday;
- 24 hourly cells for every day;
- click-and-drag painting of allowed or blocked hours;
- per-day **All** and **None** controls;
- global **Default**, **Allow all**, and **Block all** controls;
- an exact-time editor for hours and minutes; unchanged schedules retain minute precision;
- individual service selection for YouTube, Steam, Wikipedia/Wikimedia, TikTok,
  Instagram, Facebook/Messenger, Roblox, Epic Games, and Twitch.

The initial preset selects YouTube, Steam, and Wikipedia/Wikimedia, allows them
from `19:00` until the end of Monday through Friday, and allows them all day on
Saturday and Sunday. This is only a starting preset: every hour and every day
can be changed before saving.

The scheduler stores independent `day_windows` for each day. Older backups that
use `weekday_windows`, `weekend_mode`, and `weekend_windows` remain readable,
but new dashboard profiles use the per-day format.

### Requirements

1. Give each managed device a static DHCP lease or otherwise reserve a stable
   LAN address.
2. Add that IPv4 address to exactly one enabled profile.
3. Keep the router clock and timezone correct. Schedules use local router time.
4. Managed devices must use the router DNS service. The firewall blocks direct
   external DNS and DNS-over-TLS for profile addresses.

### Important limitations

Domain-to-service mapping is useful household policy, not perfect application
identification. Large platforms share CDNs, change domains, use encrypted DNS,
or connect directly to previously learned IP addresses. VPNs, proxies, Tor,
mobile tethering, private relay services, and future encrypted-name mechanisms
can bypass DNS-derived classification unless they are separately controlled.

For that reason:

- do not describe this feature as a legal, school-exam, or high-assurance access
  control boundary;
- review the generated rules and test every service on the actual devices;
- combine it with device operating-system controls when stronger enforcement is
  needed;
- avoid adding broad shared CDN domains that could block unrelated sites.

### Failure behavior

Invalid profiles are rejected before generation. Duplicate device addresses,
overlapping windows, invalid local times, unknown services, and addresses
outside the LAN subnet fail closed. A 24-hour hourly row may contain up to 12
non-overlapping windows, which covers the most fragmented possible hourly
selection.

Configuration changes use the normal snapshot, preflight, apply, verify, and
rollback transaction. If DNS or nftables preflight fails, the previous known-good
configuration remains active.

### Example API fragment

```json
{
  "adguard": {
    "enabled": true,
    "filter_devices": [],
    "device_profiles": [
      {
        "id": "kids-main",
        "name": "Kids",
        "ip_addresses": ["192.168.1.50"],
        "services": ["youtube", "steam", "wiki"],
        "enabled": true,
        "schedule": {
          "day_windows": {
            "monday": [{ "start": "19:00", "end": "23:59" }],
            "tuesday": [{ "start": "19:00", "end": "23:59" }],
            "wednesday": [{ "start": "19:00", "end": "23:59" }],
            "thursday": [{ "start": "19:00", "end": "23:59" }],
            "friday": [{ "start": "19:00", "end": "23:59" }],
            "saturday": [{ "start": "00:00", "end": "23:59" }],
            "sunday": [{ "start": "00:00", "end": "23:59" }]
          }
        }
      }
    ]
  }
}
```

The compatibility key may be renamed only through a versioned API migration;
new user-facing text must use **DNS Filter**.

## DNS risk alerts

Open **Operate → DNS Activity** and enable **Record DNS activity**. This also
starts category-list downloads. Wait for all five lists and a successful
collection round; lookups seen while the lists are still downloading are not
checked, and missing or stale data appears as **Incomplete coverage**, not as a
safe result.

### Existing DNS blocking

**DNS Filter** already blocks the firmware's small built-in ad/tracker domain
list when enabled, and supports scheduled service rules for device profiles.
It uses dnsmasq and nftables, not an embedded AdGuard Home server. Its external
blocklist downloader is not enabled in the hardened appliance.

**DNS Activity** adds independent monitoring using the five updated risk lists
below. These lists produce alerts; they do not automatically become DNS Filter
blocking rules. A notification exception does not allow blocked traffic.

### What an alert means

The router observed a DNS lookup matching an adult, phishing, malware, fraud or
gambling domain. Malware/phishing/fraud are marked high risk; adult/gambling are
content warnings. “Hacking” as a topic is not a threat classification: legitimate
security research sites should not be classified as malware merely by topic.

Requests can come from a background app, advert, preview or prefetch. An alert
does not establish a visit, successful connection, blocked response, infection
or what content was viewed. URL paths, searches, videos and page contents are
not available from this DNS data. VPN, external encrypted DNS and mobile data
can bypass the router's resolver.

Alerts cover the whole network, so new devices and changing MAC/IP addresses
do not break matching. The last observed IP is a troubleshooting clue; it is
not a stable device or child identity. The older device/site statistics still
use current DHCP labels, which may differ from the device that held an IP
when the request occurred.

### Reviewing alerts

- **New alerts** excludes reviewed records and saved notification exceptions.
- **Mark reviewed** acknowledges one domain/category/UTC day. More requests on
  that day update its count; a later day can create another alert.
- **All alerts** includes reviewed and ignored matches, with pagination.
- **Ignore domain/category** suppresses notifications for that exact matched
  list domain and category. Remove it in **Category lists & exceptions** to
  restore notifications for unreviewed retained matches. It is not a traffic
  allowlist and it does not disable DNS Filter rules.
- **Delete history** clears DNS statistics, recent lookups, risk alerts and
  queued risk work. Turning recording off also deletes history. Public list
  caches and explicitly saved exceptions remain; exceptions can be removed
  while recording is off.

Notifications are in the dashboard, refreshed every minute while visible.
There is no email, push or webhook delivery. The alert list is
independent of the device/period/site filters used by ordinary DNS statistics.
It covers the configured retained history, with its own category filter.

### Sources and operation

The five [Block List Project](https://github.com/blocklistproject/Lists) lists
use the Unlicense. Only the fixed HTTPS `alt-version/*-nl.txt` feeds are fetched:
`porn`, `phishing`, `malware`, `fraud`, `gambling`. Public downloads send no
observed DNS names or client addresses to the provider. No external reputation
lookup is performed per request.

Full query names are matched locally, including parent domains down to the
registrable domain. The Public Suffix List includes private suffixes, preventing
one malicious tenant from flagging every other tenant on a shared platform.
Matching retains the most specific listed parent per category. Invalid domain
entries and public suffixes themselves are excluded.

Each category has two bounded SQLite index slots. The inactive slot is streamed,
validated and synced before activation; failures retain the old slot. Downloads
are limited to 128 MiB, four million indexed domains and 256 MiB per slot.
Empty, HTML, heavily malformed or catastrophically shrunken lists are rejected.
The two slots cap total index storage at 2.5 GiB; typical size is much lower.
The 2026-10-08 source check produced about 110 MiB across five active indexes
(about 220 MiB with both generations retained). Memory caches are small and
fixed; the full catalogs are not loaded into a Go map.

Lists refresh every 24 hours while recording is on, retry after six hours on
failure, and are stale after 48 hours without successful verification. Manual
refresh is limited to once per five minutes. Critical storage pressure stops
new index building and alert writes. Public lists do not change DNS answers,
forwarding or the privileged helper's permissions.

Classification uses a bounded worker queue and does not block DNS accounting.
Missing initial lists, overload and restarts can lose checks; historical
statistics are not rescanned because they have already discarded subdomains.
Dropped lookups since service start/clear and list/collector errors are visible.
An observed chart bucket means at least one successful collection in that
bucket, not proof of uninterrupted coverage for the whole hour or day.

Alerts are limited to 10,000 rows and follow the 1–90 day DNS retention setting
(30 by default). The oldest rows are removed at the bound, with a visible count.
There can be at most 500 notification exceptions. All data stays outside the
canonical configuration backup; see [Privacy](../PRIVACY.md).

### Validation and limits

Offline tests cover matching boundaries, changing IP addresses, filtering,
pagination, acknowledgement, exceptions, restart, retention, queue invalidation,
failed refreshes, ETags, storage pressure and authenticated API access. Browser
tests exercise both designs and light/dark modes on desktop and mobile layouts.
`MINIMALROUTER_LIVE_FEED_TEST=1 go test ./internal/dnsrisk -run TestLiveCategorySources -v`
is an optional public-source compatibility check; ordinary tests stay offline.

These checks do not establish list completeness or real-appliance performance.
Actual DNS capture, resource behavior and coverage require an appliance pilot.
