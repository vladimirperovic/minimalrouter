#!/bin/sh
set -eu
REPO=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
CHECK="$REPO/packaging/alpine/check-kernel-security.sh"
WORK=$(mktemp -d "${TMPDIR:-/tmp}/minimalrouter-kernel-test.XXXXXX")
trap 'rm -rf "$WORK"' EXIT HUP INT TERM

reject() {
    if sh "$CHECK" "$@" > "$WORK/output" 2>&1; then
        echo "FAIL accepted unsafe kernel: $*" >&2
        exit 1
    fi
}
for release in 6.18.56-0-lts 6.18.59-0-lts 6.18.100-12-lts; do
    [ "$(sh "$CHECK" --release "$release")" = "$release" ]
done
for release in '' 6.18.0-0-lts 6.18.55-99-lts 6.12.114-0-lts 6.19.0-0-lts \
    6.18.56 6.18.56-r0 6.18.56-0-virt 6.18.56-rc1-0-lts 6.18.056-0-lts \
    6.18.56-0-lts-extra '6.18.56-0-lts anything'; do
    reject --release "$release"
done
# A second line must not let a valid line hide malformed input.
reject --release "$(printf '6.18.56-0-lts\n6.18.55-0-lts')"
reject --image "$WORK/missing"
printf '6.18.59-0-lts\n' > "$WORK/not-a-kernel"
reject --image "$WORK/not-a-kernel"

# Minimal real x86 boot headers exercise the system file parser without
# downloading or committing a kernel. See docs.kernel.org/arch/x86/boot.html.
fixture() {
    dd if=/dev/zero of="$WORK/vmlinuz" bs=4096 count=1 2>/dev/null
    printf '\125\252' | dd of="$WORK/vmlinuz" bs=1 seek=510 conv=notrunc 2>/dev/null
    printf 'HdrS' | dd of="$WORK/vmlinuz" bs=1 seek=514 conv=notrunc 2>/dev/null
    printf '\017\002' | dd of="$WORK/vmlinuz" bs=1 seek=518 conv=notrunc 2>/dev/null
    printf '\000\002' | dd of="$WORK/vmlinuz" bs=1 seek=526 conv=notrunc 2>/dev/null
    printf '\001' | dd of="$WORK/vmlinuz" bs=1 seek=529 conv=notrunc 2>/dev/null
    printf '%s (fixture) #1-Alpine SMP\000' "$1" | dd of="$WORK/vmlinuz" bs=1 seek=1024 conv=notrunc 2>/dev/null
}
fixture 6.18.59-0-lts
[ "$(sh "$CHECK" --image "$WORK/vmlinuz")" = 6.18.59-0-lts ]
fixture 6.18.55-0-lts
# A safe-looking string elsewhere must not override the unsafe boot header.
printf '6.18.59-0-lts\000' | dd of="$WORK/vmlinuz" bs=1 seek=2048 conv=notrunc 2>/dev/null
reject --image "$WORK/vmlinuz"
fixture 6.18.56-rc1-0-lts
reject --image "$WORK/vmlinuz"
echo 'PASS kernel security floor, unknown/malformed releases and actual x86 boot headers'
