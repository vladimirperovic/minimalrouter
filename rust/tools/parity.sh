#!/bin/sh
# Generates Go reference cases and runs the Rust differential tests.
#   sh rust/tools/parity.sh [cases]
set -eu
root="$(cd "$(dirname "$0")/../../.." && pwd)"
cases="${1:-4000}"
cd "$root"
go run ./rust/_goparity -out rust/target/parity -n "$cases"
cd rust
cargo test --release --test 'parity_*' -- --nocapture
