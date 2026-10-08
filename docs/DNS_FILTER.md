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
CNAME chains. External encrypted DNS, VPNs, direct IP connections and mobile
data can bypass router DNS. Category blocking does not create per-child identity.

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

The policy/drop-in uses its own narrow helper socket so published v0.1.9
bootstrap tools and installed integration files remain compatible with dashboard
updates. A rollback to older firmware preserves the native dnsmasq drop-in, but
older dashboards cannot edit the new category policy.
