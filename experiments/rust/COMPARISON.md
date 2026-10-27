# Go vs Rust: Minimal Router port comparison

This records what was measured when the whole Minimal Router code base was
ported from Go to Rust on the `experiment/rust-compare` branch. The port lives
in `experiments/rust/`; the Go code is unchanged.

Measured on 2026-10-27 on one Windows 11 workstation, cross-compiling for
`linux/amd64`. Nothing in this report was run on a Minimal Router appliance.

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
| Memory use on the appliance | not measured | not measured | |

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
- Linux-only paths were compiled for Linux but not run there, because this
  workstation has no WSL, Docker or Linux host. This includes the A/B slot
  test, `router-applyd`'s privileged actions and `rc-service` handling.
- No Rust binary has run on an appliance, and the `Appliance ISO` workflow
  was not run. The Rust build is not wired into packaging.
- Memory, CPU and start-up time were not measured. On a 128 MiB appliance
  this is the most important open question.

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

Keep Go for Minimal Router for now. Rust halves the binary size, but Go has the
complete test suite, the validated appliance pipeline, much faster builds and
simpler cross-compilation, and switching would mean re-proving everything on
real hardware. If memory headroom on the 128 MiB appliance becomes the deciding
factor, measure resident memory of both builds of `routerd` and
`router-applyd` on the Hyper-V lab before deciding; that measurement is the one
result that could change this recommendation.

## Reproducing

From the repository root, in Git Bash:

```bash
. experiments/rust/tools/env.sh
sh experiments/rust/tools/parity.sh
cd experiments/rust && cargo test
cargo zigbuild --release --target x86_64-unknown-linux-musl --bins
```
