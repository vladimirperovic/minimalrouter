#!/bin/sh
# Removes kernel subsystems a headless router never loads from an image root.
#
# Usage: prune-kernel-modules.sh ROOT REQUIRED_LIST...
#
# Runs in the CI image builder after the kernel package is installed (it needs
# kmod's depmod and modprobe there). It refuses to remove a module that a kept
# module depends on, rebuilds the module index, and fails unless every module
# named in the REQUIRED_LIST files still resolves with all its dependencies.
# The caller then rebuilds the initramfs from the same tree, so kernel,
# initramfs and /lib/modules stay one validated set (docs/GOLDEN-IMAGE.md).
set -eu

[ "$#" -ge 2 ] || { echo "usage: $0 ROOT REQUIRED_LIST..." >&2; exit 2; }
ROOT="$1"
shift

trees=$(find "$ROOT/lib/modules" -mindepth 1 -maxdepth 1 -type d)
[ "$(printf '%s\n' "$trees" | grep -c .)" -eq 1 ] || {
    echo "ERROR: expected exactly one kernel module tree under $ROOT/lib/modules" >&2
    exit 1
}
MODDIR="$trees"
RELEASE=$(basename "$MODDIR")
[ -s "$MODDIR/modules.dep" ] || { echo "ERROR: $MODDIR/modules.dep is missing" >&2; exit 1; }

# Whole subsystems with no role on a headless router appliance. Every
# Ethernet and Wi-Fi driver, virtio, Hyper-V, storage, USB, input, netfilter,
# traffic control, crypto and the filesystems the appliance uses stay.
# The Golden disk boots through SYSLINUX on BIOS, where the console is the
# built-in VGA text console, so the large vendor GPU drivers are not needed.
PRUNE="
kernel/sound
kernel/drivers/media
kernel/drivers/staging
kernel/drivers/infiniband
kernel/drivers/bluetooth
kernel/net/bluetooth
kernel/drivers/nfc
kernel/net/nfc
kernel/drivers/isdn
kernel/drivers/iio
kernel/drivers/input/joystick
kernel/drivers/input/tablet
kernel/drivers/input/touchscreen
kernel/drivers/gpu/drm/amd
kernel/drivers/gpu/drm/radeon
kernel/drivers/gpu/drm/nouveau
kernel/drivers/gpu/drm/i915
kernel/drivers/gpu/drm/xe
kernel/arch/x86/kvm
kernel/fs/btrfs
kernel/fs/xfs
kernel/fs/ocfs2
kernel/fs/gfs2
kernel/fs/jfs
kernel/fs/nilfs2
kernel/fs/f2fs
kernel/fs/ceph
kernel/fs/smb
kernel/fs/nfs
kernel/fs/nfsd
"

before=$(du -sk "$MODDIR" | cut -f1)

removal=$(mktemp)
trap 'rm -f "$removal"' EXIT HUP INT TERM
for path in $PRUNE; do
    [ -e "$MODDIR/$path" ] || continue
    (cd "$MODDIR" && find "$path" -type f -name '*.ko*')
done | sort -u > "$removal"

# A kept module that needs a pruned one would fail to load at runtime.
if ! awk -v removal="$removal" '
    BEGIN { while ((getline line < removal) > 0) gone[line] = 1 }
    {
        target = $1
        sub(/:$/, "", target)
        if (target in gone) next
        for (i = 2; i <= NF; i++) {
            if ($i in gone) {
                print "ERROR: kept module " target " depends on pruned " $i > "/dev/stderr"
                bad = 1
            }
        }
    }
    END { exit bad }
' "$MODDIR/modules.dep"; then
    exit 1
fi

for path in $PRUNE; do
    rm -rf "${MODDIR:?}/$path"
done
depmod -b "$ROOT" "$RELEASE"

missing=0
for list in "$@"; do
    [ -r "$list" ] || { echo "ERROR: required module list $list is unreadable" >&2; exit 1; }
    for module in $(sed -e 's/#.*//' "$list"); do
        if ! modprobe -d "$ROOT" -S "$RELEASE" --show-depends "$module" >/dev/null 2>&1; then
            echo "ERROR: required kernel module $module is unavailable after pruning" >&2
            missing=1
        fi
    done
done
[ "$missing" -eq 0 ] || exit 1

after=$(du -sk "$MODDIR" | cut -f1)
echo "Kernel modules for $RELEASE: removed $(grep -c . "$removal") files, ${before} KiB -> ${after} KiB"
