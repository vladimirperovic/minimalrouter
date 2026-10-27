# Go vs Rust: Minimal Router port comparison

This records the Rust port and its current layered workspace on
`experiment/rust-native`. Build sizes, build times and Windows/Linux test
results were refreshed on 2026-10-27 from source commit `0b7af8e`.
The Go source is unchanged. Machine-readable build and test evidence is in
[validation-2026-10-27.json](validation-2026-10-27.json).

The memory, CPU and start-up figures below are **historical measurements of
the earlier polling runtime** on a Hyper-V lab router. The current
event-driven runtime has not been re-measured. No new router run or restore
was possible without the current lab credential file. Nothing was run on
Proxmox or real hardware.

## Scope

Everything was ported: all 24 packages under `internal/` and all 8 commands
under `cmd/` (`routerd`, `router-applyd`, `router-recovery`, `router-setup`,
`router-update`, `firmware-keygen`, `firmware-sign`, `minimalrouter-mcp`).
The dashboard (`web/`) and packaging are shared and were not touched.

Toolchains: Go 1.26.9 (`CGO_ENABLED=0`, pure-Go SQLite). Rust 1.99.1,
`x86_64-pc-windows-gnu` host with MinGW for bundled SQLite,
`cargo-zigbuild` 0.24.0 and Zig 0.17.0 for the static musl target.
The Windows environment was loaded with `. rust/tools/env.sh`.

## Results at a glance

| Current workspace build / test measure | Go | Rust |
|---|---:|---:|
| All 8 binaries, stripped, static linux/amd64 | 56.0 MB | 27.7 MB (-50.6%) |
| `routerd` binary | 14.7 MB | 10.7 MB |
| Production code lines (non-blank, non-comment) | 32,933 | 30,119 |
| Test functions | 727 | 111 |
| Test code lines | 21,502 | 2,254 |
| Clean release build, all 8 binaries | 41.2 s | 119.8 s (62.4 s `release-fast`) |
| Rebuild after a one-line API change | 1.3 s | 4.9 s dev / 34.2 s `release-fast` / 76.1 s release |
| Third-party dependencies | 5 direct, 30 modules | 34 direct, 151 crates |
| `unsafe` code | — | 34 blocks, all in `mr-core/src/sys.rs` |
| Windows workspace tests | Not re-run | 105 passed |
| Linux `mr-core` tests, including Unix-only cases | Not re-run | 36 passed |
| New lab memory, idle CPU and start-up comparison | Not re-measured | Not measured |

Historical polling-runtime memory/CPU figures are kept in their own section
below; the old "about 10× more idle CPU" result does not describe this build.

## Binary sizes

All eight outputs were built from `0b7af8e` for Linux AMD64 with version
`0.3.3`, commit and build-date metadata set consistently for both languages.
Go used `-trimpath -ldflags="-s -w -buildid= -X …"`; Rust used
`strip = "symbols"`, `lto = true`, `codegen-units = 1` (`release`).
All outputs are ELF binaries without a dynamic interpreter.

| Binary | Go (bytes) | Rust workspace (bytes) | Change |
|---|---:|---:|---:|
| routerd | 14,708,862 | 10,698,784 | -27.3% |
| router-applyd | 9,220,222 | 3,585,096 | -61.1% |
| router-recovery | 8,978,558 | 4,170,528 | -53.6% |
| router-setup | 8,908,926 | 4,141,344 | -53.5% |
| minimalrouter-mcp | 6,299,774 | 2,310,440 | -63.3% |
| router-update | 3,068,030 | 1,817,304 | -40.8% |
| firmware-sign | 2,592,894 | 536,576 | -79.3% |
| firmware-keygen | 2,261,118 | 444,672 | -80.3% |

Against the Mac-built "split + regex trim" column in `NEXT-STEPS.md`:

| Binary | Reference (bytes) | Windows cross-build (bytes) | Delta (bytes) |
|---|---:|---:|---:|
| routerd | 10,698,016 | 10,698,784 | +768 |
| router-applyd | 3,584,936 | 3,585,096 | +160 |
| router-recovery | 4,171,424 | 4,170,528 | -896 |
| router-setup | 4,141,216 | 4,141,344 | +128 |
| router-update | 1,817,816 | 1,817,304 | -512 |
| minimalrouter-mcp | 2,310,504 | 2,310,440 | -64 |

