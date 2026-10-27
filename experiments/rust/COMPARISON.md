# Go vs Rust: Minimal Router port comparison

This records what was measured when the whole Minimal Router code base was
ported from Go to Rust on the `experiment/rust-compare` branch. The port lives
in `experiments/rust/`; the Go code is unchanged.

Measured on 2026-10-27 on one Windows 11 workstation, cross-compiling for
`linux/amd64`. Memory was measured on an installed Minimal Router running as a
Hyper-V virtual machine (see [Memory on the Hyper-V lab](#memory-on-the-hyper-v-lab));
nothing was run on Proxmox or real hardware.

## Scope

Everything was ported: all 24 packages under `internal/` and all 8 commands
under `cmd/` (`routerd`, `router-applyd`, `router-recovery`, `router-setup`,
`router-update`, `firmware-keygen`, `firmware-sign`, `minimalrouter-mcp`).
The dashboard (`web/`) and packaging are shared and were not touched.

Toolchains: Go 1.26.9 (`CGO_ENABLED=0`, pure-Go SQLite). Rust 1.99.1, GNU host
toolchain with MinGW for the bundled SQLite C code, `cargo-zigbuild` for the
static musl target.

## Results at a glance

| Measure | Go | Rust | Difference |
|---|---:|---:|---:|
| All 8 binaries, stripped, static linux/amd64 | 56.0 MB | 28.6 MB | −49% |
| `routerd` binary | 14.7 MB | 10.5 MB | −28% |
| `router-applyd` binary | 9.2 MB | 3.9 MB | −58% |
| Production code lines (non-blank, non-comment) | 32,933 | 29,815 | −9% |
| Test functions | 727 | 97 | −87% |
| Test code lines | 21,502 | 2,198 | −90% |
| Cold release build, all 8 binaries | 13.4 s | 124 s | 9× slower |
| Incremental build after a one-line API change | 0.9 s | 5.1 s dev / 96 s release | 6–107× slower |
| Third-party dependencies | 30 modules | 35 direct, 210 crates total | |
| `routerd` resident memory, idle (Hyper-V lab) | 24.4 MiB | 16.6 MiB | −32% |
| `routerd` resident memory after an API load with one login | 59.2 MiB | 21.9 MiB | −63% |
| `routerd` peak resident memory (VmHWM) | 120.6 MiB | 80.9 MiB | −33% |
| `router-applyd` resident memory, idle | 15.8 MiB | 12.4 MiB | −22% |
| `routerd` CPU for the load phase (one login, 3,748 requests) | 1.68 s | 1.01 s | −40% |
| `routerd` CPU while idle, per minute | 0.005 s | 0.057 s | about 10× more |

## Binary sizes

Built with `go build -trimpath -ldflags="-s -w"` and with a Rust release
profile of `strip = "symbols"`, `lto = true`, `codegen-units = 1`.

| Binary | Go (bytes) | Rust (bytes) | Change |
|---|---:|---:|---:|
| routerd | 14,708,898 | 10,528,928 | −28% |
| router-applyd | 9,220,258 | 3,852,864 | −58% |
| router-recovery | 8,978,594 | 4,357,152 | −51% |
| router-setup | 8,908,962 | 4,417,952 | −50% |
| minimalrouter-mcp | 6,299,810 | 2,310,440 | −63% |
| router-update | 3,068,066 | 2,184,584 | −29% |
| firmware-sign | 2,592,930 | 537,152 | −79% |
| firmware-keygen | 2,261,154 | 445,152 | −80% |

## Memory on the Hyper-V lab

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

| | Go | Rust |
|---|---:|---:|
| Files | 208 | 113 |
| Lines including blanks and comments | 38,432 | 34,141 |
| Code lines | 32,933 | 29,815 |

The Rust total includes about 1,900 code lines in `src/goish/`, a layer that
reproduces Go standard-library behaviour the router depends on (IP parsing,
JSON `null` versus empty lists, case-insensitive JSON keys, time formats,
`flag` parsing, Go's `CombinedOutput` and `WaitDelay`). It also includes an
HTTP/1.1 server (`src/httpd/`, about 1,100 code lines) because Go's
`net/http` has no equivalent in the Rust standard library. Without those two
pieces the Rust code is about 19% smaller than the Go code.

`unsafe` appears in 31 places across 13 files, all thin calls into libc
(`flock`, `statvfs`, `getifaddrs`, `setsid`, process-group `kill`,
`geteuid`, `SO_PEERCRED`).

## Correctness evidence

The port aimed for identical behaviour, so most checks compare Rust output
with Go output on the same input:

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
generate the cases). Reproduce with `sh experiments/rust/tools/parity.sh`.

What is **not** covered:

- The Rust suite is much smaller than the Go suite (97 versus 727 test
  functions); most Go unit tests were not ported.
- The Rust test suite does not run on Linux, because this workstation has no
  WSL, Docker or Linux host; the A/B slot test was compiled but not run.
- The Rust `routerd` and `router-applyd` ran only on the Hyper-V lab VM:
  startup reconcile of the real configuration (PPPoE, WireGuard, dnsmasq,
  nftables came up), and read-only API traffic. Configuration changes,
  confirmation and rollback, updates, recovery and the other commands were
  not exercised there.
- The `Appliance ISO` workflow was not run, and the Rust build is not wired
  into packaging.

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
- Operating-system error text differs (for example `File exists (os error
  17)` versus Go's `open PATH: file exists`).

## Observations

- **Memory.** Rust's memory use follows its allocations closely: the 64 MiB
  Argon2id buffer goes back to the system as soon as the check ends. Go keeps
  freed memory until its runtime returns it, which shows up as a high
  resident size after a login.
- **Binary size.** Rust binaries are about half the size overall. The gap is
  largest for small tools, where Go's runtime and the pure-Go SQLite library
  dominate.
- **Build speed.** Go builds and cross-compiles far faster with no extra
  toolchain. The Rust release profile chosen for size (fat LTO, one codegen
  unit) makes every release build slow; a development build is 5 s.
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

Keep Go for Minimal Router for now. Rust halves the binary size, and on the lab
VM its `routerd` used a third less memory at idle, held 37 MiB less after a
login and peaked 40 MiB lower. With a 1 GiB minimum those savings are real but
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
. experiments/rust/tools/env.sh
sh experiments/rust/tools/parity.sh
cd experiments/rust && cargo test
cargo zigbuild --release --target x86_64-unknown-linux-musl --bins
```

The memory measurement uses `experiments/rust/tools/memlab/`: `memtest.sh`
runs on the router as root (copy it to `/opt/memtest/` with the two builds in
`/opt/memtest/go/` and `/opt/memtest/rust/`, then run `memtest.sh backup`
once), `memdrive.py` drives the runs from a LAN host, and `summarize.py`
turns the results into the tables above. The raw results of the runs reported
here are in `results-2026-10-27.jsonl`. After measuring, `memtest.sh restore`
puts the installed binaries and the original state back.
