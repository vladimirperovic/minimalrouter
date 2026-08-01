# Minimal Router Home private overlay

`minimalrouterhome` uses the public `vladimirperovic/minimalrouter` repository as its shared application engine.

The rule is intentionally simple:

- shared application/runtime code follows public `minimalrouter`;
- home-specific operational material belongs under `private/`;
- real credentials, keys, addresses, VM identifiers, runtime databases, backups, packet captures and generated appliance state must never be committed.

## What stays outside Git

Production state belongs on the appliance or in an encrypted offline backup, not in this repository. Examples include:

- PPPoE username/password;
- administrator password and TOTP state;
- WireGuard private/preshared keys;
- Cloudflare tokens;
- real public/private addresses, MAC addresses and household inventory;
- Proxmox node/VM identifiers and bridge assignments when they identify the live installation;
- `/var/lib/minimalrouter/minimalrouter.db` and its WAL files;
- `/var/lib/minimalrouter-applyd/` recovery metadata;
- generated `/etc/ppp`, WireGuard, dnsmasq, nftables and related runtime configuration;
- exported backups and packet captures.

## Local overlay paths

Use these ignored directories if a local deployment helper needs files during installation or recovery:

```text
private/runtime/
private/secrets/
private/backups/
```

Nothing in those directories should be required to build or test the shared application engine.

## Upstream-sync rule

When public `minimalrouter` changes, sync the shared application code from public `main` and preserve only this `private/` overlay plus explicitly private GitHub/release policy where required.

Do not resolve upstream differences by embedding production values into shared Go, TypeScript, Alpine packaging, API, firewall or test files.

Current public core baseline for this synchronization: `vladimirperovic/minimalrouter@7e832da8e6b3a461924e0e94112746597fd0d8c5`.
