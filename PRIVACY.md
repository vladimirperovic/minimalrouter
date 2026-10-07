# Privacy

Minimal Router OS is designed as a locally administered router appliance. The
current project does not intentionally include project-operated analytics,
advertising, usage tracking, or cloud telemetry.

## Data stored locally

Depending on enabled features, the appliance may store:

- router configuration and encrypted or hashed credentials;
- administrator sessions and authentication metadata;
- DHCP lease information;
- WireGuard peer metadata and private key material;
- audit events and bounded operational logs;
- configuration snapshots and encrypted backup exports;
- optional integration settings for services such as Cloudflare Dynamic DNS.

This information can identify a household, office, network, or device inventory
and must be treated as sensitive.

## Local activity history

Optional per-device traffic accounting records IP-address byte totals, sampled
from routed traffic every five minutes. Monthly totals follow the configured
retention; recent hourly totals and per-address daily totals are retained for
32 days. Device names and MAC labels are resolved from current DHCP leases and
reservations, rather than copied into this usage database. Disabling accounting
immediately hides this history from the API and deletes it on the next collector
round. Overview activity means a positive byte delta recorded within ten minutes;
it is not a live presence scan and may omit quiet or local-only devices.

The firewall also retains aggregate allowed/blocked input and forwarded packet
counts in minute buckets for at most 25 hours, independently of optional device
accounting. These aggregates contain no per-device identifiers, browsing
categories, destinations, packet contents or payloads. Collection gaps and counter
resets are excluded. Both histories remain on the appliance and require an
administrator session from a trusted management network to read.

Dashboard design and light/dark preferences are stored in the local browser.
They do not change router configuration or send data to an external service.

## DNS activity

Optional DNS activity statistics are **off by default**. They are browsing
history: while enabled, the router records which sites each LAN or WireGuard
client looks up through the router's DNS resolver.

- The setting is stored in `routerd`'s local accounting database, not in the
  canonical configuration, so it is not part of configuration backups.
- While enabled, `router-applyd` installs the fixed dnsmasq drop-in
  `/etc/dnsmasq.d/minimalrouter-dns-activity.conf`, and dnsmasq writes query
  lines to `/run/minimalrouter-dns-queries.log` on tmpfs. `router-applyd`
  drains and truncates that file every minute and passes only validated client
  addresses, query names and counts to `routerd`. Reverse lookups,
  single-label names and the router's own lookups are ignored.
- `routerd` keeps the most recent lookups (at most 2,000, with full hostnames)
  in memory only; they are lost on restart.
- On disk, in the local accounting database, `routerd` stores per UTC day,
  device address and registrable site a lookup count with first and last time
  seen, plus hourly lookup totals per device. Full hostnames, query types and
  answers are not stored. Writes happen every five minutes in one transaction
  and are skipped under critical disk pressure.
- History is retained for the configured number of days (at most 90, 30 by
  default), bounded to 20,000 device/site rows per day and 1,000,000 rows in
  total. Lookups beyond a bound are counted without a device or site.
- Turning recording off removes the tmpfs log and in-memory data, hides the
  history from the API immediately and deletes it on the next collection round.
  The page's **Delete history** action deletes it at once and is audited.

Read-only MCP clients configured with the administrator password can read
this history (see `docs/MCP.md`). Lookups that bypass the router's resolver
(encrypted DNS in the browser, VPNs, mobile data) are not seen. Recording other people's browsing may be
regulated where you live; inform the people using the network.

## Network traffic

Packet forwarding remains in the Linux networking stack. Minimal Router OS does
not intentionally send browsing history or packet contents to the project
maintainer.

Traffic may leave the appliance when required for normal routing, DNS resolution,
software-package access during installation, or an optional integration that the
administrator explicitly enables. Each external provider has its own privacy and
retention practices.

## Diagnostics and support

The project does not need a complete runtime database, backup, packet capture, or
real network inventory to accept a bug report. Public issues and screenshots must
remove:

- public IP addresses and real hostnames;
- MAC addresses and device names;
- PPPoE, Wi-Fi, proxy, backup, and provider credentials;
- session identifiers and CSRF tokens;
- WireGuard private keys, preshared keys, profiles, and QR codes;
- unredacted logs, backups, databases, snapshots, and packet captures.

See [SUPPORT.md](SUPPORT.md) and [SECURITY.md](SECURITY.md) before sharing
diagnostics.

## Backups

Backup exports can contain credentials and private keys. Use encryption, store
backups outside the source repository, limit access, and delete obsolete copies
securely. Never attach a backup to a public issue.

## Optional integrations

Optional integrations are disabled by default. Enabling an integration may send
configured identifiers or status information to that provider. Administrators
are responsible for reviewing the provider's privacy terms and using scoped,
revocable credentials.

## Changes

Privacy-relevant behavior must be documented in the same pull request as the
code change. A future feature that introduces project-operated telemetry or a
hosted service requires an explicit product decision, security review, clear
opt-in behavior, and an update to this document before release.