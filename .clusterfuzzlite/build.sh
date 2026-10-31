#!/bin/bash
set -euo pipefail
cd "$SRC/minimalrouter/rust"
# This workspace has its own lockfile; appliance dependencies remain separate.
export CARGO_TARGET_DIR="$WORK/minimalrouter-fuzz-target"
cargo +nightly fuzz build --locked -O --debug-assertions
for target in http_request config_json apply_ipc firmware_metadata; do
    cp "$CARGO_TARGET_DIR/x86_64-unknown-linux-gnu/release/$target" "$OUT/$target"
    cp "fuzz/options/$target.options" "$OUT/$target.options"
    cp "fuzz/dictionaries/$target.dict" "$OUT/$target.dict"
    (cd "fuzz/seeds/$target" && zip -q "$OUT/${target}_seed_corpus.zip" ./*)
done
