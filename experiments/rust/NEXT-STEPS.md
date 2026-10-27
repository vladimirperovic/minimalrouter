# Next steps (2026-10-27)

Open work across the experiment and OS branches. Read `AGENTS.md` first.

## Alpine 3.24 image — branch `os/alpine-3.24` (from `main`)

Done: Golden image on Alpine 3.24.3 with linux-lts 6.18.58; unused kernel
subsystems pruned after a dependency check (`prune-kernel-modules.sh` and its
test), initramfs regenerated from the same tree; the Alpine release pinned
only in `packaging/alpine/alpine-release`; `scripts/alpine-bump.sh <version>`
checks and performs moves; installers use the running system's own release.

To do:

- Open a PR against `main` and run the **Appliance ISO** workflow; it must
  pass before merge (`docs/GOLDEN-IMAGE.md`). The image has not been built yet
  (no Docker on the development host).
- If the build fails on a name in `kernel-modules.required` or on `mkinitfs`,
  fix that; keep the dependency check.
- Review `docs/WEB-UPDATE.md` (installation boundary) and run
  `sh scripts/ci/bootstrap-compatibility.sh`; the bootstrap binaries did not
  change.

## Rust — branch `experiment/rust-native` (from `experiment/rust-compare`)

Done: event-driven runtime with no timer polling (`Shutdown`, `poll(2)`
accept, `waitid`-based command runner); every `unsafe` block in `src/sys.rs`,
denied elsewhere. Zero warnings on Windows and musl; all tests pass.

To do:

1. Split into a workspace (core, config, services, api, applyd) and add a
   faster release profile for iteration (thin LTO, more codegen units);
   measure build times.
2. Trim dependencies (`chrono` and `time` overlap; `anyhow`, `regex`,
   `x509-parser` are candidates); measure binary sizes.
3. Run the Unix-only tests (`proc`, `sys`) on Linux; re-measure memory and
   idle CPU on the lab router with `tools/memlab/` and update `COMPARISON.md`.
4. Later: remove the `goish` compatibility layer, port more Go tests, add
   landlock/seccomp confinement.

## Lab router

The lab VM used for the memory runs may still run the experiment daemons via
bind mounts (`/opt/memtest`, state backup in `/var/tmp/memtest-state.tar`).
Restore as root with `sh /opt/memtest/memtest.sh restore`, then delete both.

## Go follow-up

Return the 64 MiB Argon2id buffer to the system after each login in Go
`routerd` (for example `debug.FreeOSMemory()`), without changing the hashing
parameters; a separate PR on `main`.
