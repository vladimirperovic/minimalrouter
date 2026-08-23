# Restoring your own settings onto a fresh install

A fresh Minimal Router install is deliberately empty. PPPoE, WireGuard, DDNS,
the proxy, QoS, Wi-Fi and per-device accounting are all switched off, there are
no peers, port forwards, firewall rules or DHCP reservations, and no secret is
stored anywhere. That is what makes a new appliance safe to boot: nothing is
half-configured, and nothing is enabled that would fail without a credential
you have not supplied yet.

The cost of that is retyping your own setup after a reinstall or a lab rebuild.
[`scripts/apply-config.sh`](../scripts/apply-config.sh) exists so you do not
have to.

## Where your settings live

**Not in this repository.** A settings file holds PPPoE credentials, WireGuard
private keys, a DDNS token and the proxy password. Keep it in a private
location — a separate private repository, an encrypted volume, a password
manager attachment. `PRIVACY.md` and the release process both treat private
deployment material as something that stays outside the public tree.

If you keep it in a private git repository, make sure the file is not
world-readable on disk and that the repository is actually private. A leaked
WireGuard private key means anyone holding it can join your tunnel.

## Writing the settings file

The file is JSON and only needs the sections you want to set. It is merged
recursively over whatever the appliance currently has, so anything you leave
out keeps the appliance's value.

```json
{
  "wan": {
    "interface": "eth0",
    "enabled": true,
    "username": "your-isp-login",
    "password": "your-isp-password",
    "mtu": 1492
  },
  "lan": {
    "interface": "eth1",
    "ip_address": "192.168.1.1",
    "netmask": "255.255.255.0",
    "cidr": "192.168.1.1/24"
  },
  "dhcp": {
    "enabled": true,
    "dns_enabled": true,
    "range_start": "192.168.1.100",
    "range_end": "192.168.1.200",
    "lease_time": "12h",
    "dns_servers": ["1.1.1.1", "9.9.9.9"],
    "static_leases": [
      { "id": "nas", "hostname": "home-nas", "mac": "02:b7:50:2e:81:54", "ip_address": "192.168.1.30" }
    ]
  },
  "dns": {
    "records": [{ "name": "nas.lan", "ip": "192.168.1.30" }]
  },
  "wireguard": {
    "enabled": true,
    "interface": "wg0",
    "listen_port": 51820,
    "address": "10.8.0.1/24",
    "private_key": "your-server-private-key",
    "peers": [
      {
        "id": "phone",
        "name": "Phone",
        "public_key": "peer-public-key",
        "allowed_ips": ["10.8.0.2/32"],
        "enabled": true
      }
    ]
  }
}
```

Sections you do not want stay absent. To leave WireGuard off entirely, simply
do not mention it.

## Applying it

```sh
MINIMALROUTER_PASSWORD='your-dashboard-password' \
  scripts/apply-config.sh \
    --host https://192.168.1.1:8443 \
    --config ~/minimalrouterhome/my-router.json \
    --insecure
```

`--insecure` is for the appliance's self-signed certificate on first contact;
drop it once you trust the certificate. If two-factor authentication is on, set
`MINIMALROUTER_TOTP` as well.

Check what would be written before writing it:

```sh
scripts/apply-config.sh --config my-router.json --dry-run
```

The script signs in, reads the live configuration, merges your file over it and
applies the result. When the change touches WAN or LAN the appliance applies it
provisionally and waits for the operator to confirm they can still reach it;
the script has just proved that it can, so it confirms automatically. Without
that confirmation the appliance would roll the change back.

`curl` and `jq` must be available on the machine running the script.

## What it does not do

It does not set the dashboard password, the recovery root password or SSH host
keys. Those are firstboot's job and are deliberately not scriptable from a file
that sits in a repository.

It also does not replace a backup. `docs/RECOVERY.md` covers the encrypted
backup and restore path, which captures the appliance's full canonical state
including material this file has no business holding.
