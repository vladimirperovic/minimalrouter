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

Done:

- Event-driven runtime with no timer polling (`Shutdown`, `poll(2)` accept,
  `waitid`-based command runner); every `unsafe` block in
  `crates/mr-core/src/sys.rs`, denied elsewhere (34 blocks, each with a
  `SAFETY:` comment).
- Workspace split: `mr-core` ← `mr-config` ← `mr-services` ← {`mr-api`,
  `mr-applyd`}. The root package `minimalrouter` is a facade that re-exports
  them under the original module paths and owns the eight binaries and
  `tests/`, so commands such as `cargo zigbuild --release --target
  x86_64-unknown-linux-musl --bins` are unchanged. Each crate re-exports the
  layer below with `pub use`, so modules still say `crate::config`. The layers
  follow dependencies, not responsibilities (`httpd` and `auth` live in
  `mr-config`).
- The privileged boundary holds in the binaries: the release `routerd`
  contains no `mr-applyd` code and `router-applyd` none of `mr-api` (checked
  from the panic-location strings in the stripped binaries).
- `release-fast` profile (thin LTO, 16 codegen units, incremental) for
  iterating on a build that must run on the router. Size and memory figures
  come from `release`.
- `regex` limited to the Unicode tables the 24 patterns use (`\pL \pN`,
  `(?i)`, `\s \d \b`) plus `perf-literal`: 385 KB smaller per binary that uses
  it. Without `perf-literal` the diagnostics redaction patterns ran about 170
  times slower on a 2 MB blob (387 ms against 2 ms). Every pattern was
  compiled with the reduced feature set.
- `time` stays in `mr-config` only: `tlsutil.rs` calls `::time::OffsetDateTime`
  for `rcgen`. `chrono` stays because `goish::time` needs the local time zone,
  which `time` does not give reliably in a multi-threaded Unix process.
- Fixed `goish::json::List`: it derived `Hash` next to a hand-written
  `PartialEq`, so two equal lists could hash differently (`clippy`
  `derived_hash_with_manual_eq`, deny by default). Nothing used `List` as a
  key. Regression test added.
- `sys.rs` Linux-only calls (`prctl`, `getresuid`, `getresgid`) are gated on
  `target_os = "linux"`; elsewhere the hardening calls return `Unsupported`.
  `cargo test --workspace` therefore runs natively on macOS: 113 tests pass.
  The macOS build shows 8 dead-code warnings for code only Linux uses.
- Windows verification on `0b7af8e` (2026-10-27): `cargo test --workspace`
  passes 105 tests without compiler warnings, `cargo clippy --workspace
  --all-targets` exits 0 with 110 unique lint warnings, and the regular musl
  release build passes without compiler warnings. The missing clippy
  toolchain component was installed; no Windows source fix was needed.
- Linux verification: all 36 `mr-core` tests pass on Alpine virt 3.22.6 /
  Linux 6.12.110-0-virt in a temporary diskless Hyper-V VM (no network adapter,
  2 vCPU, 1 GiB RAM). This includes five `proc`, two Unix-only `sys`, and the
  firmware stage/activate/rollback test. The initial run passed 35/36 because
  loopback was down; after bringing `lo` up, all pass. The VM was removed.
  The full workspace suite and daemon hardening/startup have not run on Linux.

Build times (MacBook Air M1, musl through zigbuild, warm zig cache, one run
each, expect about ±15%). The first build after installing zig is slower
because its C cache is cold; do not use it as a baseline.

| | one crate | workspace |
|---|---:|---:|
| dev, clean | 32 s | 27 s |
| dev, one-line change in the API | 5 s | 3 s |
| release, clean | 125 s | 141 s (119 s in an earlier run) |
| release, one-line change in the API | 106 s | 93 s |
| `release-fast`, clean / one-line change | — | 107 s / 73 s |

Release binary sizes (bytes, `x86_64-unknown-linux-musl`):

