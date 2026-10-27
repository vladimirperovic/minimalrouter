#!/bin/sh
# Generates Go reference cases and runs the Rust differential tests.
#   sh experiments/rust/tools/parity.sh [cases]
set -eu
root="$(cd "$(dirname "$0")/../../.." && pwd)"
cases="${1:-4000}"
cd "$root"
go run ./experiments/rust/_goparity -out experiments/rust/target/parity -n "$cases"
cd experiments/rust
cargo test --release --test 'parity_*' -- --nocapture
