# Support

Minimal Router OS is an early alpha community project. There is currently no
commercial support, guaranteed response time, production SLA, or warranty.

## Before asking for help

1. Read `README.md`, `docs/DEVELOPMENT.md`, `docs/TESTING.md`, and the relevant
   installation guide.
2. Search existing issues.
3. Confirm that the problem occurs on the current `main` branch or identify the
   exact release/commit being used.
4. Remove all credentials, tokens, keys, public IP addresses, hostnames, device
   names, MAC addresses, and private network inventory from logs and screenshots.

## Bug reports

Use the bug report template and include:

- exact commit or release;
- architecture and hardware/VM details;
- Alpine version;
- expected and actual behavior;
- minimal reproduction steps;
- relevant redacted logs;
- whether console access and rollback are available.

Do not upload a real configuration database, pfSense XML export, backup archive,
WireGuard profile, or `/var/lib/minimalrouter` directory.

## Questions and feature ideas

Questions and focused feature proposals may be opened through GitHub issues.
Please remember that the project intentionally has a narrow scope. A feature may
be declined even when it is useful if it significantly increases attack surface,
runtime complexity, or maintenance cost.

## Security vulnerabilities

Do not report vulnerabilities in a public issue. Use GitHub's private
vulnerability reporting feature when available. When it is not available, create
a public issue containing only a request for private contact and no technical
security details.

## Production incidents

This project is not currently supported as an unattended production firewall.
For a network outage or security incident, restore the known-good router or
firewall first. Do not wait for a community response while the network remains
exposed or unavailable.