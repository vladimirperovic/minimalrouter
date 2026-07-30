# Privacy

Minimal Router OS is designed as a locally administered router appliance. The
current project does not intentionally include project-operated analytics,
advertising, usage tracking, or cloud telemetry.

## Data stored locally

Depending on enabled features, the appliance may store:

- router configuration and encrypted or hashed credentials;
- administrator sessions and authentication metadata;
- DHCP lease information, static reservations, interface inventory, and device-policy assignments;
- WireGuard peer metadata and private key material;
- audit events and bounded operational logs;
- local timezone, IoT subnet, and weekday/weekend access windows;
- configuration snapshots and encrypted backup exports;
- optional integration settings for services such as Cloudflare Dynamic DNS.

This information can identify a household, office, network, or device inventory
and must be treated as sensitive.

## Network traffic

Packet forwarding remains in the Linux networking stack. Minimal Router OS does
not intentionally send browsing history or packet contents to the project
maintainer.

Traffic may leave the appliance when required for normal routing, DNS resolution,
software-package access during installation, or an optional integration that the
administrator explicitly enables. Each external provider has its own privacy and
retention practices.

Service-only device schedules use DNS answers to populate volatile nftables
destination sets. The current implementation does not intentionally persist a
per-device browsing history or packet contents, but DHCP reservations, device
names, MAC addresses, assigned profiles, and audit metadata remain sensitive
local network inventory.

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