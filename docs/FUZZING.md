# Parser fuzzing and router laboratory

ClusterFuzzLite builds four Rust/libFuzzer targets with AddressSanitizer:

| Target | Production code exercised |
| --- | --- |
| http_request | HTTP head, framing, chunked body, URL/query/cookie and response handling through an in-memory connection |
| config_json | Configuration decoding/validation and stable canonical serialization |
| apply_ipc | Single-document request/response decoding and contradictory-outcome rejection |
| firmware_metadata | Manifest parsing/validation/signature rejection, release versions and archive path confinement |

These are bounded parser tests, not a running privileged router. Each input is
limited to 64 KiB, with a 10-second per-input timeout and 1 GiB RSS limit. The
firmware target does not decompress or install an archive. The HTTP harness
opens no sockets. No target executes applyd commands or reads appliance state.
The feature-gated hooks are absent from normal appliance builds.

## CI and reproduction

The Parser fuzzing workflow runs a total 300-second campaign on PRs and main
pushes, a daily 1,800-second batch, and weekly corpus pruning. Manual runs offer
300 or 1,800 seconds. Build failures and crashes fail the job; they are never
converted to successful tests. ClusterFuzzLite uploads crash reproducers, corpus
and diagnostic artifacts. The workflow uses read-only repository permissions
and does not need a PAT, external corpus repository or production secrets.

Synthetic seed inputs and dictionaries live under rust/fuzz. Generated corpus,
crash artifacts, coverage and binaries remain ignored; minimize and review a
reproducer before committing it as a regression. Do not seed with live traffic,
configuration exports, cookies, credentials, backups or private inventory.

On Linux with the nightly Rust toolchain and cargo-fuzz installed:

~~~sh
cd rust
cargo +nightly fuzz build --locked -O --debug-assertions
cargo +nightly fuzz run http_request fuzz/seeds/http_request -- -max_total_time=60
cargo +nightly fuzz run http_request path/to/crash-reproducer
~~~

The independent rust/fuzz lockfile starts from the application's locked
libraries. Update shared dependency versions together. Windows can type-check
the harnesses with cargo check --manifest-path rust/fuzz/Cargo.toml
--no-default-features --locked; this omits native libFuzzer linking and is not
fuzz execution. The CI build enables libFuzzer and ASan normally.

## Network and hardware testing

The existing unfiltered Lab installs the exact successful Appliance ISO into
QEMU, connects an isolated provider/LAN topology, then runs all 3,000 API
contracts, firewall/network scenarios, fault recovery and the soak. It now also
runs weekly on main. A stale ISO is rejected by the existing commit provenance
check. Manual release qualification keeps the only filter empty.

The Lab already measures traffic through NAT and PPPoE with iperf3. Virtualized
throughput is diagnostic and is not a physical-router capacity claim. Keep the
existing QEMU path as the repeatable functional baseline.

For the next hardware stage, register an ephemeral self-hosted runner on a
separate lab controller, with an isolated WAN/provider segment and LAN/client
segment attached to the test appliance. Require an explicit maintainer dispatch
on reviewed main; never expose a persistent private-network runner to arbitrary
public PR code. Runner labels, host addresses and credentials belong in private
operator configuration. No hardware runner is installed or activated by these
repository changes.

TRex is the next load generator for a confirmed isolated lab with suitable NICs.
Measure connection rate, simultaneous connections, TCP/UDP throughput, latency,
loss and recovery under load. Record hardware, firmware, packet sizes and test
duration with results. No TRex or physical-hardware validation is claimed yet.

References: [ClusterFuzzLite Rust integration](https://google.github.io/clusterfuzzlite/build-integration/rust-lang/),
[GitHub Actions integration](https://google.github.io/clusterfuzzlite/running-clusterfuzzlite/github-actions/),
[TRex](https://trex-tgn.cisco.com/).

## Mutation testing

The Safety mutation tests workflow uses pinned cargo-mutants 25.3.1 against
the shared WireGuard handshake verifier and ApplyResponse::validate. It runs
the original tests first, then deliberately changes conditions/operators and
requires the tests to catch the changes. Missed mutations fail CI. Reports
include the mutation, diff and test outcome; compiler-rejected mutations are
not counted as tests catching a bug. This narrow gate runs on PRs, main, weekly
and by manual dispatch, with per-test and build timeouts.

See [cargo-mutants CI guidance](https://mutants.rs/ci.html). The IPC regression
enumerates all sixteen flag combinations against the four documented legal
outcomes; WG tests include same-second activation, exact freshness/clock-skew
boundaries, idle clients and overflow-safe arithmetic.

Version 25.3.1 is intentionally pinned for the function-scoped gate: 27.1.0
was observed to emit struct-field mutations outside its --re filter. The CI
selection check requires both intended functions and rejects unrelated ones.
