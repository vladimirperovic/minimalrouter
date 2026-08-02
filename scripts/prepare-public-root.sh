#!/bin/sh
# Build a one-commit public-release candidate from a reviewed source ref.
#
# This script is deliberately local-only: it does not add a remote, push,
# rename a GitHub repository, change visibility, or publish an artifact.
set -eu

SOURCE_REF="${1:-HEAD}"
DESTINATION="${2:-/tmp/minimalrouter-public-root}"
GITLEAKS_IMAGE="ghcr.io/gitleaks/gitleaks:v8.30.1"

fail() {
    printf 'ERROR: %s\n' "$*" >&2
    exit 1
}

ROOT="$(git rev-parse --show-toplevel 2>/dev/null)" || fail "run this inside the Minimal Router Git checkout"
cd "$ROOT"

case "$DESTINATION" in
    ""|/|"$ROOT") fail "unsafe destination: $DESTINATION" ;;
esac

[ ! -e "$DESTINATION" ] || fail "destination already exists: $DESTINATION"
git rev-parse --verify "${SOURCE_REF}^{commit}" >/dev/null 2>&1 || fail "source ref does not exist: $SOURCE_REF"

AUTHOR_NAME="$(git config user.name || true)"
AUTHOR_EMAIL="$(git config user.email || true)"
[ -n "$AUTHOR_NAME" ] || fail "configure Git author name first: git config --global user.name 'Your Name'"
[ -n "$AUTHOR_EMAIL" ] || fail "configure Git author email first: git config --global user.email 'you@example.com'"

mkdir -p "$DESTINATION"
git archive --format=tar "$SOURCE_REF" | tar -xf - -C "$DESTINATION"

# This staging-only guard document belongs to the private preparation branch,
# not to the public source tree.
rm -f "$DESTINATION/PUBLIC_RELEASE_CHECKLIST.md"

for forbidden in \
    AI_HANDOFF.md \
    SESSION6_SUMMARY.md \
    implementation_plan.md \
    data/current_config.json \
    data/snapshots

do
    [ ! -e "$DESTINATION/$forbidden" ] || fail "forbidden private/runtime path was exported: $forbidden"
done

SUSPICIOUS_FILES="$({
    find "$DESTINATION" -type f \( \
        -name '.env' -o \
        -name '.env.*' -o \
        -name '*.db' -o \
        -name '*.db-shm' -o \
        -name '*.db-wal' -o \
        -name '*.sqlite' -o \
        -name '*.sqlite3' -o \
        -name '*.pem' -o \
        -name '*.key' -o \
        -name '*.p12' -o \
        -name '*.pfx' -o \
        -name '*.age' -o \
        -name '*.backup' -o \
        -name '*.pcap' -o \
        -name '*.pcapng' -o \
        -name '*.iso' -o \
        -name '*.img' -o \
        -name '*.qcow2' -o \
        -name '*.vmdk' \
    \) ! -name '.env.example' -print
} | sort)"
[ -z "$SUSPICIOUS_FILES" ] || fail "suspicious files found:\n$SUSPICIOUS_FILES"

git -C "$DESTINATION" init -b main
git -C "$DESTINATION" config user.name "$AUTHOR_NAME"
git -C "$DESTINATION" config user.email "$AUTHOR_EMAIL"
git -C "$DESTINATION" add -A
git -C "$DESTINATION" commit -m "Initial public release"

COMMIT_COUNT="$(git -C "$DESTINATION" rev-list --all --count)"
[ "$COMMIT_COUNT" = "1" ] || fail "expected exactly one commit, found $COMMIT_COUNT"
[ -z "$(git -C "$DESTINATION" tag -l)" ] || fail "unexpected tags exist in release candidate"
[ -z "$(git -C "$DESTINATION" remote)" ] || fail "release candidate unexpectedly has a remote"
git -C "$DESTINATION" fsck --full

if command -v gitleaks >/dev/null 2>&1; then
    gitleaks git "$DESTINATION" --redact --no-banner --verbose
elif command -v docker >/dev/null 2>&1; then
    docker run --rm \
        -v "$DESTINATION:/repo" \
        "$GITLEAKS_IMAGE" \
        git /repo --redact --no-banner --verbose
else
    fail "install gitleaks or Docker, then rerun; a full-history secret scan is mandatory"
fi

printf '\nPublic-root candidate prepared successfully.\n'
printf 'Directory: %s\n' "$DESTINATION"
printf 'Commit:    %s\n' "$(git -C "$DESTINATION" rev-parse HEAD)"
printf 'Tree:      %s\n' "$(git -C "$DESTINATION" rev-parse HEAD^{tree})"
printf 'Remotes:   none\n'
printf '\nNothing was pushed or published. Continue only during the owner-reviewed release session.\n'
