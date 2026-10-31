#!/bin/bash
set -euo pipefail
cd "$SRC/minimalrouter/rust"
# This workspace has its own lockfile; appliance dependencies remain separate.
export CARGO_TARGET_DIR="$WORK/minimalrouter-fuzz-target"
cargo fetch --locked --manifest-path fuzz/Cargo.toml
sha256sum fuzz/Cargo.lock > "$WORK/minimalrouter-fuzz-lock.sha256"
export CARGO_NET_OFFLINE=true
cargo fuzz build -O --debug-assertions
sha256sum -c "$WORK/minimalrouter-fuzz-lock.sha256"
for target in http_request config_json apply_ipc firmware_metadata; do
    cp "$CARGO_TARGET_DIR/x86_64-unknown-linux-gnu/release/$target" "$OUT/$target"
    cp "fuzz/options/$target.options" "$OUT/$target.options"
    cp "fuzz/dictionaries/$target.dict" "$OUT/$target.dict"
    (cd "fuzz/seeds/$target" && zip -q "$OUT/${target}_seed_corpus.zip" ./*)
done
