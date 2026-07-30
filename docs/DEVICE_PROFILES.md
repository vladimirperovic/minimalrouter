# DNS Filter device profiles and parental control

Minimal Router OS calls this feature **DNS Filter**. The historical JSON key
`adguard` is retained only for backup and API compatibility; the project does
not embed or impersonate AdGuard Home.

## What a device profile does

A profile binds one or more static LAN IPv4 addresses to selected managed
services and a weekly access schedule. The router resolver places IPv4
addresses returned for those services into bounded nftables sets. The firewall
then allows or blocks matching traffic for the profile addresses.

Profiles are applied before established-connection acceptance, so an existing
stream is cut when its permitted window closes instead of remaining open until
the application disconnects.

## Default Kids example

The dashboard starts a new `Kids` profile with:

- YouTube, Steam, and Wikipedia/Wikimedia selected;
- access from `19:00` through `23:59` Monday through Friday;
- selected services available all day Saturday and Sunday.

These values are editable before saving. Other services can be selected
individually.

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
overlapping windows, invalid time ranges, unknown services, and addresses
outside the LAN subnet fail closed.

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
          "weekday_windows": [
            { "start": "19:00", "end": "23:59" }
          ],
          "weekend_mode": "all_day",
          "weekend_windows": []
        }
      }
    ]
  }
}
```

The compatibility key may be renamed only through a versioned API migration;
new user-facing text must use **DNS Filter**.
