# Dynamic DNS — No-IP and Cloudflare

Minimal Router uses Alpine `inadyn` for Dynamic DNS. New configurations default
to **No-IP**, while Cloudflare DDNS remains supported for backward compatibility.

The historical configuration/API object remains named `cloudflare` so existing
backups and clients can be restored without a schema-breaking migration. The
DDNS fields inside that object are now provider-aware.

## Supported providers

| Provider | `ddns_provider` | inadyn provider | Required fields |
|---|---|---|---|
| No-IP | `noip` | `no-ip.com` | username, credential, hostname/update target |
| Cloudflare | `cloudflare` | `cloudflare.com` | zone name, API token, hostname |

An empty provider value is interpreted as `cloudflare` only for compatibility
with configurations written before provider-aware DDNS existed. `DefaultConfig`
uses `noip` for new installs.

## Recommended No-IP setup

Prefer a **No-IP DDNS Key** instead of the main No-IP account password. A DDNS
Key is scoped to a hostname or hostname group and can be revoked independently.

In the dashboard open **Dynamic DNS** and enter:

- Provider: `No-IP`
- Enable Dynamic DNS: enabled
- No-IP username / DDNS Key username: the generated DDNS Key username
- New provider credential: the generated DDNS Key password
- Hostname / update target: `all.ddnskey.com` when using a current No-IP DDNS Key

The WireGuard client endpoint remains the actual No-IP hostname assigned to the
deployment. `all.ddnskey.com` is the updater target associated with a DDNS Key;
it does not replace the hostname used by the WireGuard client.

If intentionally using legacy No-IP account credentials instead of a DDNS Key,
enter the No-IP username/email and password and use the intended No-IP hostname
as the update target. DDNS Keys are preferred because they limit credential
scope.

Never store No-IP credentials in Git, documentation, shell history, evidence
files or chat output.

## Cloudflare compatibility

For Cloudflare select `Cloudflare` and enter:

- Hostname / update target: full DNS record, for example `router.example.com`
- Cloudflare zone: zone **name**, for example `example.com`, not the hexadecimal
  Zone ID
- New provider credential: scoped Cloudflare API token

Legacy Cloudflare configurations whose `ddns_provider` field is absent retain
Cloudflare semantics.

## Apply lifecycle

Dynamic DNS uses the existing transactional network apply pipeline:

1. generate an `inadyn` candidate configuration;
2. run `inadyn --check-config` against the candidate;
3. install `/etc/inadyn/inadyn.conf` with restricted ownership/permissions;
4. run one bounded real update in the foreground;
5. restart the OpenRC `inadyn` service;
6. verify that the service is active;
7. roll back the previous configuration/service state if activation or
   verification fails.

Dynamic DNS requires HTTPS egress but does not open a management port on WAN.

## Safe diagnostics

```sh
inadyn --check-config -f /etc/inadyn/inadyn.conf
rc-service inadyn status
inadyn --once --force --foreground --no-pidfile \
  --config /etc/inadyn/inadyn.conf --loglevel notice
rc-service inadyn restart
```

Redact provider usernames, passwords, API tokens and private hostnames before
sharing any diagnostic output.

Then verify the public DNS result from a separate network/resolver. A production
proof must also verify a later WAN public-IP change without manually running a
DDNS updater on the Proxmox host.

## Target-host status

The 2026-08-01 Proxmox pilot proved that manually provisioned Dynamic DNS on the
Proxmox side allowed a real external phone to establish WireGuard and open the
Minimal Router dashboard. That proves the external hostname/endpoint and
WireGuard path.

The remaining DDNS gate after this implementation is to prove that
**MinimalRouter itself** updates No-IP through `inadyn`, keeps the daemon healthy
and follows a real public-IP change without a host-side workaround.
