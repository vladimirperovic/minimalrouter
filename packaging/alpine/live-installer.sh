#!/bin/sh
# Minimal Router OS — live ISO installer.
# Runs from the Alpine live environment, verifies the router configuration first,
# then installs Alpine + the verified MinimalRouter state to the selected disk.
set -eu
umask 077

log() { printf '%s\n' "$*"; }
fail() {
    printf '\nERROR: %s\n' "$*" >&2
    printf 'A recovery shell will open on this console. Type exit to restart the installer.\n\n' >&2
    /bin/sh || true
    exit 1
}

find_media() {
    for root in /media/* /mnt/* /run/media/*; do
        [ -d "$root/minimalrouter" ] || continue
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

boot_disk_for_source() {
    src="$1"
    case "$src" in
        /dev/*)
            parent="$(lsblk -ndo PKNAME "$src" 2>/dev/null | head -n 1 || true)"
            if [ -n "$parent" ]; then
                printf '/dev/%s\n' "$parent"
            else
                printf '%s\n' "$src"
            fi
            ;;
        *) printf '\n' ;;
    esac
}

list_candidate_disks() {
    boot_disk="$1"
    lsblk -dpno NAME,TYPE 2>/dev/null | while read -r dev type; do
        [ "$type" = "disk" ] || continue
        case "$dev" in
            /dev/loop*|/dev/ram*|/dev/fd*|/dev/sr*) continue ;;
        esac
        [ -n "$boot_disk" ] && [ "$dev" = "$boot_disk" ] && continue
        printf '%s\n' "$dev"
    done
}

show_disk_table() {
    printf '\nAvailable installation disks\n'
    printf '%s\n' '----------------------------'
    lsblk -dpno NAME,SIZE,MODEL,TYPE,TRAN 2>/dev/null | awk '$4 == "disk" {print}' || true
    printf '\n'
}

mount_target_root() {
    target="$1"
    if mountpoint -q /mnt 2>/dev/null; then
        return 0
    fi

    root_part="$(lsblk -nrpo NAME,FSTYPE "$target" 2>/dev/null | awk '$2 ~ /^(ext4|xfs|btrfs)$/ {candidate=$1} END {print candidate}')"
    [ -n "$root_part" ] || return 1
    mkdir -p /mnt
    mount "$root_part" /mnt
}

bind_into_target() {
    path="$1"
    mkdir -p "/mnt$path"
    mount --rbind "$path" "/mnt$path"
    mount --make-rslave "/mnt$path" 2>/dev/null || true
}

cleanup_mounts() {
    for path in /run /media /sys /proc /dev; do
        umount -R "/mnt$path" 2>/dev/null || true
    done
}

prepare_packages() {
    apk_dir="$1"
    [ -d "$apk_dir" ] || fail "ISO package bundle is missing: $apk_dir"

    set -- "$apk_dir"/*.apk
    [ -f "$1" ] || fail "ISO package bundle contains no APK files"

    log "Preparing the offline installation environment..."
    if ! apk add --no-network --no-cache "$apk_dir"/*.apk >/tmp/minimalrouter-apk-install.log 2>&1; then
        cat /tmp/minimalrouter-apk-install.log >&2 || true
        fail "Unable to install the bundled Alpine packages"
    fi
    command -v setup-disk >/dev/null 2>&1 || fail "setup-disk is unavailable after loading the package bundle"
    command -v lsblk >/dev/null 2>&1 || fail "lsblk is unavailable after loading the package bundle"
    modprobe pppoe >/dev/null 2>&1 || fail "The bundled linux-lts kernel cannot load the PPPoE module"
}

install_target_packages() {
    apk_dir_inside="$1"
    set -- "$apk_dir_inside"/*.apk
    [ -f "/mnt$1" ] || fail "Target package path is unavailable inside chroot: $apk_dir_inside"
    if ! chroot /mnt apk add --no-network --no-cache "$apk_dir_inside"/*.apk >/tmp/minimalrouter-target-apk.log 2>&1; then
        cat /tmp/minimalrouter-target-apk.log >&2 || true
        fail "Unable to install bundled packages into the target system"
    fi
}

MEDIA="$(wait_for_media)" || fail "MinimalRouter payload was not found on the boot media"
ISO_ROOT="$MEDIA/minimalrouter"
DIST="$ISO_ROOT/minimalrouter-linux-amd64"
APK_DIR="$ISO_ROOT/apks"
VERSION="dev"
[ -r "$ISO_ROOT/VERSION" ] && VERSION="$(tr -d '\r\n' < "$ISO_ROOT/VERSION")"
[ -n "$VERSION" ] || VERSION="dev"

[ -d "$DIST" ] || fail "MinimalRouter distribution payload is missing"
[ -x "$DIST/bin/router-setup-amd64" ] || fail "router-setup is missing from the ISO payload"

prepare_packages "$APK_DIR"

# The normal installer owns the visible welcome/prerequisite screen, PPPoE
# discovery, WAN/LAN confirmation, dashboard password and transactional network
# verification. VERSION is copied beside it by the ISO builder.
MINIMALROUTER_OFFLINE=1 sh "$DIST/install.sh" --offline || fail "MinimalRouter live configuration did not verify successfully"

printf '\nRecovery console password\n'
printf '%s\n' '-------------------------'
printf 'Set the local Linux root password used only for console recovery.\n'
printf 'It is separate from the Web Dashboard administrator password.\n\n'
while ! passwd; do
    printf 'The passwords did not match or were rejected. Try again.\n'
done

BOOT_SOURCE="$(findmnt -no SOURCE "$MEDIA" 2>/dev/null || true)"
BOOT_DISK="$(boot_disk_for_source "$BOOT_SOURCE")"
CANDIDATES="$(list_candidate_disks "$BOOT_DISK")"
[ -n "$CANDIDATES" ] || fail "No writable installation disk was detected"

show_disk_table
DEFAULT_DISK=""
COUNT="$(printf '%s\n' "$CANDIDATES" | awk 'NF {n++} END {print n+0}')"
[ "$COUNT" -eq 1 ] && DEFAULT_DISK="$(printf '%s\n' "$CANDIDATES" | head -n 1)"

while :; do
    if [ -n "$DEFAULT_DISK" ]; then
        printf 'Install Minimal Router OS v%s to disk [%s]: ' "$VERSION" "$DEFAULT_DISK"
    else
        printf 'Install Minimal Router OS v%s to disk: ' "$VERSION"
    fi
    IFS= read -r TARGET
    [ -n "$TARGET" ] || TARGET="$DEFAULT_DISK"
    if printf '%s\n' "$CANDIDATES" | grep -qxF "$TARGET"; then
        break
    fi
    printf 'Please choose one of the listed installation disks.\n'
done

printf '\nSelected disk: %s\n' "$TARGET"
lsblk "$TARGET" 2>/dev/null || true
printf '\nWARNING: every partition and all data on %s will be erased.\n' "$TARGET"
printf 'Type ERASE to continue: '
IFS= read -r CONFIRM
[ "$CONFIRM" = "ERASE" ] || fail "Disk installation was cancelled"

printf '\nInstalling Alpine Linux 3.22 + Minimal Router OS v%s to %s...\n' "$VERSION" "$TARGET"
sync
swapoff -a 2>/dev/null || true
for part in $(lsblk -nrpo NAME "$TARGET" 2>/dev/null | tail -n +2); do
    umount "$part" 2>/dev/null || true
done

# The operator has already explicitly typed ERASE for this exact device, so the
# setup-disk confirmation can safely be suppressed here.
if ! ERASE_DISKS="$TARGET" SWAP_SIZE=0 setup-disk -m sys -k lts -s 0 "$TARGET"; then
    fail "Alpine system-disk installation failed"
fi

mount_target_root "$TARGET" || fail "The newly installed root filesystem could not be mounted"

# Make the boot media and kernel pseudo-filesystems visible inside the target so
# APK verification and the hardened core installer work exactly as on a normal
# Alpine installation.
bind_into_target /dev
bind_into_target /proc
bind_into_target /sys
bind_into_target /run
bind_into_target /media

TARGET_INSTALLER=/root/minimalrouter-installer
rm -rf "/mnt$TARGET_INSTALLER"
mkdir -p "/mnt$TARGET_INSTALLER"
cp -a "$DIST"/. "/mnt$TARGET_INSTALLER/"
cp "$ISO_ROOT/VERSION" "/mnt$TARGET_INSTALLER/VERSION" 2>/dev/null || true

# setup-disk may install only the base world. Reinstalling the signed APK bundle
# inside the target is deterministic, offline and guarantees every router
# dependency is present before install-core.sh performs its checks.
APK_DIR_INSIDE="$APK_DIR"
install_target_packages "$APK_DIR_INSIDE"

if ! chroot /mnt sh "$TARGET_INSTALLER/install-core.sh" --offline; then
    cleanup_mounts
    fail "MinimalRouter core installation into the target system failed"
fi

# Freeze the verified live configuration before copying SQLite/WAL and the
# helper's last-good state. This avoids carrying a database that is changing
# underneath cp(1), while preserving exactly the WAN/LAN/PPPoE/admin state that
# already passed the production transaction path.
rc-service routerd stop >/dev/null 2>&1 || true
rc-service router-applyd stop >/dev/null 2>&1 || true
sync

# Replace the fresh default database with the exact configuration that was
# already verified on the live system: WAN/LAN roles, PPPoE credentials and the
# hashed dashboard administrator password. The privileged helper will reconcile
# it again on the first disk boot.
rm -rf /mnt/var/lib/minimalrouter /mnt/var/lib/minimalrouter-applyd
cp -a /var/lib/minimalrouter /mnt/var/lib/minimalrouter
cp -a /var/lib/minimalrouter-applyd /mnt/var/lib/minimalrouter-applyd
chroot /mnt chown -R routerd:routerd /var/lib/minimalrouter
chroot /mnt chmod 0700 /var/lib/minimalrouter
find /mnt/var/lib/minimalrouter -maxdepth 1 -type f -name 'minimalrouter.db*' -exec chmod 0600 {} \;
chroot /mnt chown -R root:root /var/lib/minimalrouter-applyd
chroot /mnt chmod 0700 /var/lib/minimalrouter-applyd

mkdir -p /mnt/etc/minimalrouter
printf '%s\n' "$VERSION" > /mnt/etc/minimalrouter/VERSION
chmod 0644 /mnt/etc/minimalrouter/VERSION
rm -rf "/mnt$TARGET_INSTALLER"

sync
cleanup_mounts

printf '\n\033[32m●\033[0m Minimal Router OS v%s installation completed successfully.\n' "$VERSION"
printf '\033[32m●\033[0m PPPoE and WAN/LAN configuration were verified before the disk was written.\n'
printf '\033[32m●\033[0m Dashboard after boot: https://192.168.1.1:8443\n\n'
printf 'The machine will power off now. Detach the ISO, then start it from the installed disk.\n'
sleep 5
poweroff -f
