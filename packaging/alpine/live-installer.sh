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
    [ "$(printf '%s\n' "$CANDIDATES" | awk 'NF {n++} END {print n+0}')" -le 1 ] && return 0
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

    # Installing APK files by path adds exact local-package constraints to
    # /etc/apk/world. Those live-only recovery tools must never become the
    # desired package set for setup-disk (older failed ISOs exposed these as
    # checksum-like ><Q1 world entries). Preserve the pristine Alpine live world,
    # install the tools, then restore world before setup-disk builds the target.
    world_backup=/tmp/minimalrouter-apk-world.before
    if [ -f /etc/apk/world ]; then
        cp /etc/apk/world "$world_backup"
    else
        : > "$world_backup"
    fi

    if ! apk add --no-network --no-cache --force-non-repository "$apk_dir"/*.apk >/tmp/minimalrouter-apk-install.log 2>&1; then
        cp "$world_backup" /etc/apk/world
        chmod 0644 /etc/apk/world
        cat /tmp/minimalrouter-apk-install.log >&2 || true
        fail "Unable to install the bundled Alpine packages"
    fi
    cp "$world_backup" /etc/apk/world
    chmod 0644 /etc/apk/world

    # setup-disk requires a real APKINDEX. Discover the repository on the actual
    # mounted Alpine ISO instead of assuming /media/cdrom or treating our flat
    # package bundle as a repository.
    base_repo="$(find "$MEDIA/apks" -type f -name APKINDEX.tar.gz -print 2>/dev/null | head -1 | xargs -r dirname)"
    [ -n "$base_repo" ] || fail "The Alpine base repository (APKINDEX.tar.gz) was not found on the boot media"
    ALPINE_MEDIA_REPO="$base_repo"
    printf '%s\n' "$ALPINE_MEDIA_REPO" > /etc/apk/repositories
    if ! apk update --no-network >/tmp/minimalrouter-apk-update.log 2>&1; then
        cat /tmp/minimalrouter-apk-update.log >&2 || true
        fail "The Alpine base repository on the ISO could not be opened"
    fi
    command -v setup-disk >/dev/null 2>&1 || fail "setup-disk is unavailable after loading the package bundle"
    command -v lsblk >/dev/null 2>&1 || fail "lsblk is unavailable after loading the package bundle"
    # Moduli LIVE kernela (base ISO verzija) — modloop sa medije. Initramfs
    # modloop mount je nepouzdan na remasterovanom ISO-u, a setup-disk mora da
    # modprobe ext4 za ciljni disk. squashfs -> /tmp/ml, pa BIND montiranje
    # tacnog modul direktorijuma na /lib/modules/<ver> (RW, bez symlink-a).
    live_ver="$(uname -r)"
    if [ ! -d "/lib/modules/$live_ver" ]; then
        modloop_file="$(find /media -name modloop-lts 2>/dev/null | head -1)"
        if [ -n "$modloop_file" ]; then
            mkdir -p /tmp/ml
            if mount -t squashfs -o loop "$modloop_file" /tmp/ml 2>/dev/null; then
                if [ -d "/tmp/ml/modules/$live_ver" ]; then
                    mkdir -p "/lib/modules/$live_ver"
                    if mount --bind "/tmp/ml/modules/$live_ver" "/lib/modules/$live_ver" 2>/dev/null; then
                        log "Loaded live kernel modules ($live_ver) from the boot media"
                    else
                        log "WARNING: could not bind-mount the modloop modules; setup-disk module checks may fail"
                    fi
                else
                    log "WARNING: modloop has no modules/$live_ver; setup-disk module checks may fail"
                fi
            else
                log "WARNING: could not mount the modloop; setup-disk module checks may fail"
            fi
        else
            log "WARNING: modloop-lts not found on media; setup-disk module checks may fail"
        fi
    fi
    if ! find /lib/modules -name "pppoe.ko*" 2>/dev/null | grep -q .; then
        modprobe pppoe 2>/dev/null || fail "The bundled linux-lts kernel cannot load the PPPoE module"
    fi
}

restore_alpine_media_repo() {
    [ -n "${ALPINE_MEDIA_REPO:-}" ] || fail "The Alpine media repository path was lost before disk installation"
    [ -r "$ALPINE_MEDIA_REPO/APKINDEX.tar.gz" ] || fail "The Alpine media APKINDEX is no longer available: $ALPINE_MEDIA_REPO"
    printf '%s\n' "$ALPINE_MEDIA_REPO" > /etc/apk/repositories
    if ! apk update --no-network >/tmp/minimalrouter-apk-update.log 2>&1; then
        cat /tmp/minimalrouter-apk-update.log >&2 || true
        fail "The Alpine media repository could not be restored for setup-disk"
    fi
}

install_target_packages() {
    apk_dir_inside="$1"

    # Validate against the mounted target, then deliberately expand *.apk only
    # after entering the chroot. Expanding it in the live shell would look for
    # /var/cache/minimalrouter/apks on the live tmpfs instead of on /mnt.
    set -- "/mnt$apk_dir_inside"/*.apk
    [ -f "$1" ] || fail "Target package path is unavailable inside chroot: $apk_dir_inside"

    if ! chroot /mnt /bin/sh -c '''
        apk_dir="$1"
        set -- "$apk_dir"/*.apk
        [ -f "$1" ] || exit 66
        exec apk add --no-network --no-cache --force-non-repository "$@"
    ''' sh "$apk_dir_inside" >/tmp/minimalrouter-target-apk.log 2>&1; then
        cat /tmp/minimalrouter-target-apk.log >&2 || true
        fail "Unable to install bundled packages into the target system"
    fi
}

is_qemu_vm() {
    for f in /sys/class/dmi/id/sys_vendor /sys/class/dmi/id/product_name /sys/class/dmi/id/board_vendor; do
        [ -r "$f" ] || continue
        grep -Eiq 'qemu|kvm|proxmox' "$f" 2>/dev/null && return 0
    done
    return 1
}

safe_auto_vm_disk() {
    [ "$COUNT" -eq 1 ] || return 1
    is_qemu_vm || return 1

    candidate="$(printf '%s
' "$CANDIDATES" | awk 'NF {print; exit}')"
    [ -b "$candidate" ] || return 1
    dev="${candidate#/dev/}"
    [ -r "/sys/block/$dev/removable" ] || return 1
    [ "$(cat "/sys/block/$dev/removable" 2>/dev/null || printf 1)" = "0" ] || return 1

    # /dev/vd* is VirtIO block by definition. For emulated SCSI/SATA/NVMe,
    # require the device model/vendor to identify itself as virtual. This keeps
    # a raw physical disk passed through to QEMU out of the automatic erase path.
    case "$candidate" in
        /dev/vd*) ;;
        /dev/sd*|/dev/nvme*)
            identity="$(cat "/sys/block/$dev/device/vendor" "/sys/block/$dev/device/model" 2>/dev/null || true)"
            printf '%s
' "$identity" | grep -Eiq 'qemu|virtio|virtual' || return 1
            ;;
        *) return 1 ;;
    esac

    printf '%s
' "$candidate"
}

preflight_host() {
    mem_kib="$(awk '/^MemTotal:/ {print $2; exit}' /proc/meminfo 2>/dev/null || true)"
    [ -n "$mem_kib" ] || fail "Unable to determine system memory"
    if [ "$mem_kib" -lt 900000 ]; then
        fail "This system has less than 1 GiB RAM. Increase the VM memory to at least 1 GiB and boot the ISO again"
    fi
}

validate_target_disk() {
    disk="$1"
    bytes="$(blockdev --getsize64 "$disk" 2>/dev/null || true)"
    [ -n "$bytes" ] || fail "Unable to determine installation disk size: $disk"
    min_bytes=8589934592
    if [ "$bytes" -lt "$min_bytes" ]; then
        gib="$((bytes / 1024 / 1024 / 1024))"
        fail "Installation disk $disk is only ${gib} GiB. Use an 8 GiB or larger VM disk"
    fi
}

guard_existing_install() {
    boot_source="$(findmnt -no SOURCE "$MEDIA" 2>/dev/null || true)"
    boot_disk="$(boot_disk_for_source "$boot_source")"
    check_dir=/tmp/minimalrouter-existing-check
    mkdir -p "$check_dir"

    for disk in $(list_candidate_disks "$boot_disk"); do
        for part in $(lsblk -nrpo NAME,FSTYPE "$disk" 2>/dev/null | awk '$2 ~ /^(ext4|xfs|btrfs)$/ {print $1}'); do
            umount "$check_dir" 2>/dev/null || true
            if mount -o ro "$part" "$check_dir" 2>/dev/null; then
                if [ -f "$check_dir/etc/minimalrouter/installed" ] || [ -f "$check_dir/etc/minimalrouter/VERSION" ]; then
                    installed_version="$(cat "$check_dir/etc/minimalrouter/VERSION" 2>/dev/null | tr -d '\r\n' || true)"
                    umount "$check_dir" 2>/dev/null || true
                    printf '\nminimalrouter is already installed%s on %s.\n' "${installed_version:+ v$installed_version}" "$disk"
                    printf 'The installer stopped before making disk or network changes.\n\n'
                    printf 'Detach the ISO in Proxmox, then type: reboot\n'
                    printf 'This shell remains available for recovery diagnostics.\n\n'
                    exec /bin/sh
                fi
                umount "$check_dir" 2>/dev/null || true
            fi
        done
    done
}

configure_live_ssh() {
    live_lan_file=/run/minimalrouter-live-lan
    [ -r "$live_lan_file" ] || fail "Selected LAN interface is unavailable for recovery SSH"
    live_lan="$(tr -d '\r\n' < "$live_lan_file")"
    [ -n "$live_lan" ] || fail "Selected LAN interface is empty"
    [ -e "/sys/class/net/$live_lan" ] || fail "Selected LAN interface does not exist: $live_lan"

    ip link set dev "$live_lan" up
    ip addr flush dev "$live_lan" 2>/dev/null || true
    ip addr add 192.168.1.1/24 dev "$live_lan"

    mkdir -p /etc/ssh
    ssh-keygen -A >/dev/null 2>&1
    cat > /etc/ssh/sshd_config <<'SSHD'
Port 22
AddressFamily inet
ListenAddress 192.168.1.1
PermitRootLogin yes
PasswordAuthentication yes
KbdInteractiveAuthentication no
PermitEmptyPasswords no
X11Forwarding no
AllowTcpForwarding no
PermitTunnel no
Subsystem sftp internal-sftp
SSHD
    rc-service sshd restart >/dev/null 2>&1 || rc-service sshd start >/dev/null 2>&1 || fail "Recovery SSH could not be started"

    # On VGA/noVNC ttyS0 can be an independent recovery login. When the wizard
    # itself owns ttyS0, getty must not compete for the same keystrokes.
    if [ "${MINIMALROUTER_INSTALL_TTY:-/dev/tty1}" != "/dev/ttyS0" ] \
       && [ -c /dev/ttyS0 ] \
       && ! grep -q '^ttyS0::respawn:' /etc/inittab 2>/dev/null; then
        printf '%s
' 'ttyS0::respawn:/sbin/getty -L ttyS0 115200 vt100' >> /etc/inittab
        grep -qxF ttyS0 /etc/securetty 2>/dev/null || printf '%s
' ttyS0 >> /etc/securetty
        kill -HUP 1 2>/dev/null || true
    fi

    printf '\nRecovery access is active on the selected LAN:\n'
    printf '  SSH:    ssh root@192.168.1.1\n'
    printf '  Serial: ttyS0 at 115200 baud\n'
    printf 'Use the recovery password you just set. SSH is not exposed on the WAN.\n\n'
}

verify_bundle() {
    manifest="$ISO_ROOT/APK-SHA256SUMS"
    [ -r "$manifest" ] || fail "ISO APK checksum manifest is missing"
    if ! (cd "$APK_DIR" && sha256sum -c "$manifest") >/tmp/minimalrouter-apk-sha.log 2>&1; then
        cat /tmp/minimalrouter-apk-sha.log >&2 || true
        fail "ISO APK bundle checksum verification failed"
    fi
}

configure_target_recovery() {
    root_shadow="$(grep '^root:' /etc/shadow | head -1)"
    [ -n "$root_shadow" ] || fail "Live recovery password hash is unavailable"
    grep -v '^root:' /mnt/etc/shadow > /mnt/etc/shadow.minimalrouter
    { printf '%s\n' "$root_shadow"; cat /mnt/etc/shadow.minimalrouter; } > /mnt/etc/shadow
    rm -f /mnt/etc/shadow.minimalrouter
    chmod 0600 /mnt/etc/shadow

    mkdir -p /mnt/etc/ssh
    cat > /mnt/etc/ssh/sshd_config <<'SSHD'
Port 22
AddressFamily inet
PermitRootLogin yes
PasswordAuthentication yes
KbdInteractiveAuthentication no
PermitEmptyPasswords no
X11Forwarding no
AllowTcpForwarding no
PermitTunnel no
Subsystem sftp internal-sftp
SSHD
    chroot /mnt ssh-keygen -A >/dev/null 2>&1
    chroot /mnt rc-update add sshd default >/dev/null

    grep -qxF ttyS0 /mnt/etc/securetty 2>/dev/null || printf '%s\n' ttyS0 >> /mnt/etc/securetty
    if ! grep -q '^ttyS0::respawn:' /mnt/etc/inittab 2>/dev/null; then
        printf '%s\n' 'ttyS0::respawn:/sbin/getty -L ttyS0 115200 vt100' >> /mnt/etc/inittab
    fi

    if [ -f /mnt/etc/update-extlinux.conf ]; then
        sed -i '/# MinimalRouter serial begin/,/# MinimalRouter serial end/d' /mnt/etc/update-extlinux.conf
        cat >> /mnt/etc/update-extlinux.conf <<'EXTLINUX'
# MinimalRouter serial begin
serial_port=0
serial_baud=115200
default_kernel_opts="$default_kernel_opts console=tty0 console=ttyS0,115200"
# MinimalRouter serial end
EXTLINUX
        chroot /mnt update-extlinux >/dev/null 2>&1 || fail "Could not persist the serial console in extlinux"
    fi
    if [ -f /mnt/etc/default/grub ] && [ -d /mnt/boot/grub ]; then
        sed -i '/# MinimalRouter serial begin/,/# MinimalRouter serial end/d' /mnt/etc/default/grub
        cat >> /mnt/etc/default/grub <<'GRUB'
# MinimalRouter serial begin
GRUB_TERMINAL="console serial"
GRUB_SERIAL_COMMAND="serial --unit=0 --speed=115200 --word=8 --parity=no --stop=1"
GRUB_CMDLINE_LINUX_DEFAULT="$GRUB_CMDLINE_LINUX_DEFAULT console=tty0 console=ttyS0,115200"
# MinimalRouter serial end
GRUB
        chroot /mnt grub-mkconfig -o /boot/grub/grub.cfg >/dev/null 2>&1 || fail "Could not persist the serial console in GRUB"
    fi

    cat > /mnt/etc/apk/repositories <<'REPOS'
https://dl-cdn.alpinelinux.org/alpine/v3.22/main
https://dl-cdn.alpinelinux.org/alpine/v3.22/community
REPOS
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

verify_bundle
prepare_packages "$APK_DIR"
preflight_host
guard_existing_install

# The normal installer owns the visible welcome/prerequisite screen, PPPoE
# discovery, WAN/LAN confirmation, dashboard password and transactional network
# verification. VERSION is copied beside it by the ISO builder.
MINIMALROUTER_ISO_INSTALL=1 MINIMALROUTER_OFFLINE=1 sh "$DIST/install.sh" --offline || fail "MinimalRouter live configuration could not be prepared"
# install-core must not touch the caller-owned repo in offline mode. Reassert it
# here anyway so future installer changes cannot silently reintroduce a CDN
# dependency before setup-disk.
restore_alpine_media_repo

printf '\nRecovery / SSH root password\n'
printf '%s\n' '----------------------------'
printf 'Set the Linux root password used for emergency console and trusted-LAN SSH recovery.\n'
printf 'It is separate from the Web Dashboard administrator password and is never exposed on WAN.\n\n'
while ! passwd; do
    printf 'The passwords did not match or were rejected. Try again.\n'
done
configure_live_ssh

BOOT_SOURCE="$(findmnt -no SOURCE "$MEDIA" 2>/dev/null || true)"
BOOT_DISK="$(boot_disk_for_source "$BOOT_SOURCE")"
CANDIDATES="$(list_candidate_disks "$BOOT_DISK")"
[ -n "$CANDIDATES" ] || fail "No writable installation disk was detected"

show_disk_table
DEFAULT_DISK=""
COUNT="$(printf '%s\n' "$CANDIDATES" | awk 'NF {n++} END {print n+0}')"
[ "$COUNT" -eq 1 ] && DEFAULT_DISK="$(printf '%s\n' "$CANDIDATES" | head -n 1)"

TARGET="$(safe_auto_vm_disk 2>/dev/null || true)"
if [ -n "$TARGET" ]; then
    printf '\nProxmox/QEMU VM detected.\n'
    printf 'Using the only attached installation disk automatically: %s\n' "$TARGET"
    printf 'Only disks visible inside this VM are considered.\n'
else
    while :; do
        if [ -n "$DEFAULT_DISK" ]; then
            printf 'Install Minimal Router OS v%s to disk [%s]: ' "$VERSION" "$DEFAULT_DISK"
        else
            printf 'Install Minimal Router OS v%s to disk: ' "$VERSION"
        fi
        IFS= read -r TARGET
        [ -n "$TARGET" ] || TARGET="$DEFAULT_DISK"
        printf '%s\n' "$CANDIDATES" | grep -qxF "$TARGET" && break
        printf 'Please choose one of the listed installation disks.\n'
    done
    printf '\nSelected disk: %s\n' "$TARGET"
    lsblk "$TARGET" 2>/dev/null || true
    printf '\nThis layout needs one extra safety check.\n'
    printf 'Every partition and all data on %s will be erased.\n' "$TARGET"
    printf 'Type ERASE to continue: '
    IFS= read -r CONFIRM
    case "$CONFIRM" in [Ee][Rr][Aa][Ss][Ee]) ;; *) fail "Disk installation was cancelled" ;; esac
fi

validate_target_disk "$TARGET"
printf '\nInstalling Alpine Linux 3.22 + minimalrouter v%s to %s...\n' "$VERSION" "$TARGET"
sync
swapoff -a 2>/dev/null || true
for part in $(lsblk -nrpo NAME "$TARGET" 2>/dev/null | tail -n +2); do
    umount "$part" 2>/dev/null || true
done

# Reassert the local ISO repository at the last possible point. This is also
# what makes the CI full-install test a genuine zero-Internet installation.
restore_alpine_media_repo

# The target has either passed the conservative virtual-disk guard or the
# operator explicitly confirmed it. Capture Alpine's verbose installer output:
# if setup-disk ever fails, the ISO/CI log must contain the actual reason rather
# than only a generic MinimalRouter error.
SETUP_DISK_LOG=/tmp/minimalrouter-setup-disk.log
rm -f "$SETUP_DISK_LOG"
if ! ERASE_DISKS="$TARGET" SWAP_SIZE=0 setup-disk -v -m sys -k lts -s 0 "$TARGET" >"$SETUP_DISK_LOG" 2>&1; then
    printf '\n--- Alpine setup-disk diagnostic log ---\n' >&2
    cat "$SETUP_DISK_LOG" >&2 || true
    printf '%s\n' '--- installer environment ---' >&2
    printf 'kernel: %s\n' "$(uname -r)" >&2
    printf 'target: %s\n' "$TARGET" >&2
    printf 'repositories:\n' >&2
    cat /etc/apk/repositories >&2 2>/dev/null || true
    printf 'target disk:\n' >&2
    lsblk -f "$TARGET" >&2 2>/dev/null || true
    printf 'required tools:\n' >&2
    for tool in setup-disk sfdisk mkfs.ext4 extlinux grub-install; do
        command -v "$tool" >&2 2>/dev/null || printf 'missing: %s\n' "$tool" >&2
    done
    printf '%s\n' '--- end setup-disk diagnostics ---' >&2
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
TARGET_APK_DIR=/var/cache/minimalrouter/apks
rm -rf "/mnt$TARGET_APK_DIR"
mkdir -p "/mnt$TARGET_APK_DIR"
cp -a "$APK_DIR"/. "/mnt$TARGET_APK_DIR/"
cp "$ISO_ROOT/APK-SHA256SUMS" "/mnt$TARGET_APK_DIR/APK-SHA256SUMS"
APK_DIR_INSIDE="$TARGET_APK_DIR"
install_target_packages "$APK_DIR_INSIDE"

if ! chroot /mnt sh "$TARGET_INSTALLER/install-core.sh" --offline; then
    cleanup_mounts
    fail "MinimalRouter core installation into the target system failed"
fi
configure_target_recovery

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
cat > /mnt/etc/minimalrouter/installed <<EOF
version=$VERSION
installed_by=all-in-one-iso
EOF
chmod 0644 /mnt/etc/minimalrouter/VERSION /mnt/etc/minimalrouter/installed
rm -rf "/mnt$TARGET_INSTALLER"

sync
cleanup_mounts

cat <<'ART'
+----------------------------------------------------------+
|                      minimalrouter                       |
+----------------------------------------------------------+
ART
printf '\n\033[32m●\033[0m minimalrouter v%s installation completed successfully.\n' "$VERSION"
printf '\033[32m●\033[0m PPPoE and WAN/LAN configuration were saved before the disk was written.\n'
printf '\033[32m●\033[0m Dashboard after boot: https://192.168.1.1:8443\n'
printf '\033[32m●\033[0m SSH after boot: ssh root@192.168.1.1 (LAN/WireGuard only)\n'
printf '\033[32m●\033[0m Serial recovery: ttyS0 @ 115200\n\n'
printf 'The machine will reboot now. The first boot finalizes minimalrouter.\n'
eject /dev/sr0 2>/dev/null || true
sleep 5
reboot -f