Each difference is below 0.03%. The host/linker/build-metadata contribution
was not isolated, so these differences are not attributed to a source change.
The remaining approximately 1.2% increase of `routerd` over the single-crate
10,574,880-byte reference remains unexplained. No `mr-applyd`
panic-location strings were found in `routerd`, and no `mr-api` ones in
`router-applyd`. This check does not establish the complete privilege boundary.

## Build times

One sequential run per cell on the Windows 11 host (Intel i7-14700KF,
20 cores / 28 logical processors, approximately 96 GiB RAM). Clean means a
fresh Cargo target directory for Rust, with each profile initially empty,
and a fresh `GOCACHE` for Go. Downloads and the global Zig cache were warm.
These are subprocess wall times, not a statistically established average.
The earlier single-crate timings used a different source/cache state.

| All eight Linux AMD64 binaries | Go, stripped | Rust dev | Rust release | Rust release-fast |
|---|---:|---:|---:|---:|
| Clean | 41.2 s | 47.0 s | 119.8 s | 62.4 s |
| One-line API comment change | 1.3 s | 4.9 s | 76.1 s | 34.2 s |

The comment was appended to `crates/mr-api/src/api/mod.rs` for Rust and
`internal/api/wireguard.go` for Go, then restored byte-for-byte. This measures
rebuilding the dependency graph after an API edit without changing runtime
semantics. Go binaries were reproduced byte-for-byte after restoration;
Rust release sizes/hashes were saved before any comment edits.
`release-fast` uses thin LTO, 16 codegen units and incremental compilation.
All binary-size figures above use the regular `release` profile.

## Memory on the Hyper-V lab

**Current event-driven build: not measured.** The Go → Rust → Go → Rust
rerun, including idle CPU, RSS/peak memory and start-up, still requires the
current root SSH and dashboard credentials. Existing SSH keys did not
authenticate. No binaries were deployed and no router services or state were
changed during this validation. `memtest.sh restore` was not executed;
any old `/opt/memtest` mounts and `/var/tmp/memtest-state.tar` remain unverified.

### Historical polling-runtime measurements

Measured on 2026-10-27 on a lab VM: Hyper-V generation 1,
2 vCPU, 2 GiB RAM, Alpine with kernel 6.12.112-lts. The router was a real
installation with PPPoE WAN (to a lab ISP VM), WireGuard, dnsmasq, the DNS
filter (24 MB of lists on disk) and DNS risk feeds (214 MB on disk). Hyper-V is
not a qualified platform; Proxmox and real hardware were not measured.

Method:

