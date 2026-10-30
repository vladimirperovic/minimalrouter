#!/bin/sh
# Builds the Minimal Router Linux binaries from the Rust workspace.
#
# Usage: sh scripts/build-rust.sh amd64|arm64 [bootstrap|runtime|all] [OUTDIR]
#
#   bootstrap  router-update, router-recovery, router-setup. They run outside
#              the A/B slot and every activation requires the candidate's copy
#              to be byte-identical to the installed one, so they are built
#              without version, commit or date: two releases built from the
#              same bootstrap source are then identical.
#   runtime    routerd and router-applyd, stamped with BUILD_VERSION,
#              BUILD_COMMIT and BUILD_DATE.
#   all        both (default).
#
# Output: OUTDIR/<command>-linux-<arch> (OUTDIR defaults to bin).
#
# Static musl binaries are cross-built with cargo-zigbuild (pip install
# cargo-zigbuild ziglang). A native x86-64 Linux host with musl-gcc may build
# the amd64 target with plain cargo.
set -eu

usage() {
    echo 'usage: build-rust.sh amd64|arm64 [bootstrap|runtime|all] [OUTDIR]' >&2
    exit 2
}

arch="${1:-}"
part="${2:-all}"
out="${3:-bin}"
case "$arch" in
    amd64) target=x86_64-unknown-linux-musl ;;
    arm64) target=aarch64-unknown-linux-musl ;;
    *) usage ;;
esac
case "$part" in bootstrap | runtime | all) ;; *) usage ;; esac

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
case "$out" in /*) ;; *) out="$root/$out" ;; esac
mkdir -p "$out"

if cargo zigbuild --help >/dev/null 2>&1; then
    builder="cargo zigbuild"
elif [ "$arch" = amd64 ] && [ "$(uname -s)-$(uname -m)" = Linux-x86_64 ] && command -v musl-gcc >/dev/null 2>&1; then
    builder="cargo build"
    export CC_x86_64_unknown_linux_musl=musl-gcc
else
    echo "ERROR: building $target needs cargo-zigbuild and zig (pip install cargo-zigbuild ziglang)" >&2
    exit 1
fi

# Paths must not leak into the binaries, or two checkouts would differ.
cargo_home="${CARGO_HOME:-$HOME/.cargo}"
rustup_home="${RUSTUP_HOME:-$HOME/.rustup}"
rustflags="--remap-path-prefix=$root=/src --remap-path-prefix=$cargo_home=/cargo --remap-path-prefix=$rustup_home=/rustup"

# build TAG BIN... : release build of the named binaries into OUTDIR.
build() {
    tag="$1"
    shift
    bins=""
    for bin in "$@"; do
        bins="$bins --bin $bin"
    done
    # Relative for cargo, so a Windows cargo run from Git Bash resolves it too.
    targetdir="target/dist-$tag"
    # shellcheck disable=SC2086
    ( cd "$root/rust" && RUSTFLAGS="$rustflags" CARGO_TARGET_DIR="$targetdir" $builder --release --locked --target "$target" $bins )
    for bin in "$@"; do
        cp "$root/rust/$targetdir/$target/release/$bin" "$out/$bin-linux-$arch"
    done
}

if [ "$part" = bootstrap ] || [ "$part" = all ]; then
    echo "Building bootstrap binaries for $arch (no version stamp)..."
    ( unset MINIMALROUTER_VERSION MINIMALROUTER_COMMIT MINIMALROUTER_BUILD_DATE
      build bootstrap-$arch router-update router-recovery router-setup )
fi
if [ "$part" = runtime ] || [ "$part" = all ]; then
    echo "Building runtime binaries for $arch..."
    ( MINIMALROUTER_VERSION="${BUILD_VERSION:-dev}"
      MINIMALROUTER_COMMIT="${BUILD_COMMIT:-unknown}"
      MINIMALROUTER_BUILD_DATE="${BUILD_DATE:-unknown}"
      export MINIMALROUTER_VERSION MINIMALROUTER_COMMIT MINIMALROUTER_BUILD_DATE
      build runtime-$arch routerd router-applyd )
fi
echo "Built $part binaries for $arch in $out"
