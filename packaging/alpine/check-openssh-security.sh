#!/bin/sh
# The Golden appliance ships Alpine's openssh-server, built for musl without
# PAM. Before 10.3_p1-r2 its sshd accepted any 257-1024 byte password for an
# account whose shadow field is `*` once another account had a $5$/$6$ hash
# (fixed upstream in OpenSSH 10.6p2). Fail closed on another upstream series
# until its security floor has been reviewed explicitly.
# https://alpinelinux.org/posts/2026-11-05-openssh-password-authentication-bypass/
set -eu
export LC_ALL=C

fail() { echo "ERROR: openssh security check: $*" >&2; exit 1; }
[ "$#" -eq 2 ] || fail "usage: $0 --version APK_VERSION | --db APK_INSTALLED_DB"
case "$1" in
    --version) version=$2 ;;
    --db)
        [ -s "$2" ] || fail "apk installed database is missing or empty: $2"
        # Blank-line separated records: P: names a package, V: its version.
        # Exactly one openssh-server record may exist.
        version=$(awk '
            function flush() { if (p == "openssh-server") { print v; n++ } p = ""; v = "" }
            /^$/ { flush(); next }
            /^P:/ { p = substr($0, 3) }
            /^V:/ { v = substr($0, 3) }
            END { flush(); if (n != 1) exit 1 }' "$2") ||
            fail "expected exactly one openssh-server package in $2"
        ;;
    *) fail "unknown mode: $1" ;;
esac
case "$version" in
    *[!0-9._pr-]*) fail "invalid characters in openssh-server version" ;;
esac
printf '%s\n' "$version" | grep -Eq '^10[.]3_p1-r(0|[1-9][0-9]*)$' ||
    fail "unsupported or malformed openssh-server version '$version'; expected Alpine 10.3_p1-rN"
release=${version##*-r}
[ "$release" -ge 2 ] 2>/dev/null ||
    fail "'$version' is below the musl password-bypass security floor 10.3_p1-r2"
printf '%s\n' "$version"
