#!/bin/sh
# MinimalRouter live ISO flasher.
# The live system does not install packages, resolve dependencies, build an
# initramfs or run MinimalRouter installers. It verifies and writes one CI-built
# bootable disk image, then reboots into that image's first-boot wizard.
set -eu
umask 077

log() { printf '%s\n' "$*"; }
fail() {
    printf '\nERROR: %s\n' "$*" >&2
    printf 'No further disk changes will be made.\n' >&2
    printf 'A recovery shell will open on this console. Type exit to stop.\n\n' >&2
    exec /bin/sh
}

find_media() {
    for root in /media/* /mnt/* /run/media/*; do
        [ -d "$root/minimalrouter" ] || continue
        [ -s "$root/minimalrouter/golden.img.gz" ] || continue
        printf '%s\n' "$root"
        return 0
    done
    return 1
}

wait_for_media() {
    attempts=0
    while [ "$attempts" -lt 30 ]; do
        if media="$(find_media 2>/dev/null)"; then
            printf '%s\n' "$media"
            return 0
        fi
        attempts=$((attempts + 1))
        sleep 1
    done
    return 1
}

is_qemu_vm() {
    for f in /sys/class/dmi/id/sys_vendor /sys/class/dmi/id/product_name /sys/class/dmi/id/board_vendor; do
        [ -r "$f" ] || continue
        grep -Eiq 'qemu|kvm|proxmox' "$f" 2>/dev/null && return 0
    done
    return 1
}

list_candidate_disks() {
    for sys in /sys/block/*; do
        [ -e "$sys" ] || continue
        name="${sys##*/}"
        case "$name" in
            loop*|ram*|sr*|fd*|dm-*|md*|zram*) continue ;;
        esac
        [ -b "/dev/$name" ] || continue
        [ "$(cat "$sys/removable" 2>/dev/null || printf 1)" = "0" ] || continue
        printf '/dev/%s\n' "$name"
    done
}

disk_sys_name() {
    printf '%s\n' "${1#/dev/}"
}

disk_size_bytes() {
    name="$(disk_sys_name "$1")"
    sectors="$(cat "/sys/block/$name/size" 2>/dev/null || true)"
    [ -n "$sectors" ] || return 1
    printf '%s\n' "$((sectors * 512))"
}

show_disks() {
    printf '\nAvailable installation disks\n'
    printf '%s\n' '----------------------------'
    for disk in $CANDIDATES; do
        name="$(disk_sys_name "$disk")"
        bytes="$(disk_size_bytes "$disk" 2>/dev/null || printf 0)"
        gib="$((bytes / 1024 / 1024 / 1024))"
        model="$(tr -d '\000\r\n' < "/sys/block/$name/device/model" 2>/dev/null || true)"
        printf '  %-14s %s GiB  %s\n' "$disk" "$gib" "$model"
    done
    printf '\n'
}

safe_auto_vm_disk() {
    [ "$COUNT" -eq 1 ] || return 1
    is_qemu_vm || return 1
    disk="$(printf '%s\n' "$CANDIDATES" | awk 'NF {print; exit}')"
    [ -b "$disk" ] || return 1
    name="$(disk_sys_name "$disk")"
    case "$disk" in
        /dev/vd*) ;;
        /dev/sd*|/dev/nvme*)
            identity="$(cat "/sys/block/$name/device/vendor" "/sys/block/$name/device/model" 2>/dev/null || true)"
            printf '%s\n' "$identity" | grep -Eiq 'qemu|virtio|virtual' || return 1
            ;;
        *) return 1 ;;
    esac
    printf '%s\n' "$disk"
}

preflight_host() {
    mem_kib="$(awk '/^MemTotal:/ {print $2; exit}' /proc/meminfo 2>/dev/null || true)"
    [ -n "$mem_kib" ] || fail "Unable to determine VM memory"
    [ "$mem_kib" -ge 900000 ] || fail "This system has less than 1 GiB RAM. Increase the VM memory to at least 1 GiB"
}

validate_target_disk() {
    bytes="$(disk_size_bytes "$1" 2>/dev/null || true)"
    [ -n "$bytes" ] || fail "Unable to determine installation disk size: $1"
    min_bytes=8589934592
    if [ "$bytes" -lt "$min_bytes" ]; then
        gib="$((bytes / 1024 / 1024 / 1024))"
        fail "Installation disk $1 is only ${gib} GiB. Use an 8 GiB or larger VM disk"
    fi
}

partition_nodes() {
    disk="$1"
    name="$(disk_sys_name "$disk")"
    sys="/sys/block/$name"
    for part_sys in "$sys"/"$name"*; do
        [ -e "$part_sys" ] || continue
        part="${part_sys##*/}"
        [ "$part" = "$name" ] && continue
        [ -b "/dev/$part" ] && printf '/dev/%s\n' "$part"
    done
}

