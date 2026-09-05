#!/bin/sh
# Decide whether this tree can be delivered to an existing appliance as an
# ordinary A/B web update, or whether it needs the full signed installer.
#
# router-update and router-recovery run from /usr/libexec/minimalrouter/bootstrap,
# outside the A/B slot. cmd/router-update/runtime_layout.go refuses activation
# unless the candidate's copy of each is byte-identical to the installed one,
# because a binary that survives rollback cannot itself be rolled back by moving
# a symlink. So "is this release web-updatable?" is exactly "are the bootstrap
# binaries byte-identical to the baseline release's?".
#
# Both sides are built here with the same toolchain and the same deterministic
# flags, so the answer reflects the bootstrap source only — not the commit hash
# Go would otherwise stamp into every build.
#
# Usage: sh scripts/ci/bootstrap-compatibility.sh [baseline-ref]
#   baseline-ref defaults to bootstrap_baseline_ref in
#   packaging/alpine/bootstrap-baseline.json.
set -eu

BASELINE_FILE="packaging/alpine/bootstrap-baseline.json"
BOOTSTRAP_COMMANDS="router-update router-recovery"
BOOTSTRAP_ARCHES="amd64 arm64"
# Must stay in sync with GO_BOOTSTRAP_BUILD_FLAGS and GO_LDFLAGS in the Makefile.
BOOTSTRAP_LDFLAGS="-s -w -buildid="

json_string_field() {
    sed -n 's/.*"'"$1"'"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$2" | head -1
}

[ -r "$BASELINE_FILE" ] || { echo "ERROR: $BASELINE_FILE is missing" >&2; exit 1; }

BASELINE_REF="${1:-$(json_string_field bootstrap_baseline_ref "$BASELINE_FILE")}"
[ -n "$BASELINE_REF" ] || { echo "ERROR: no bootstrap baseline ref configured" >&2; exit 1; }

if ! git rev-parse --verify --quiet "$BASELINE_REF^{commit}" >/dev/null; then
    echo "SKIP: baseline ref $BASELINE_REF is not present in this checkout."
    echo "      Fetch tags (actions/checkout with fetch-depth: 0) to run this check."
    exit 0
fi

WORK="$(mktemp -d)"
BASELINE_TREE="$WORK/baseline"
cleanup() {
    git worktree remove --force "$BASELINE_TREE" >/dev/null 2>&1 || true
    rm -rf "$WORK"
}
trap cleanup EXIT INT TERM

git worktree add --detach "$BASELINE_TREE" "$BASELINE_REF" >/dev/null 2>&1 ||
    { echo "ERROR: could not check out baseline ref $BASELINE_REF" >&2; exit 1; }

build_bootstrap() {
    tree="$1"
    outdir="$2"
    mkdir -p "$outdir"
    for arch in $BOOTSTRAP_ARCHES; do
        for command in $BOOTSTRAP_COMMANDS; do
            ( cd "$tree" && CGO_ENABLED=0 GOOS=linux GOARCH="$arch" \
                go build -trimpath -buildvcs=false -ldflags="$BOOTSTRAP_LDFLAGS" \
                -o "$outdir/$command-$arch" "./cmd/$command" )
        done
    done
}

echo "Building bootstrap binaries from the working tree..."
build_bootstrap "$PWD" "$WORK/head"
echo "Building bootstrap binaries from baseline $BASELINE_REF..."
build_bootstrap "$BASELINE_TREE" "$WORK/base"

drift=0
for arch in $BOOTSTRAP_ARCHES; do
    for command in $BOOTSTRAP_COMMANDS; do
        name="$command-$arch"
        head_sum="$(sha256sum "$WORK/head/$name" | cut -d' ' -f1)"
        base_sum="$(sha256sum "$WORK/base/$name" | cut -d' ' -f1)"
        if [ "$head_sum" = "$base_sum" ]; then
            printf 'same       %-26s %s\n' "$name" "$head_sum"
        else
            printf 'CHANGED    %-26s baseline=%s head=%s\n' "$name" "$base_sum" "$head_sum"
            drift=1
        fi
    done
done

echo
if [ "$drift" -eq 0 ]; then
    echo "WEB_UPDATE_SUPPORTED=true"
    echo "Bootstrap binaries are unchanged since $BASELINE_REF, so an appliance running"
    echo "that baseline can activate this build as an ordinary A/B web update."
    exit 0
fi

echo "WEB_UPDATE_SUPPORTED=false"
echo "Bootstrap binaries changed since $BASELINE_REF. router-update will refuse to"
echo "activate this build over that baseline, by design: these binaries run outside"
echo "the A/B slot and survive rollback, so they are only replaced by the full signed"
echo "installer."
echo

ACKNOWLEDGED="$(json_string_field bootstrap_change_acknowledged_version "$BASELINE_FILE")"
CURRENT_VERSION="$(tr -d '\r\n' < VERSION 2>/dev/null || echo '')"
if [ -n "$ACKNOWLEDGED" ] && [ "$ACKNOWLEDGED" = "$CURRENT_VERSION" ]; then
    echo "This change is recorded in $BASELINE_FILE for version $ACKNOWLEDGED:"
    json_string_field bootstrap_change_reason "$BASELINE_FILE"
    echo
    echo "Existing appliances must therefore take this release through the full signed"
    echo "distribution installer once. Releases after it become web-updatable again."
    exit 0
fi

echo "This change is NOT recorded. Update $BASELINE_FILE in this pull request:"
echo "  bootstrap_change_acknowledged_version -> $CURRENT_VERSION"
echo "  bootstrap_change_reason               -> why bootstrap code had to change"
echo "and document the one-time full-installer step for existing appliances."
echo "Do not relax the byte-identity check in cmd/router-update to make this pass."
exit 1
