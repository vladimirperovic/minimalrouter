# Support

Minimal Router OS is an early-alpha community project. There is currently no
commercial support, guaranteed response time, production SLA, or warranty.

## Before asking for help

1. Read `README.md`, `docs/INSTALLATION.md`, `docs/TROUBLESHOOTING.md`, and the
   relevant development or deployment guide.
2. Search existing issues and pull requests.
3. Confirm that the problem occurs on the current `main` branch or identify the
   exact release or commit being used.
4. Restore the known-good router first when the test network is exposed or
   unavailable.
5. Remove all credentials, tokens, keys, public IP addresses, hostnames, device
   names, MAC addresses, profiles, QR codes, and private network inventory from
   logs and screenshots.

## Bug reports

Use the bug report template and include:

- exact commit or release;
- architecture and generic hardware or VM details;
- Alpine version;
- expected and actual behavior;
- minimal reproduction steps;
- relevant redacted logs;
- whether console access and rollback were available.

Do not upload a real configuration database, pfSense XML export, backup archive,
WireGuard profile, packet capture, or `/var/lib/minimalrouter` directory.

## Hardware validation reports

Use the hardware validation issue template for VM or dedicated-device evidence.
State the exact commit, test topology, method, duration, units, results, and
limitations. Use an isolated test network and synthetic identifiers.

A successful result on one device does not establish general hardware support or
production readiness.

## Questions and feature ideas

Questions and focused feature proposals may be opened through GitHub issues.
Please remember that the project intentionally has a narrow scope. A feature may
be declined even when useful if it significantly increases attack surface,
runtime complexity, privilege, recovery risk, or maintenance cost.

Project decision-making is documented in [GOVERNANCE.md](GOVERNANCE.md).

## Security vulnerabilities

Do not report vulnerabilities in a public issue. Use GitHub's private
vulnerability reporting feature when available. When it is unavailable, create
a public issue containing only a request for private contact and no technical
security details.

See [SECURITY.md](SECURITY.md) for the complete reporting policy.

## Privacy

The project does not require a full backup, database, packet capture, or real
network inventory for support. Read [PRIVACY.md](PRIVACY.md) before sharing any
diagnostic material.

## Production incidents

This project is not currently supported as an unattended production firewall.
For a network outage or security incident, restore the known-good router or
firewall first. Do not wait for a community response while the network remains
exposed or unavailable.