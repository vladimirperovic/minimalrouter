#!/bin/sh
set -eu

# LAB scenario 24: signed A/B update lifecycle.
#
# The lab runner prepares and signs the payload. This script deliberately never
# generates or accepts a private signing key. It installs only the public trust
# anchor, stages the signed release, activates it, verifies the selected slot,
# rolls back, and proves that the original current slot was restored.
#
# Expected default payload layout:
#   /root/lab-update/
#     manifest.json
#     firmware-signing.pub
#     dist/minimalrouter-linux-amd64/...
#
# Usage:
#   sudo sh scripts/lab-scenario-24-signed-update.sh /root/lab-update
#
# Optional environment variables:
#   LAB24_VERSION=9.9.9
#   LAB24_RELEASE_DIR=/root/lab-update/dist/minimalrouter-linux-amd64
#   LAB24_PUBLIC_KEY=/root/lab-update/firmware-signing.pub
#   LAB24_MANIFEST=/root/lab-update/manifest.json
#   LAB24_UPDATE_ROOT=/var/lib/minimalrouter-update
#   LAB24_REQUIRE_SIGNED=1   # fail instead of SKIP when signed inputs are absent

PAYLOAD_DIR=${1:-${LAB24_PAYLOAD_DIR:-/root/lab-update}}
UPDATE_ROOT=${LAB24_UPDATE_ROOT:-/var/lib/minimalrouter-update}
MANIFEST=${LAB24_MANIFEST:-$PAYLOAD_DIR/manifest.json}
PUBLIC_KEY=${LAB24_PUBLIC_KEY:-$PAYLOAD_DIR/firmware-signing.pub}
PINNED_KEY=/etc/minimalrouter/firmware-signing.pub
REQUIRE_SIGNED=${LAB24_REQUIRE_SIGNED:-0}

if [ -n "${LAB24_RELEASE_DIR:-}" ]; then
    RELEASE_DIR=$LAB24_RELEASE_DIR
elif [ -d "$PAYLOAD_DIR/dist/minimalrouter-linux-amd64" ]; then
    RELEASE_DIR=$PAYLOAD_DIR/dist/minimalrouter-linux-amd64
else
    RELEASE_DIR=$PAYLOAD_DIR
fi

skip_or_fail() {
    message=$1
    if [ "$REQUIRE_SIGNED" = "1" ]; then
        echo "LAB-24 FAIL: $message" >&2
        exit 1
    fi
    echo "LAB-24 SKIP: $message"
    exit 0
}

[ "$(id -u)" -eq 0 ] || {
    echo "LAB-24 FAIL: scenario must run as root" >&2
    exit 1
}

command -v router-update >/dev/null 2>&1 || {
    echo "LAB-24 FAIL: router-update is not installed" >&2
    exit 1
}

[ -f "$MANIFEST" ] || skip_or_fail "signed manifest not prepared: $MANIFEST"
[ -f "$PUBLIC_KEY" ] || skip_or_fail "firmware public key not prepared: $PUBLIC_KEY"
[ -d "$RELEASE_DIR" ] || skip_or_fail "signed release directory not prepared: $RELEASE_DIR"

VERSION=${LAB24_VERSION:-}
if [ -z "$VERSION" ]; then
    VERSION=$(sed -n 's/.*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$MANIFEST" | head -n 1)
fi
[ -n "$VERSION" ] || {
    echo "LAB-24 FAIL: could not determine version from manifest" >&2
    exit 1
}

mkdir -p /etc/minimalrouter
cp "$PUBLIC_KEY" "$PINNED_KEY.tmp"
chmod 0644 "$PINNED_KEY.tmp"
mv -f "$PINNED_KEY.tmp" "$PINNED_KEY"

current_target() {
    readlink "$UPDATE_ROOT/current" 2>/dev/null || true
}

current_version() {
    target=$(current_target)
    [ -n "$target" ] || return 0
    basename "$target"
}

INITIAL_CURRENT=$(current_target)
INITIAL_VERSION=$(current_version)

if [ -z "$INITIAL_CURRENT" ]; then
    echo "LAB-24 FAIL: update baseline has no current slot" >&2
    exit 1
fi

printf '%s\n' "[24.1] initial update status"
router-update status
printf '%s\n' "[24.2] pinned firmware trust key: $PINNED_KEY"
printf '%s\n' "[24.3] signed release version: $VERSION"

# Use the real CLI. The obsolete `minimalrouter --version`, argument-less
# `activate`, and argument-less `rollback` forms are intentionally not used.
printf '%s\n' "[24.4] stage signed release"
router-update stage --dir "$RELEASE_DIR" --manifest "$MANIFEST"

printf '%s\n' "[24.5] verify staged status"
STAGED_STATUS=$(router-update status)
printf '%s\n' "$STAGED_STATUS"
printf '%s\n' "$STAGED_STATUS" | grep -F '"'"$VERSION"'"' >/dev/null || {
    echo "LAB-24 FAIL: staged version $VERSION is absent from router-update status" >&2
    exit 1
}

printf '%s\n' "[24.6] activate $VERSION"
router-update activate --version "$VERSION" --confirm ACTIVATE-UPDATE

ACTIVE_VERSION=$(current_version)
[ "$ACTIVE_VERSION" = "$VERSION" ] || {
    echo "LAB-24 FAIL: current slot after activation is $ACTIVE_VERSION, expected $VERSION" >&2
    router-update status >&2 || true
    exit 1
}

printf '%s\n' "[24.7] status after activation"
router-update status

printf '%s\n' "[24.8] explicit rollback"
router-update rollback --confirm ROLLBACK-UPDATE

FINAL_CURRENT=$(current_target)
FINAL_VERSION=$(current_version)
if [ "$FINAL_CURRENT" != "$INITIAL_CURRENT" ]; then
    echo "LAB-24 FAIL: rollback did not restore original current slot" >&2
    echo "  before: $INITIAL_CURRENT ($INITIAL_VERSION)" >&2
    echo "  after:  $FINAL_CURRENT ($FINAL_VERSION)" >&2
    router-update status >&2 || true
    exit 1
fi

printf '%s\n' "[24.9] final status"
router-update status
printf '%s\n' "LAB-24 PASS: signed $VERSION staged and activated; rollback restored $INITIAL_VERSION"
