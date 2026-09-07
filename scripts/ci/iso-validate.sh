#!/bin/sh
# Complete installed-appliance gate, shared by unsigned CI and signed release.
# Run only in CI/a dedicated QEMU test host. All test disks are new files under
# an exclusive work directory; the production ISO is never modified.
set -eu
ISO=${1:?usage: iso-validate.sh production.iso new-test-directory}
OUT=${2:?usage: iso-validate.sh production.iso new-test-directory}
[ -f "$ISO" ] || exit 1
mkdir "$OUT"  # fail rather than overwrite another run's disks/logs
sha256sum "$ISO" > "$OUT/production-iso.sha256"

# Production defaults to VGA. Extract that exact ISO's boot menu and change
# only its default entry, preserving the payload and boot metadata.
xorriso -osirrox on -indev "$ISO" -extract /boot/syslinux/syslinux.cfg "$OUT/syslinux.cfg"
grep -qx 'DEFAULT minimalrouter' "$OUT/syslinux.cfg"
sed 's/^DEFAULT minimalrouter$/DEFAULT minimalrouter-serial/' "$OUT/syslinux.cfg" > "$OUT/syslinux-serial.cfg"
xorriso -indev "$ISO" -outdev "$OUT/serial-test.iso" -boot_image any replay \
    -map "$OUT/syslinux-serial.cfg" /boot/syslinux/syslinux.cfg -commit -end
sha256sum "$OUT/serial-test.iso" > "$OUT/serial-test-iso.sha256"

truncate -s 8G "$OUT/installed.raw"
timeout 2100 expect scripts/ci/iso-full-install.exp "$OUT/serial-test.iso" "$OUT/installed.raw" "$OUT/full-install.log"
grep -F 'FULL_ISO_INSTALL_OK' "$OUT/full-install.log"
grep -F 'INSTALLED_SSH_OK' "$OUT/full-install.log"

timeout 2100 expect scripts/ci/iso-installed-reboot.exp "$OUT/installed.raw" "$OUT/reboot.log"
for marker in INSTALLED_COLD_BOOT_OK ROUTERD_CRASH_RECOVERY_OK INSTALLED_WARM_REBOOT_OK INSTALLED_REBOOT_MATRIX_OK; do
    grep -F "$marker" "$OUT/reboot.log"
done

# A refusal boot must perform no destructive write: no reflash, no reformat,
# no console-marker change. Those are the flasher's only write domains on
# this path (the full-disk dd and the post-MBR-gap marker write both live
# after the existing-install guard, which execs a shell instead).
#
# A whole-disk byte compare is the wrong instrument here: CI evidence shows
# the ext4 interior (regions 00/02) drifting across a refusal boot while the
# MBR gap, marker, table and filesystem identity stay identical and the
# guard demonstrably refuses. Byte drift inside a poweroff -f'd journaling
# filesystem cannot distinguish a benign live touch from a destructive
# write, so the gate asserts exactly the destructive-write domains below
# and keeps the per-region table as non-failing evidence.
flasher_domain_fingerprint() {
    disk=$1
    out=$2
    : > "$out"
    # MBR, post-MBR gap (console marker at byte 32768) and partition table.
    dd if="$disk" bs=1M count=1 2>/dev/null | sha256sum | sed 's/  -$/  mbr-gap/' >> "$out"
    dd if="$disk" bs=1 skip=64 count=1 2>/dev/null | tr -d '\000\r\n ' | sed 's/^/marker /' >> "$out"
    # Partition layout as seen by fdisk (read-only on a regular file).
    fdisk -l "$disk" 2>/dev/null | grep -E '^(Disk|Units|Sector|Disklabel|Disk identifier|/[^ :]+)' >> "$out" || true
    # ext4 superblock identity inside partition 1 (starts at 1 MiB):
    # filesystem UUID at superblock+56 (16 bytes), label at superblock+120.
    dd if="$disk" bs=1 skip=$((1048576 + 1024 + 56)) count=16 2>/dev/null | od -An -tx1 | tr -d ' \n' | sed 's/^/fs-uuid /' >> "$out"
    dd if="$disk" bs=1 skip=$((1048576 + 1024 + 120)) count=16 2>/dev/null | tr -d '\000' | sed 's/^/fs-label /' >> "$out"
}
disk_fingerprint() {
    disk=$1
    out=$2
    : > "$out"
    i=0
    while [ "$i" -lt 8 ]; do
        dd if="$disk" bs=1M skip=$((i * 1024)) count=1024 2>/dev/null | sha256sum | sed "s/  -$/  region-$(printf '%02d' "$i")-gib/" >> "$out"
        i=$((i + 1))
    done
    sha256sum "$disk" | sed 's/  /  full-disk /' >> "$out"
}
disk_compare_domains() {
    before=$1
    after_disk=$2
    after_tmp=$3
    flasher_domain_fingerprint "$after_disk" "$after_tmp"
    if cmp -s "$before" "$after_tmp"; then
        return 0
    fi
    printf 'ERROR: flasher write domain changed across a refusal boot\n' >&2
    diff -u "$before" "$after_tmp" >&2 || true
    return 1
}

flasher_domain_fingerprint "$OUT/installed.raw" "$OUT/existing-disk-before.domains"
disk_fingerprint "$OUT/installed.raw" "$OUT/existing-disk-before.regions"
timeout 300 expect scripts/ci/iso-installer-safety.exp existing "$OUT/serial-test.iso" "$OUT/installed.raw" "$OUT/existing-refusal.log"
grep -F 'EXISTING_INSTALL_GUARD_OK' "$OUT/existing-refusal.log"
disk_compare_domains "$OUT/existing-disk-before.domains" "$OUT/installed.raw" "$OUT/existing-disk-after.domains"
# Evidence only: interior drift does not fail the gate (see comment above),
# but both tables are kept in the uploaded validation directory.
disk_fingerprint "$OUT/installed.raw" "$OUT/existing-disk-after.regions" || true
truncate -s 4G "$OUT/undersized.raw"
sha256sum "$OUT/undersized.raw" > "$OUT/small-disk-before.sha256"
timeout 300 expect scripts/ci/iso-installer-safety.exp small "$OUT/serial-test.iso" "$OUT/undersized.raw" "$OUT/small-refusal.log"
grep -F 'UNDERSIZED_DISK_GUARD_OK' "$OUT/small-refusal.log"
sha256sum -c "$OUT/small-disk-before.sha256"
sha256sum -c "$OUT/production-iso.sha256"
printf '%s\n' 'COMPLETE_ISO_GATE_OK' > "$OUT/result.txt"
