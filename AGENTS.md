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

## Secrets and generated state

- Never commit live credentials, private keys, preshared keys, TOTP secrets, PPPoE credentials, DDNS credentials, session material, private host inventory, packet captures, VM disks, runtime databases, or generated appliance state.
- Local private deployment material belongs under ignored `/private/` paths. Real credentials must stay untracked.
- Do not commit generated binaries or archives such as `routerd`, `routerd.gz`, `dist.tar.gz`, firmware staging directories, or runtime state.

## Change discipline

- Preserve the privileged boundary: `routerd` is the unprivileged management plane and `router-applyd` is the narrowly allowlisted privileged helper. Do not bypass that separation for convenience.
- Networking changes must remain fail-closed and recoverable. Do not weaken rollback, confirmation, signed-update, trusted-network, or default-deny behavior to make a test pass.
- Firmware/update changes must keep signature verification, architecture validation, anti-downgrade checks, runtime-layout compatibility, and rollback semantics intact.
- Add or update regression coverage for behavior changes. Do not claim real-lab validation from code or CI alone.

## Before merging

- Review the full diff for secrets, private topology, generated artifacts, and accidental Home-only edits.
- Run the relevant Go and dashboard checks when available.
- In `minimalrouterhome`, verify shared-core parity with the intended public commit before merging.
- Keep documentation claims aligned with the evidence actually collected.
