# DNS risk alerts

Open **Operate → DNS Activity** and enable **Record DNS activity**. This also
starts category-list downloads. Existing recording settings remain enabled on
upgrade to v0.2.0. Wait for all five lists and a successful collection round;
missing or stale data appears as **Incomplete coverage**, not as a safe result.

## Existing DNS blocking

**DNS Filter** already blocks the firmware's small built-in ad/tracker domain
list when enabled, and supports scheduled service rules for device profiles.
It uses dnsmasq and nftables, not an embedded AdGuard Home server. Its external
blocklist downloader is not enabled in the hardened appliance.

**DNS Activity** adds independent monitoring using the five updated risk lists
below. These lists produce alerts; they do not automatically become DNS Filter
blocking rules. A notification exception does not allow blocked traffic.

## What an alert means

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

## Reviewing alerts

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
There is no email, push or webhook delivery in v0.2.0. The alert list is
independent of the device/period/site filters used by ordinary DNS statistics.
It covers the configured retained history, with its own category filter.

## Sources and operation

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

## Validation and limits

Offline tests cover matching boundaries, changing IP addresses, filtering,
pagination, acknowledgement, exceptions, restart, retention, queue invalidation,
failed refreshes, ETags, storage pressure and authenticated API access. Browser
tests exercise both designs and light/dark modes on desktop and mobile layouts.
`MINIMALROUTER_LIVE_FEED_TEST=1 go test ./internal/dnsrisk -run TestLiveCategorySources -v`
is an optional public-source compatibility check; ordinary tests stay offline.

These checks do not establish list completeness or real-appliance performance.
Actual DNS capture, resource behavior and coverage require an appliance pilot.