| | one crate | split | split + regex trim |
|---|---:|---:|---:|
| routerd | 10,574,880 | 11,089,312 | 10,698,016 |
| router-applyd | 3,882,808 | 3,971,408 | 3,584,936 |
| router-recovery | 4,359,072 | 4,555,808 | 4,171,424 |
| router-setup | 4,462,112 | 4,526,496 | 4,141,216 |
| router-update | 2,205,392 | 2,203,584 | 1,817,816 |
| minimalrouter-mcp | 2,310,504 | 2,310,504 | 2,310,504 |

Windows musl release verification against "split + regex trim" (bytes;
source `0b7af8e`, version/commit/build-date metadata injected):

| Binary | Reference | Windows | Delta |
|---|---:|---:|---:|
| routerd | 10,698,016 | 10,698,784 | +768 |
| router-applyd | 3,584,936 | 3,585,096 | +160 |
| router-recovery | 4,171,424 | 4,170,528 | -896 |
| router-setup | 4,141,216 | 4,141,344 | +128 |
| router-update | 1,817,816 | 1,817,304 | -512 |
| minimalrouter-mcp | 2,310,504 | 2,310,440 | -64 |

The cause of these sub-0.03% differences was not isolated. Refreshed Windows
build times (one run, clean / one API comment edit, all eight musl binaries):
dev 47.0/4.9 s, release 119.8/76.1 s,
release-fast 62.4/34.2 s; Go 41.2/1.3 s.
See [COMPARISON.md](COMPARISON.md) for cache methodology and
[validation-2026-10-27.json](validation-2026-10-27.json) for measured evidence.

To do:

1. Windows build/test verification is complete; review the existing clippy
   warnings separately if desired. No lint suppression was added.
2. Re-measure memory, idle CPU and start-up on the Hyper-V lab router with
   `tools/memlab/` (Go, Rust, Go, Rust, both from the same source commit).
   **Not measured:** no current lab credential file was supplied, and SSH
   keys did not authenticate. No binaries were deployed or router state
   changed. The event-driven build's idle CPU remains unknown; "about 10×
   more than Go" belongs to the historical polling build. Linux `mr-core`
   tests are complete, but daemon startup/hardening is not covered by them.
   The required `memtest.sh restore` and removal of `/opt/memtest` and
   `/var/tmp/memtest-state.tar` are still pending; previous harness state
   could not be checked. `COMPARISON.md` now separates current build data
   from historical runtime data.
3. The split made `routerd` 4.9% larger (10.57 → 11.09 MB) for a reason not
   found; after the regex change and current Windows verification it remains
   approximately 1.2% above the single-crate reference. Cause not established.
4. Other trim candidates, not touched: `x509-parser` (two call sites in
   `tlsutil.rs`), `anyhow` (it is the `goish` error type), duplicate crate
   versions pulled in transitively (`base64` 0.22/0.23, `getrandom` 0.2/0.3).
5. Later: remove the `goish` compatibility layer, port more Go tests, add
   landlock/seccomp confinement.

Building on macOS (Apple silicon): `tools/env.sh` is Windows-only. Install
rustup (stable, target `x86_64-unknown-linux-musl`), `cargo install
cargo-zigbuild --locked` and zig (for example the PyPI `ziglang` wheel with
`zig` on `PATH`). Linux binaries cannot run on the Mac.

## Lab router

The lab VM used for the memory runs may still run the experiment daemons via
bind mounts (`/opt/memtest`, state backup in `/var/tmp/memtest-state.tar`).
Restore as root with `sh /opt/memtest/memtest.sh restore`, then delete both.

`tools/memlab/memdrive.py` logs in as root on `192.168.1.1:8443`. That is the
lab router only on the lab's own network; on a home LAN the same address can be
a production router. `memtest.sh` stops the daemons and replaces
`/var/lib/minimalrouter*` from a tar. Run both only against the lab VM.

## Go follow-up

Return the 64 MiB Argon2id buffer to the system after each login in Go
`routerd` (for example `debug.FreeOSMemory()`), without changing the hashing
parameters; a separate PR on `main`.
