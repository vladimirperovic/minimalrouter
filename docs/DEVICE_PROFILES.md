# DNS Filter device profiles and parental control

Minimal Router OS calls this feature **DNS Filter**. The historical JSON key
`adguard` is retained only for backup and API compatibility; the project does
not embed or impersonate AdGuard Home.

## What a device profile does

A profile binds one or more static LAN IPv4 addresses to selected managed
services and an access schedule. The router resolver places IPv4 addresses
returned for those services into bounded nftables sets. The firewall then
allows or blocks matching traffic for the profile addresses.

Profiles are applied before established-connection acceptance, so an existing
stream is cut when its permitted window closes instead of remaining open until
the application disconnects.

## Kids profile and visual scheduler

In the dashboard, select **Add device profile**, then choose **Kids** as the
profile type. Only then does the parental-control editor open.

The editor contains:

- a seven-day grid, Monday through Sunday;
- 24 hourly cells for every day;
- click-and-drag painting of allowed or blocked hours;
- per-day **All** and **None** controls;
- global **Default**, **Allow all**, and **Block all** controls;
- individual service selection for YouTube, Steam, Wikipedia/Wikimedia, TikTok,
  Instagram, Facebook/Messenger, Roblox, Epic Games, and Twitch.

The initial preset selects YouTube, Steam, and Wikipedia/Wikimedia, allows them
from `19:00` until the end of Monday through Friday, and allows them all day on
Saturday and Sunday. This is only a starting preset: every hour and every day
can be changed before saving.

The scheduler stores independent `day_windows` for each day. Older backups that
use `weekday_windows`, `weekend_mode`, and `weekend_windows` remain readable,
but new dashboard profiles use the per-day format.

## Requirements

1. Give each managed device a static DHCP lease or otherwise reserve a stable
   LAN address.
2. Add that IPv4 address to exactly one enabled profile.
3. Keep the router clock and timezone correct. Schedules use local router time.
4. Managed devices must use the router DNS service. The firewall blocks direct
   external DNS and DNS-over-TLS for profile addresses.

## Important limitations

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

## Failure behavior

Invalid profiles are rejected before generation. Duplicate device addresses,
overlapping windows, invalid local times, unknown services, and addresses
outside the LAN subnet fail closed. A 24-hour hourly row may contain up to 12
non-overlapping windows, which covers the most fragmented possible hourly
selection.

Configuration changes use the normal snapshot, preflight, apply, verify, and
rollback transaction. If DNS or nftables preflight fails, the previous known-good
configuration remains active.

## Example API fragment

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
