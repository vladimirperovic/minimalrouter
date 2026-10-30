#!/bin/sh
# The bootstrap programs must not depend on build metadata: two releases built
# from the same bootstrap source have to be byte-identical, or no release could
# ever be installed over another as a web update (docs/WEB-UPDATE.md).
#
# Builds them once the way scripts/build-rust.sh does, then again with a
# version, commit and date injected into the compile-time environment, and
# fails if any byte differs.
set -eu
cd "$(dirname "$0")/../.."
root="$PWD"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT HUP INT TERM

target=x86_64-unknown-linux-musl
sh scripts/build-rust.sh amd64 bootstrap "$work/plain"

cargo_home="${CARGO_HOME:-$HOME/.cargo}"
rustup_home="${RUSTUP_HOME:-$HOME/.rustup}"
(
    cd rust
    MINIMALROUTER_VERSION=9.9.9 MINIMALROUTER_COMMIT=0123456789abcdef MINIMALROUTER_BUILD_DATE=2099-01-01T00:00:00Z \
        RUSTFLAGS="--remap-path-prefix=$root=/src --remap-path-prefix=$cargo_home=/cargo --remap-path-prefix=$rustup_home=/rustup" \
        CARGO_TARGET_DIR="$work/stamped" \
        cargo zigbuild --release --locked --target "$target" --bin router-update --bin router-recovery --bin router-setup
)

status=0
for command in router-update router-recovery router-setup; do
    plain="$(sha256sum "$work/plain/$command-linux-amd64" | cut -d' ' -f1)"
    stamped="$(sha256sum "$work/stamped/$target/release/$command" | cut -d' ' -f1)"
    if [ "$plain" = "$stamped" ]; then
        printf 'same     %-18s %s\n' "$command" "$plain"
    else
        printf 'DIFFERS  %-18s plain=%s stamped=%s\n' "$command" "$plain" "$stamped"
        status=1
    fi
done
[ "$status" -eq 0 ] || echo "A bootstrap program embeds build metadata; web updates between releases would be impossible." >&2
exit "$status"
