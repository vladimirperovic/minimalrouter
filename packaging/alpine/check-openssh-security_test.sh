#!/bin/sh
set -eu
REPO=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
CHECK="$REPO/packaging/alpine/check-openssh-security.sh"
WORK=$(mktemp -d "${TMPDIR:-/tmp}/minimalrouter-openssh-test.XXXXXX")
trap 'rm -rf "$WORK"' EXIT HUP INT TERM

reject() {
    if sh "$CHECK" "$@" > "$WORK/output" 2>&1; then
        echo "FAIL accepted unsafe openssh-server: $*" >&2
        exit 1
    fi
}
for version in 10.3_p1-r2 10.3_p1-r3 10.3_p1-r10; do
    [ "$(sh "$CHECK" --version "$version")" = "$version" ]
done
for version in '' 10.3_p1-r0 10.3_p1-r1 10.3_p1-r01 10.3_p1 10.3p1-r2 \
    10.2_p1-r1 10.6_p2-r0 10.3_p1-r2-extra '10.3_p1-r2 anything'; do
    reject --version "$version"
done
# A second line must not let a valid line hide a vulnerable one.
reject --version "$(printf '10.3_p1-r2\n10.3_p1-r1')"

record() {
    printf 'C:Q1synthetic=\nP:%s\nV:%s\nA:x86_64\n\n' "$1" "$2"
}
{ record openssh-keygen 10.3_p1-r1; record openssh-server 10.3_p1-r2; record openssh-server-common 10.3_p1-r1; } > "$WORK/installed"
[ "$(sh "$CHECK" --db "$WORK/installed")" = 10.3_p1-r2 ]
# The last record needs no trailing blank line.
{ record musl 1.2.5-r10; printf 'P:openssh-server\nV:10.3_p1-r2\n'; } > "$WORK/installed"
[ "$(sh "$CHECK" --db "$WORK/installed")" = 10.3_p1-r2 ]
{ record openssh-server 10.3_p1-r1; } > "$WORK/installed"
reject --db "$WORK/installed"
# A package whose name only contains openssh-server is not the server.
{ record openssh-server-pam 10.3_p1-r2; } > "$WORK/installed"
reject --db "$WORK/installed"
{ record openssh-server 10.3_p1-r2; record openssh-server 10.3_p1-r1; } > "$WORK/installed"
reject --db "$WORK/installed"
: > "$WORK/empty"
reject --db "$WORK/empty"
reject --db "$WORK/missing"
reject --unknown 10.3_p1-r2
echo 'PASS openssh-server security floor, unknown versions and the apk installed database'
