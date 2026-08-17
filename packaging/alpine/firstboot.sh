#!/bin/sh
# MinimalRouter golden-image first boot.
# Runs once from the already-installed appliance image. No packages are installed.
set -eu
umask 077

PROVISION=/run/minimalrouter-firstboot.json
DONE=/etc/minimalrouter/firstboot-complete

fail() {
    printf '\nERROR: %s\n' "$*" >&2
    printf 'First boot stopped before router services were started.\n' >&2
    printf 'A recovery shell is opening on this console. Type exit to retry on the next boot.\n\n' >&2
    exec /bin/sh
}

root_partition() {
    awk '$2 == "/" { print $1; exit }' /proc/mounts
}

root_disk() {
    part="$(root_partition)"
    case "$part" in
        /dev/nvme*n*p[0-9]*|/dev/mmcblk*p[0-9]*)
            printf '%s\n' "$(printf '%s' "$part" | sed -E 's/p[0-9]+$//')"
            ;;
        /dev/vd*[0-9]*|/dev/sd*[0-9]*|/dev/xvd*[0-9]*)
            printf '%s\n' "$(printf '%s' "$part" | sed -E 's/[0-9]+$//')"
            ;;
        *)
            return 1
            ;;
    esac
}

selected_console() {
    disk="$(root_disk 2>/dev/null || true)"
    if [ -n "$disk" ] && [ -b "$disk" ]; then
        value="$(dd if="$disk" bs=512 skip=64 count=1 2>/dev/null | tr -d '\000\r\n ' || true)"
        case "$value" in
            ttyS0) printf '%s\n' /dev/ttyS0; return 0 ;;
            tty1)  printf '%s\n' /dev/tty1; return 0 ;;
        esac
    fi
    printf '%s\n' /dev/tty1
}

enable_recovery_gettys() {
    if [ -f /etc/inittab ]; then
        sed -i \
            -e 's|^# MR-FIRSTBOOT tty1::respawn:|tty1::respawn:|' \
            -e 's|^# MR-FIRSTBOOT ttyS0::respawn:|ttyS0::respawn:|' \
            /etc/inittab
        if ! grep -q '^ttyS0::respawn:' /etc/inittab; then
            printf '%s\n' 'ttyS0::respawn:/sbin/getty -L ttyS0 115200 vt100' >> /etc/inittab
        fi
        grep -qxF ttyS0 /etc/securetty 2>/dev/null || printf '%s\n' ttyS0 >> /etc/securetty
        kill -HUP 1 2>/dev/null || true
    fi
}

resize_root_filesystem() {
    part="$(root_partition)"
    [ -b "$part" ] || return 0
    printf 'Checking the appliance filesystem size...\n'
    if resize2fs "$part" >/tmp/minimalrouter-resize2fs.log 2>&1; then
        printf '  [OK] filesystem ready\n'
    else
        cat /tmp/minimalrouter-resize2fs.log >&2 || true
        fail "Could not verify/expand the root filesystem"
    fi
}

configure_ssh() {
    mkdir -p /etc/ssh
    ssh-keygen -A >/dev/null 2>&1 || fail "Could not generate SSH host keys"
    cat > /etc/ssh/sshd_config <<'SSHD'
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
    chmod 0600 /etc/ssh/ssh_host_*_key 2>/dev/null || true
}

main() {
    [ -f "$DONE" ] && exit 0
    [ -x /usr/sbin/router-setup ] || fail "router-setup is missing from the golden image"

    cat <<'ART'
+----------------------------------------------------------+
|                      minimalrouter                       |
+----------------------------------------------------------+
ART
    version="$(cat /etc/minimalrouter/VERSION 2>/dev/null || printf dev)"
    printf '\nminimalrouter v%s — first boot\n\n' "$version"
    printf 'The operating system, kernel, packages, Go services and Dashboard are already installed.\n'
    printf 'This screen only writes your router configuration and recovery password.\n\n'

    resize_root_filesystem

    rm -f "$PROVISION"
    /usr/sbin/router-setup collect --output "$PROVISION" --data-dir /var/lib/minimalrouter \
        || fail "Router configuration was not completed"
    [ -s "$PROVISION" ] || fail "First-boot configuration file was not created"

    /usr/sbin/router-setup apply --offline --input "$PROVISION" --data-dir /var/lib/minimalrouter \
        || fail "Router configuration could not be saved"
    rm -f "$PROVISION"

    chown -R routerd:routerd /var/lib/minimalrouter
    chmod 0700 /var/lib/minimalrouter
    find /var/lib/minimalrouter -maxdepth 1 -type f -name 'minimalrouter.db*' -exec chmod 0600 {} \;

    printf '\nRecovery / SSH root password\n'
    printf '%s\n' '----------------------------'
    printf 'Set the Linux root password for emergency console and trusted-LAN SSH recovery.\n'
    printf 'It is separate from the Web Dashboard administrator password.\n\n'
    while ! passwd; do
        printf 'The passwords did not match or were rejected. Try again.\n'
    done

    configure_ssh
    enable_recovery_gettys

    mkdir -p /etc/minimalrouter
    {
        printf 'version=%s\n' "$version"
        printf 'configured_at=%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || printf unknown)"
    } > "$DONE"
    chmod 0600 "$DONE"
    sync

    cat <<'ART'

+----------------------------------------------------------+
|                      minimalrouter                       |
+----------------------------------------------------------+
ART
    printf '\n\033[32m●\033[0m First boot configuration completed.\n'
    printf '\033[32m●\033[0m Web Dashboard: https://192.168.1.1:8443\n'
    printf '\033[32m●\033[0m SSH recovery:  ssh root@192.168.1.1\n'
    printf '\033[32m●\033[0m Serial:        ttyS0 @ 115200\n'
    printf '\nRouter services are starting now. Connect your computer to the LAN interface.\n\n'
}

TTY="$(selected_console)"
[ -c "$TTY" ] || TTY=/dev/tty1
exec <"$TTY" >"$TTY" 2>&1
main