- Go and Rust were built from the same commit with version 0.3.3: Go with the
  `Makefile` flags (`-trimpath -ldflags "-s -w -buildid= -X …"`), Rust with the
  release profile above for `x86_64-unknown-linux-musl` (musl's allocator).
- Each build's `routerd` and `router-applyd` were bind-mounted over the active
  slot's binaries and started by the real OpenRC services, so the user,
  environment, `umask`, `ulimit` and supervisor were the production ones.
- Every run started from the same copy of `/var/lib/minimalrouter` and
  `/var/lib/minimalrouter-applyd`, with the page cache dropped.
- Schedule: start, sample after 1 and 4 idle minutes, then one dashboard login
  and 3 minutes of four parallel keep-alive clients reading 39 API endpoints
  and the dashboard files, sampling half way and at the end, then once more a
  minute after the load.
- Two runs per build, alternating Go, Rust, Go, Rust. Both builds served
  exactly the same 3,748 requests per run with identical status codes
  (3,660 × 200, 88 × 404), so they did the same work.
- Go sets `GOGC=50` and soft memory limits in `main` (128 MiB for `routerd`,
  64 MiB for `router-applyd`). Rust has no equivalent tuning.

Figures are VmRSS from `/proc/<pid>/status`, the mean of the two runs; the
range is in brackets where the runs differed. PSS was within 0.1 MiB of RSS
throughout, so nothing was shared between the two daemons.

`routerd`:

| Sample | Go RSS | Rust RSS | Go heap (anon) | Rust heap (anon) | Go peak | Rust peak |
|---|---:|---:|---:|---:|---:|---:|
| Idle, 1 min | 23.4 [22.2–24.6] | 16.5 | 10.7 | 9.3 | 23.4 | 16.6 |
| Idle, 4 min | 24.4 [23.3–25.5] | 16.6 | 11.7 | 9.3 | 24.4 | 16.7 |
| Load, half way | 33.4 [30.8–36.1] | 20.7 [20.2–21.2] | 19.8 | 12.4 | 120.6 | 80.9 |
| Load, end | 58.6 [56.9–60.2] | 21.9 [20.5–23.4] | 44.9 | 13.7 | 120.6 | 80.9 |
| 1 min after load | 59.2 [58.1–60.3] | 21.9 [20.4–23.4] | 45.5 | 13.7 | 120.6 | 80.9 |

`router-applyd`:

| Sample | Go RSS | Rust RSS | Go peak | Rust peak |
|---|---:|---:|---:|---:|
| Idle, 4 min | 15.8 [14.5–17.1] | 12.4 [11.5–13.3] | 16.0 | 12.8 |
| Load, end | 16.7 | 12.6 [11.6–13.5] | 17.1 | 12.9 |

All figures are MiB. The system's `MemAvailable` agreed: about 40 MiB more
memory was free during and after the load with the Rust pair.

What the numbers say:

- **Idle.** Rust `routerd` uses 7.8 MiB less, but most of that is the mapped
  binary (12.7 MiB of file-backed pages for Go, 7.2 MiB for Rust). The heaps
  are close: 11.7 MiB against 9.3 MiB.
- **Peak.** Both peaks include the 64 MiB that one Argon2id password check
  needs (`internal/auth`, m=64 MiB, t=3, p=2). Rust's peak is its idle size
  plus that buffer. Go's peak is about 32 MiB higher still, most likely heap
  growth that `GOGC=50` allows while the 64 MiB buffer is live; this was not
  separated further.
- **After the login.** Rust returns the Argon2 buffer as soon as the check
  ends (musl unmaps large allocations). Go still held 59 MiB, 45 MiB of it
  heap, a minute after the load; how long the Go runtime keeps it, and how
  much of it comes from the login rather than from serving requests, was not
  measured. For context, the installed (older) Go build on the same VM held
  47.7 MiB after a day of uptime.
- **`router-applyd`.** Rust uses 3–4 MiB less, again mostly the smaller
  binary.
- **CPU.** Under load Rust `routerd` used 40% less CPU time, including the
  login and its own idle polling. While idle it used about ten times more
  (5–6 clock ticks of 10 ms per minute against under one for Go), still
  under 0.1% of one core. The port polls where Go blocks: the HTTP accept loop wakes every
  50 ms, and stop-aware waits and the DNS risk queue wake every 250 ms
  (`src/httpd/server.rs`, `src/goish/sync.rs`, `src/dnsrisk/mod.rs`). That
  is a property of this port, not of Rust, and could be removed.
- **Start-up.** `routerd` was ready after 340 ms in all runs (it reused the
  verified startup reconcile). `router-applyd` took 3.65 s (Go) and 3.90 s
  (Rust) to reconcile and open its socket; the init script checks every
  0.25 s, so this is one polling step.

The appliance needs at least 1 GiB of RAM (`README.md`, enforced by the
installer). On that floor both builds have ample headroom; the differences
above are about 4% of the minimum.

## Code size

Counted on `45c2e25` (Go source unchanged since the port). Code lines exclude
blank lines and lines that start with `//`. Rust production code excludes
`#[cfg(test)]` modules; Rust test code is those modules plus `tests/`.

| | Go | Rust workspace |
|---|---:|---:|
| Source files (without tests) | 208 | 119 |
| Lines including blanks and comments | 38,432 | 36,137 |
| Production code lines | 32,933 | 30,119 |
| Test files / test functions | 204 / 727 | 6 + in-module / 111 |
| Test code lines | 21,502 | 2,254 |

The Rust total includes 1,977 code lines in `goish/`, a layer that reproduces
Go standard-library behaviour the router depends on (IP parsing, JSON `null`
versus empty lists, case-insensitive JSON keys, time formats, `flag` parsing).
It also includes an HTTP/1.1 server (`httpd/`, 1,249 code lines) because Go's
`net/http` has no equivalent in the Rust standard library. Without those two
pieces the Rust production code is about 18% smaller than the Go code. Since
the first snapshot the Rust side grew by about 1,700 lines, mostly the
event-driven runtime (`shutdown`, `proc`) and the `sys` module.

The current workspace has 34 `unsafe` blocks, all in
`crates/mr-core/src/sys.rs`, with `unsafe` denied elsewhere and `SAFETY:`
comments on the blocks. This source count was checked in this validation.

## Correctness evidence

Current checks on `0b7af8e`:

- `cargo test --workspace` on Windows: 105 tests passed, no compiler warnings.
- `cargo clippy --workspace --all-targets`: exit 0, 110 unique lint warnings,
  no errors. The missing clippy toolchain component was installed; no Rust
  source changes or lint suppression were needed for Windows.
- The musl release build of all eight binaries passed without compiler warnings.
- The cross-compiled `mr-core` test harness ran on Alpine virt 3.22.6,
  Linux 6.12.110-0-virt, in a temporary diskless Hyper-V generation 1 VM with
  2 vCPU, 1 GiB RAM and no network adapter. All 36 tests passed, including all
  five `proc` tests, both Unix-only `sys` tests, and firmware slot
  stage/activate/rollback. The first attempt passed 35/36 with loopback down;
  bringing `lo` up made the loopback-address test pass. This required no code
  change. The temporary VM was removed afterward.

The following differential/API checks were recorded for the original port.
They compare Rust output with Go output on the same input:

| Check | Cases | Result |
|---|---:|---|
| Configuration validation and normalisation (mutated configs) | 4,002 | byte-identical to Go |
| All service generators (nftables, dnsmasq, WireGuard, …) | 1,408 | byte-identical to Go |
| Go-signed firmware manifests verified in Rust, signatures reproduced | 500 | identical signatures |
| Release version precedence | 2,000 | identical |
| MCP `tools/list`, `initialize`, errors, validation (read-only and admin) | 12 messages | byte-identical |
| `firmware-sign` file hashes and public key for the same tree and key | 1 tree | identical |
| API end to end over HTTP: setup, login, CSRF/Origin/Fetch-Metadata, read-only, logout, audit | 1 scenario | passes |
| MCP bridge binary against the Rust API over TLS | 1 scenario | passes |
| All 91 API routes registered with the same method and path | 91 | identical lists |

The differential harness is `_goparity/main.go` (640 lines of Go that
generate the cases). Reproduce with `sh rust/tools/parity.sh`.

What is **not** covered:

- Most Go unit tests were not ported. The current checks ran 105 tests on
  Windows and 36 `mr-core` tests on Linux (28 overlap, eight are Unix-only).
  The Go suite was not re-run for this documentation refresh.
- The full Rust workspace suite was not run on Linux. The Linux `mr-core`
  run covers `proc`, `sys` and the A/B slot test; it does not establish
  Linux hardening/startup behavior for the management/helper daemons.
- The earlier polling `routerd` and `router-applyd` ran only on the Hyper-V lab VM:
  startup reconcile of the real configuration (PPPoE, WireGuard, dnsmasq,
  nftables came up), and read-only API traffic. Configuration changes,
  confirmation and rollback, updates, recovery and the other commands were
  not exercised there.
- The `Appliance ISO` workflow was not run, and the Rust build is not wired
  into packaging.

## Platform (applies to both builds)

Branch `os/alpine-3.24` (from `main`, commit `e78f0cb`) moves the Golden
appliance image from Alpine 3.22 with linux-lts 6.12 to Alpine 3.24.3 with
linux-lts 6.18.58, the newest long-term kernel (checked against kernel.org on
2026-10-27; 7.x has no long-term release yet). Kernel subsystems a router
never loads are removed after a dependency check, the initramfs is rebuilt
from the same module tree, the Alpine release is pinned in one file
(`packaging/alpine/alpine-release`) and `scripts/alpine-bump.sh` checks and
performs later moves. The image has not been built yet: the Appliance ISO
workflow has not run on that branch. The language choice does not depend on
this change.

## Known behaviour differences

- JSON type-error messages are worded differently (unknown fields are still
  rejected the same way).
- A few lists that Go returns as `null` come back as `[]`, for example DHCP
  leases.
- Dashboard files are served without `Range` or `If-Modified-Since` support.
- The speed test keeps running if the browser tab closes; Go stops it.
- The health DNS probe asks for an A record only (Go asks for A and AAAA);
  the verdict is the same.
- Combined command output lists stdout before stderr instead of interleaving.
- The idle runtime blocks in `poll(2)`, `waitid` and condition variables,
  like Go; the first port polled on timers instead (fixed in `58f9fed`, not
  yet measured on a router).
- Operating-system error text differs (for example `File exists (os error
  17)` versus Go's `open PATH: file exists`).

## Observations

- **Memory (historical polling build).** Rust's memory use followed its
  allocations closely: the 64 MiB
  Argon2id buffer goes back to the system as soon as the check ends. Go keeps
  freed memory until its runtime returns it, which shows up as a high
  resident size after a login.
- **Binary size.** Rust binaries are about half the size overall. The gap is
  largest for small tools, where Go's runtime and the pure-Go SQLite library
  dominate.
- **Build speed.** Go cross-compiles faster with no extra toolchain. In the
  refreshed workspace measurements, a one-line API edit took
  1.3 s in Go, 4.9 s in Rust dev,
  34.2 s in Rust release-fast, and
  76.1 s in Rust release. Fat LTO and one codegen
  unit retain a substantial release rebuild cost.
- **Toolchain.** Go cross-compiles a static binary out of the box. Rust
  needed MinGW for the C SQLite build and zig for musl linking.
- **Fidelity work.** A large share of the effort went into matching Go
  semantics rather than router logic: JSON decoding rules, `net.ParseIP`,
  `filepath.Clean`, `flag`, HTTP routing and `http.Error` bodies. Byte-for-byte
  differential tests were what made the port trustworthy.
- **Concurrency.** Go's goroutines, contexts and channels map to threads,
  stop flags and condition variables. The result is explicit but more verbose,
  and request cancellation (Go's `r.Context()`) has no direct equivalent in
  the synchronous server.

## Recommendation

Keep Go for Minimal Router for now. The current Rust workspace roughly halves
the binary size. In the historical polling-build lab runs, Rust `routerd`
used a third less memory at idle, held 37 MiB less after a login and peaked
40 MiB lower; those runtime numbers have not been confirmed for the current
event-driven build. With a 1 GiB minimum those savings are real but
not decisive. Go has the complete test suite, the validated appliance
pipeline, much faster builds and simpler cross-compilation, and switching
would mean re-proving everything on real hardware.

The memory result points at a cheaper change inside the Go code: after a login,
Go `routerd` kept roughly 37 MiB more than Rust, and the Argon2id buffer is the
likely source. Returning that memory to the system after each key derivation
(for example `debug.FreeOSMemory()`) could close much of the gap without
changing the password-hashing parameters; it would need its own measurement.

## Reproducing

From the repository root, in Git Bash:

```bash
. rust/tools/env.sh
sh rust/tools/parity.sh
cd rust && cargo test --workspace
cargo clippy --workspace --all-targets
cargo zigbuild --release --target x86_64-unknown-linux-musl --bins
```

For the Unix-only tests on a Windows build host, cross-compile the harness:

```bash
cargo zigbuild -p mr-core --tests --target x86_64-unknown-linux-musl
```

Copy the resulting `target/x86_64-unknown-linux-musl/debug/deps/mr_core-*`
test executable to a Linux machine, enable loopback if the environment does
not already do so, and run it with `--nocapture`. Linux daemons compiled for
musl are not executable on the Windows host.

The memory measurement uses `rust/tools/memlab/`. Both harnesses
must target only the confirmed lab router. `memtest.sh` runs there as root;
copy it to `/opt/memtest/` with the same-commit regular-release binaries in
`/opt/memtest/go/` and `/opt/memtest/rust/`. Preserve an existing state backup;
run `memtest.sh backup` only if none exists. For this rerun, execute
`memdrive.py OUT.jsonl go rust go rust` on the lab router, as requested,
with its Python/paramiko dependencies and a temporary credential file
provisioned privately. Keep `MR_LAB_CREDS`, credentials and live state
untracked. `summarize.py` turns the output into tables.

The historical polling raw results remain in
[results-2026-10-27.jsonl](tools/memlab/results-2026-10-27.jsonl); they must
not be relabelled as event-driven measurements. After measuring, run
`sh /opt/memtest/memtest.sh restore` and verify the installed services, then
remove `/opt/memtest` and `/var/tmp/memtest-state.tar`. That router cleanup
has not been executed during this validation.
