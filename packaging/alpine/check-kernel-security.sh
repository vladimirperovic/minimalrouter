#!/bin/sh
# The Golden appliance currently uses Alpine's 6.18 linux-lts series.
# CVE-2026-98432 is fixed upstream in 6.18.56. Fail closed on another
# series/flavour until its security floor has been reviewed explicitly.
# https://www.cve.org/CVERecord?id=CVE-2026-98432
set -eu
export LC_ALL=C

fail() { echo "ERROR: kernel security check: $*" >&2; exit 1; }
[ "$#" -eq 2 ] || fail "usage: $0 --release RELEASE | --image VMLINUZ"
case "$1" in
    --release) release=$2 ;;
    --image)
        [ -s "$2" ] || fail "kernel image is missing or empty: $2"
        # Read the x86 boot header in the actual kernel, not the APK filename
        # or a free-floating string that could describe a different image.
        description=$(file -b -- "$2") || fail "cannot identify kernel image: $2"
        release=$(printf '%s\n' "$description" | sed -nE 's/^Linux kernel x86 boot executable,? bzImage, version ([^ ,]+).*/\1/p')
        [ -n "$release" ] || fail "unrecognized x86 bzImage: $2"
        ;;
    *) fail "unknown mode: $1" ;;
esac
case "$release" in
    *[!0-9.lts-]*) fail "invalid characters in kernel release" ;;
esac
printf '%s\n' "$release" | grep -Eq '^6[.]18[.](0|[1-9][0-9]*)-(0|[1-9][0-9]*)-lts$' ||
    fail "unsupported or malformed release '$release'; expected Alpine 6.18 linux-lts"
patch=${release#6.18.}
patch=${patch%%-*}
[ "$patch" -ge 56 ] 2>/dev/null ||
    fail "'$release' is below the CVE-2026-98432 security floor 6.18.56"
printf '%s\n' "$release"
