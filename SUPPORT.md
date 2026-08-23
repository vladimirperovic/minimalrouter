# Support

Minimal Router OS is a **Beta** community project. The current release line is
v0.1.6. There is no commercial support, guaranteed response time, production SLA
or warranty.

v0.1.6 is intended for controlled pilots with console access and a known-good
router available for rollback. It is not supported as an unattended production
firewall.

## Before asking for help

1. For a new Proxmox install, read `docs/ISO_INSTALLATION.md`, `docs/PROXMOX.md`
   and `docs/GOLDEN-IMAGE.md` first.
2. Read `docs/TROUBLESHOOTING.md` for safe diagnostics.
3. Search existing issues and pull requests.
4. Confirm the exact release or commit. Prefer the published v0.1.6 release over
   an arbitrary Actions artifact when reproducing a user install problem.
5. Restore the known-good router first when the test network is exposed or unavailable.
6. Remove credentials, tokens, keys, public IP addresses, hostnames, device names,
   MAC addresses, profiles, QR codes and private network inventory from logs and screenshots.

## Installation reports

For v0.1.6 Golden ISO problems, include:

- exact ISO filename and SHA-256 result;
- BIOS/UEFI mode;
- disk bus/model and size;
- number and type of NICs;
- whether the installer used VGA/noVNC or `ttyS0`;
- the last visible installer/firstboot message;
- redacted serial or noVNC output when available.

Do not work around a failed Golden install by adding live `apk` repositories,
running `setup-disk`, rebuilding initramfs or patching the target filesystem by
hand. Capture evidence and fix the installer instead.

## Bug reports

Use the bug report template and include:

- exact commit or release;
- architecture and generic hardware or VM details;
- expected and actual behavior;
- minimal reproduction steps;
- relevant redacted logs;
- whether console access and rollback were available.

Do not upload a real configuration database, pfSense XML export, backup archive,
WireGuard profile, packet capture or `/var/lib/minimalrouter` directory.

## Hardware validation reports

Use the hardware validation issue template for VM or dedicated-device evidence.
State the exact release/commit, topology, method, duration, units, results and
limitations. Use an isolated test network and synthetic identifiers.

A successful result on one device does not establish general hardware support or
production readiness.

## Questions and feature ideas

Questions and focused feature proposals may be opened through GitHub issues. The
project intentionally has a narrow scope, so a feature may be declined when it
significantly increases attack surface, runtime complexity, privilege, recovery
risk or maintenance cost.

Project decision-making is documented in [GOVERNANCE.md](GOVERNANCE.md).
