# Agent instructions

These rules apply to every AI agent and automated contributor working in this repository.

## Repository boundary

- `vladimirperovic/minimalrouter` is the source of truth for the shared Minimal Router core.
- The private `vladimirperovic/minimalrouterhome` repository is intentionally structured as **this shared core plus a `home/` overlay**.
- When working in `minimalrouterhome`, files outside `home/` are shared core. Do not make Home-only edits to them.
- Generic fixes, features, tests, documentation, packaging, and dashboard changes land in `minimalrouter` first. Synchronize the resulting shared files into `minimalrouterhome` afterwards.
- Home-specific topology, Proxmox helpers, deployment assumptions, and operator-only material belong under `home/`.

## Shared-core parity

- After a Home sync, every tracked shared path outside `home/` should be byte-for-byte identical to the synchronized public commit whenever possible.
- Do not copy private/Home changes back into the public repository blindly. Promote only changes that are genuinely generic and sanitize them first.
- If an unwanted shared file should disappear, remove it from the public repository first and then synchronize Home.

## Golden ISO invariant

Before changing anything under `packaging/alpine/`, `.github/workflows/iso.yml`,
`scripts/ci/iso-full-install.exp` or the release ISO path, read
[`docs/GOLDEN-IMAGE.md`](docs/GOLDEN-IMAGE.md) completely.

v0.1.6 continues the Golden installation model introduced in v0.1.4:

- CI/build infrastructure builds Alpine, kernel, modules, MinimalRouter and the Dashboard;
- CI produces one bootable Golden disk image;
- the user VM only verifies and raw-copies that image, then reboots;
- router-specific state is collected by the installed appliance on first boot.

Do **not** reintroduce any of the following into `live-installer.sh`:

- `apk add`, `apk update` or dependency resolution;
- `setup-disk`, target partitioning or filesystem construction;
- `mkinitfs` against the live ISO kernel;
- a target chroot;
- rerunning MinimalRouter's distribution installer;
- mounting and patching the flashed target filesystem.

A release Golden ISO must be built from the already signed AMD64 release
distribution and must contain `firmware-signing.pub`. Never replace that with an
unsigned development payload to make release CI pass.

Installer changes are incomplete until the `Appliance ISO` workflow proves a
blank-disk flash, reboot, firstboot, serial login, real SSH login, LAN/firewall,
router service readiness and Dashboard listener from the installed disk.

## Secrets and generated state

- Never commit live credentials, private keys, preshared keys, TOTP secrets, PPPoE credentials, DDNS credentials, session material, private host inventory, packet captures, VM disks, runtime databases, or generated appliance state.
- Local private deployment material belongs under ignored `/private/` paths. Real credentials must stay untracked.
- Do not commit generated binaries or archives such as `routerd`, `routerd.gz`, `dist.tar.gz`, firmware staging directories, ISO images or runtime state.

## Change discipline

- Preserve the privileged boundary: `routerd` is the unprivileged management plane and `router-applyd` is the narrowly allowlisted privileged helper. Do not bypass that separation for convenience.
- Networking changes must remain fail-closed and recoverable. Do not weaken rollback, confirmation, signed-update, trusted-network, default-deny or installer safety behavior to make a test pass.
- Firmware/update changes must keep signature verification, architecture validation, anti-downgrade checks, runtime-layout compatibility, and rollback semantics intact.
- Add or update regression coverage for behavior changes. Do not claim real-lab validation from code or CI alone.

## Before merging

- Review the full diff for secrets, private topology, generated artifacts, and accidental Home-only edits.
- Run the relevant Go, dashboard and packaging checks when available.
- If installer/release behavior changed, require the full `Appliance ISO` E2E check.
- In `minimalrouterhome`, verify shared-core parity with the intended public commit before merging.
- Keep documentation claims aligned with the evidence actually collected.