guard_existing_install() {
    check_dir=/tmp/minimalrouter-existing-check
    mkdir -p "$check_dir"
    for disk in $CANDIDATES; do
        for part in $(partition_nodes "$disk"); do
            umount "$check_dir" >/dev/null 2>&1 || true
            if mount -o ro "$part" "$check_dir" >/dev/null 2>&1; then
                if [ -f "$check_dir/etc/minimalrouter/installed" ] || [ -f "$check_dir/etc/minimalrouter/VERSION" ]; then
                    installed_version="$(cat "$check_dir/etc/minimalrouter/VERSION" 2>/dev/null | tr -d '\r\n' || true)"
                    umount "$check_dir" >/dev/null 2>&1 || true
                    printf '\nminimalrouter is already installed%s on %s.\n' "${installed_version:+ v$installed_version}" "$disk"
                    printf 'The ISO stopped before overwriting the appliance. Detach the ISO and reboot.\n\n'
                    exec /bin/sh
                fi
                umount "$check_dir" >/dev/null 2>&1 || true
            fi
        done
    done
}

verify_golden_image() {
    [ -s "$GOLDEN" ] || fail "Golden disk image is missing from the ISO"
    [ -r "$GOLDEN_SHA" ] || fail "Golden disk image checksum is missing from the ISO"
    if ! (cd "$ISO_ROOT" && sha256sum -c golden.img.gz.sha256) >/tmp/minimalrouter-golden-sha.log 2>&1; then
        cat /tmp/minimalrouter-golden-sha.log >&2 || true
        fail "Golden disk image checksum verification failed"
    fi
    gzip -t "$GOLDEN" >/dev/null 2>&1 || fail "Golden disk image is corrupt"
    printf '\033[32m●\033[0m Golden image verified (SHA256 + gzip).\n'
}

write_golden_image() {
    disk="$1"
    printf '\nWriting the prebuilt MinimalRouter appliance to %s...\n' "$disk"
    printf 'No packages are installed on this VM; this is a verified raw-image copy.\n\n'
    if ! gzip -dc "$GOLDEN" | dd of="$disk" bs=4M 2>/tmp/minimalrouter-dd.log; then
        cat /tmp/minimalrouter-dd.log >&2 || true
        fail "Could not write the golden image to $disk"
    fi

    # Preserve which console launched the flasher in the unused post-MBR gap.
    # firstboot reads this one tiny marker and presents itself on the same console.
    console=tty1
    [ "${MINIMALROUTER_INSTALL_TTY:-/dev/tty1}" = "/dev/ttyS0" ] && console=ttyS0
    printf '%s' "$console" | dd of="$disk" bs=1 seek=32768 conv=notrunc >/dev/null 2>&1 || fail "Could not persist console selection"
    sync
    printf '\033[32m●\033[0m Golden image copied successfully.\n'
}

MEDIA="$(wait_for_media)" || fail "MinimalRouter golden image was not found on the boot media"
ISO_ROOT="$MEDIA/minimalrouter"
GOLDEN="$ISO_ROOT/golden.img.gz"
GOLDEN_SHA="$ISO_ROOT/golden.img.gz.sha256"
VERSION="dev"
[ -r "$ISO_ROOT/VERSION" ] && VERSION="$(tr -d '\r\n' < "$ISO_ROOT/VERSION")"
[ -n "$VERSION" ] || VERSION="dev"

cat <<'ART'
+----------------------------------------------------------+
|                      minimalrouter                       |
+----------------------------------------------------------+
ART
printf '\nminimalrouter v%s appliance installer\n\n' "$VERSION"
printf 'This ISO is a flasher: Alpine Linux and MinimalRouter are already built and tested in CI.\n'
printf 'The VM will not run apk, setup-disk, mkinitfs or the application installer.\n\n'

preflight_host
verify_golden_image
CANDIDATES="$(list_candidate_disks)"
[ -n "$CANDIDATES" ] || fail "No writable installation disk was detected"
COUNT="$(printf '%s\n' "$CANDIDATES" | awk 'NF {n++} END {print n+0}')"
guard_existing_install

TARGET="$(safe_auto_vm_disk 2>/dev/null || true)"
if [ -n "$TARGET" ]; then
    printf '\nProxmox/QEMU VM detected.\n'
    printf 'Using the only attached installation disk automatically: %s\n' "$TARGET"
    printf 'Only a clearly virtual, non-removable disk is eligible for automatic erase.\n'
else
    show_disks
    while :; do
        printf 'Install minimalrouter v%s to disk: ' "$VERSION"
        IFS= read -r TARGET
        printf '%s\n' "$CANDIDATES" | grep -qxF "$TARGET" && break
        printf 'Please enter one of the exact disk paths shown above.\n'
    done
    printf '\nEvery partition and all data on %s will be erased.\n' "$TARGET"
    printf 'Type ERASE to continue: '
    IFS= read -r CONFIRM
    case "$CONFIRM" in ERASE) ;; *) fail "Disk installation was cancelled" ;; esac
fi

validate_target_disk "$TARGET"
write_golden_image "$TARGET"

cat <<'ART'

+----------------------------------------------------------+
|                      minimalrouter                       |
+----------------------------------------------------------+
ART
printf '\n\033[32m●\033[0m minimalrouter v%s is installed.\n' "$VERSION"
printf '\033[32m●\033[0m Rebooting into the prebuilt appliance now.\n\n'
printf 'On first boot you will set WAN/LAN, optional PPPoE, Dashboard admin password\n'
printf 'and the separate recovery/SSH password. No operating-system installation remains.\n\n'
sleep 2
reboot -f
