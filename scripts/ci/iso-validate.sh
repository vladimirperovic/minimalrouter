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

# A refusal boot must not write the target disk at all. A single full-disk
# hash proves that but says nothing about WHERE a violation wrote. Record
# per-region hashes so a mismatch localizes the writer (flasher write
# domains are the MBR post-gap marker at offset 32768 and, on install only,
# the whole disk). The gate still fails on any difference; the table only
# diagnoses it.
disk_fingerprint() {
    disk=$1
    out=$2
    : > "$out"
    # Region 0 covers MBR, the post-MBR gap marker and the partition table.
    dd if="$disk" bs=1M count=1 2>/dev/null | sha256sum | sed 's/  -$/  region-00-mbr-gap/' >> "$out"
    i=0
    while [ "$i" -lt 8 ]; do
        dd if="$disk" bs=1M skip=$((i * 1024)) count=1024 2>/dev/null | sha256sum | sed "s/  -$/  region-$(printf '%02d' "$i")-gib/" >> "$out"
        i=$((i + 1))
    done
    sha256sum "$disk" | sed 's/  /  full-disk /' >> "$out"
}
disk_compare() {
    before=$1
    after_disk=$2
    after_tmp=$3
    disk_fingerprint "$after_disk" "$after_tmp"
    if cmp -s "$before" "$after_tmp"; then
        return 0
    fi
    printf 'ERROR: target disk changed across a refusal boot; refusal path must be read-only\n' >&2
    printf '%-22s %-64s %-64s\n' "region" "before" "after" >&2
    join -j 2 -o 0,1.1,2.1 "$before" "$after_tmp" 2>/dev/null | while read -r region b a; do
        if [ "$b" != "$a" ]; then
            printf '%-22s %s %s  CHANGED\n' "$region" "$b" "$a" >&2
        fi
    done
    return 1
}

sha256sum "$OUT/installed.raw" > "$OUT/existing-disk-before.sha256"
disk_fingerprint "$OUT/installed.raw" "$OUT/existing-disk-before.regions"
timeout 300 expect scripts/ci/iso-installer-safety.exp existing "$OUT/serial-test.iso" "$OUT/installed.raw" "$OUT/existing-refusal.log"
grep -F 'EXISTING_INSTALL_GUARD_OK' "$OUT/existing-refusal.log"
sha256sum -c "$OUT/existing-disk-before.sha256"
disk_compare "$OUT/existing-disk-before.regions" "$OUT/installed.raw" "$OUT/existing-disk-after.regions"
truncate -s 4G "$OUT/undersized.raw"
sha256sum "$OUT/undersized.raw" > "$OUT/small-disk-before.sha256"
timeout 300 expect scripts/ci/iso-installer-safety.exp small "$OUT/serial-test.iso" "$OUT/undersized.raw" "$OUT/small-refusal.log"
grep -F 'UNDERSIZED_DISK_GUARD_OK' "$OUT/small-refusal.log"
sha256sum -c "$OUT/small-disk-before.sha256"
sha256sum -c "$OUT/production-iso.sha256"
printf '%s\n' 'COMPLETE_ISO_GATE_OK' > "$OUT/result.txt"
