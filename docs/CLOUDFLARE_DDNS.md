# Cloudflare DDNS

Minimal Router currently implements **Cloudflare Dynamic DNS only**. It is not a
generic DynDNS client and it does not currently expose No-IP, DynDNS.com,
redirectme.net, DuckDNS, or arbitrary provider configuration through the
dashboard.

The runtime uses Alpine Linux `inadyn` and the native `cloudflare.com:1`
provider for IPv4 updates.

## What the dashboard fields mean

| Dashboard field | Value |
|---|---|
| Hostname | Full DNS record to update, for example `router.example.com` |
| Zone | **DNS zone name**, for example `example.com` — not a Cloudflare Zone ID |
| API token | A Cloudflare API token allowed to read the zone and edit its DNS records |

The generated `inadyn` configuration maps the zone name to the Cloudflare
provider `username`, the API token to `password`, and the full record name to
`hostname`.

## Required Cloudflare setup

1. The DNS zone must already exist in the Cloudflare account.
2. The hostname/record should belong to that zone.
3. Create a scoped API token with the minimum permissions needed to read the
   selected zone and edit DNS records in that zone.
4. Enter the **zone name** such as `example.com`; do not paste the hexadecimal
   Cloudflare Zone ID into the Zone field.
5. Enable Cloudflare DDNS and apply the configuration.

Prefer a token scoped to the one required zone. Do not widen a token to all
zones merely to make troubleshooting easier.

## What happens during apply

When Cloudflare DDNS is enabled, the privileged apply path:

1. generates `/etc/inadyn/inadyn.conf`;
2. runs `inadyn --check-config` against the candidate configuration;
3. installs the configuration with restricted permissions;
4. performs a bounded foreground `inadyn --once --force` update as a credential
   and network check;
5. restarts the OpenRC `inadyn` service;
6. verifies that the service reports healthy state;
7. rolls back to the previous configuration if activation or verification fails.

The firewall permits the HTTPS egress needed by the root-run verification and
the `inadyn` daemon only when Cloudflare DDNS is enabled.

## Troubleshooting on the router console

Do not paste tokens or full configuration files into public issues or chat logs.
Use local console commands and redact secrets before sharing output.

```sh
rc-service inadyn status
rc-service inadyn restart

# Syntax only
inadyn --check-config -f /etc/inadyn/inadyn.conf

# One diagnostic update; redact the hostname/public IP if sharing output
inadyn --once --force --foreground --no-pidfile \
  --config /etc/inadyn/inadyn.conf --loglevel debug
```

Also confirm:

```sh
ip route
ip -4 addr show ppp0
nft list chain inet minimalrouter output
```

A working Internet connection does not by itself prove DDNS is configured
correctly. Common configuration mistakes are:

- entering a Cloudflare **Zone ID** instead of the zone name;
- entering a hostname that does not belong to the selected zone;
- an API token without the required zone-read or DNS-edit permission;
- expecting a non-Cloudflare DynDNS provider to work through this screen;
- DNS/API reachability being unavailable during the one-shot verification.

## Current validation status

The 2026-08-01 owner-Proxmox pilot established stable Internet forwarding and
completed performance/load and rollback tests, but DDNS was not yet confirmed as
working. Until a successful `inadyn` update and subsequent DNS resolution check
are recorded, Cloudflare DDNS remains an open target-host validation item.

See also:

- [`CURRENT_VALIDATION.md`](CURRENT_VALIDATION.md)
- [`PROXMOX_TEST_REPORT_2026-08-01.md`](PROXMOX_TEST_REPORT_2026-08-01.md)
- [`TROUBLESHOOTING.md`](TROUBLESHOOTING.md)
