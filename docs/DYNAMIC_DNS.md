# Dynamic DNS — No-IP and Cloudflare

Minimal Router uses Alpine `inadyn` for Dynamic DNS. New configurations default
to **No-IP**, while Cloudflare DDNS remains supported for backward compatibility.

The historical configuration/API object is still named `cloudflare` so old
backups and clients can be restored without a schema-breaking migration. The
DDNS fields inside that object are provider-aware; Cloudflare Tunnel fields stay
Cloudflare-specific and the tunnel remains disabled by the hardened appliance
profile.

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
Key is scoped to one hostname or hostname group and can be revoked independently.

In the dashboard open **Dynamic DNS** and enter:

- Provider: `No-IP`
- Enable Dynamic DNS: enabled
- No-IP username / DDNS Key username: the generated DDNS Key username
- New provider credential: the generated DDNS Key password
- Hostname / update target: `all.ddnskey.com` when using a current No-IP DDNS
  Key, as recommended by No-IP

The public hostname used by a WireGuard client remains the actual No-IP hostname
assigned to the deployment. `all.ddnskey.com` is the update target associated
with the DDNS Key; it does not replace the hostname you put in the WireGuard
client endpoint.

If intentionally using legacy No-IP account credentials instead of a DDNS Key,
enter the No-IP username/email and password and use the intended No-IP hostname
as the update target. DDNS Keys are preferred because they limit credential
scope.

Do not store No-IP credentials in documentation, shell history, issue comments,
or test reports.

## Cloudflare setup

For Cloudflare select `Cloudflare` and enter:

- Hostname / update target: full DNS record, for example `router.example.com`
- Cloudflare zone: zone **name**, for example `example.com`, not the hexadecimal
  Zone ID
- New provider credential: scoped Cloudflare API token

Legacy Cloudflare configurations whose `ddns_provider` field is absent continue
to use Cloudflare semantics.

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

The firewall permits the root-run verification and the `inadyn` daemon to make
the HTTPS connection required by DDNS. Dynamic DNS does not open a management
port on WAN.

## Safe diagnostics on the router console

Never paste output that contains a provider username, password, API token, or
private hostname if the deployment treats that hostname as sensitive.

Check the installed configuration syntax:

```sh
inadyn --check-config -f /etc/inadyn/inadyn.conf
```

Check service state:

```sh
rc-service inadyn status
```

Run a foreground one-shot update when intentionally debugging the provider:

```sh
inadyn --once --force --foreground \
  --no-pidfile \
  --config /etc/inadyn/inadyn.conf \
  --loglevel notice
```

Restart the daemon after a successful check:

```sh
rc-service inadyn restart
```

Then verify the public DNS result from a separate network/resolver. For the
production proof, also verify a later WAN public-IP change without manually
running an updater on the Proxmox host.

## Target-host validation status

The 2026-08-01 Proxmox pilot proved that a manually provisioned Dynamic DNS
endpoint on the Proxmox side allowed a real external phone to establish a
WireGuard tunnel and open the Minimal Router dashboard.

That proves the external hostname/endpoint and WireGuard path, but it does not
retroactively prove the old appliance-managed Cloudflare-only implementation.
After this No-IP implementation is built and deployed, the remaining DDNS gate
is to prove that **MinimalRouter itself** updates No-IP through `inadyn`, keeps
the daemon healthy, and survives a real public-IP change without a host-side
workaround.
