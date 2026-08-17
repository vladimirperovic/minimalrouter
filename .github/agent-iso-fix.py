from pathlib import Path


def replace_once(path: str, old: str, new: str) -> None:
    p = Path(path)
    text = p.read_text()
    count = text.count(old)
    if count != 1:
        raise SystemExit(f"{path}: expected exactly one match, got {count}: {old[:100]!r}")
    p.write_text(text.replace(old, new, 1))


def insert_before(path: str, marker: str, text: str) -> None:
    p = Path(path)
    value = p.read_text()
    if marker not in value:
        raise SystemExit(f"{path}: marker not found: {marker!r}")
    p.write_text(value.replace(marker, text + marker, 1))


# build-iso.sh: complete package bundle + working serial/UEFI path.
p = "packaging/alpine/build-iso.sh"
replace_once(
    p,
    "REQUIRED_PACKAGES=\"alpine-base alpine-conf linux-lts linux-firmware-none e2fsprogs grub grub-efi syslinux dosfstools util-linux nftables ppp ppp-pppoe dnsmasq iproute2 iputils-ping iputils-arping ca-certificates wireguard-tools-wg doas squid hostapd hostapd-openrc iw inadyn inadyn-openrc chrony chrony-openrc logrotate\"",
    "REQUIRED_PACKAGES=\"alpine-base alpine-conf linux-lts linux-firmware-none e2fsprogs grub grub-efi syslinux dosfstools util-linux nftables ppp ppp-pppoe dnsmasq iproute2 iputils-ping iputils-arping ca-certificates openssh-server wireguard-tools-wg doas squid hostapd hostapd-openrc iw inadyn inadyn-openrc chrony chrony-openrc logrotate\"",
)
replace_once(
    p,
    "nftables ppp ppp-pppoe dnsmasq iproute2 iputils-ping iputils-arping ca-certificates \\\n                  wireguard-tools-wg",
    "nftables ppp ppp-pppoe dnsmasq iproute2 iputils-ping iputils-arping ca-certificates openssh-server \\\n                  wireguard-tools-wg",
)
replace_once(
    p,
    '''start() {
    # Instalater radi na vidljivoj konzoli: tty1 (VGA) ako postoji,
    # inace ttyS0 (serial-only VM).
    if [ -c /dev/tty1 ]; then
        INSTALL_TTY="/dev/tty1"
    else
        INSTALL_TTY="/dev/ttyS0"
    fi
    ebegin "Launching Minimal Router OS installer on ${INSTALL_TTY#/dev/}"

    # Prevent init from respawning a login prompt on top of the installer.
    if [ -f /etc/inittab ]; then
        sed -i 's#^ttyS0::respawn:#\\# MinimalRouter owns ttyS0: #g; s#^tty1::respawn:#\\# MinimalRouter owns tty1: #g' /etc/inittab 2>/dev/null || true
        kill -HUP 1 2>/dev/null || true
    fi
    pkill -TERM -f '[g]etty.*ttyS0' 2>/dev/null || true
    pkill -TERM -f '[g]etty.*tty1' 2>/dev/null || true

    (
        exec <"$INSTALL_TTY" >"$INSTALL_TTY" 2>&1
        exec /etc/minimalrouter/live-installer.sh
    ) &
    echo $! > "$pidfile"
    chmod 0600 "$pidfile"
    eend 0
}
''',
    '''start() {
    INSTALL_TTY="/dev/tty1"
    case " $(cat /proc/cmdline 2>/dev/null || true) " in
        *" minimalrouter.console=ttyS0 "*) INSTALL_TTY="/dev/ttyS0" ;;
        *) [ -c /dev/tty1 ] || INSTALL_TTY="/dev/ttyS0" ;;
    esac
    ebegin "Launching Minimal Router OS installer on ${INSTALL_TTY#/dev/}"

    # The installer owns only its selected TTY. The other console remains a
    # recovery path instead of being killed unconditionally.
    if [ -f /etc/inittab ]; then
        if [ "$INSTALL_TTY" = "/dev/ttyS0" ]; then
            sed -i 's#^ttyS0::respawn:#\\# MinimalRouter installer owns ttyS0: #g' /etc/inittab 2>/dev/null || true
        else
            sed -i 's#^tty1::respawn:#\\# MinimalRouter installer owns tty1: #g' /etc/inittab 2>/dev/null || true
        fi
        kill -HUP 1 2>/dev/null || true
    fi
    pkill -TERM -f "[g]etty.*${INSTALL_TTY#/dev/}" 2>/dev/null || true

    if [ -c /dev/ttyS0 ]; then
        printf 'MinimalRouter installer service started; UI=%s; serial=ttyS0@115200\\n' "${INSTALL_TTY#/dev/}" >/dev/ttyS0 2>/dev/null || true
    fi

    (
        exec <"$INSTALL_TTY" >"$INSTALL_TTY" 2>&1
        exec /etc/minimalrouter/live-installer.sh
    ) &
    echo $! > "$pidfile"
    chmod 0600 "$pidfile"
    eend 0
}
''',
)
replace_once(
    p,
    '''LABEL minimalrouter
  MENU LABEL Minimal Router OS Installer
  KERNEL /boot/vmlinuz-lts
  INITRD /boot/initramfs-lts
  APPEND modules=loop,squashfs,sd-mod,usb-storage modloop=/boot/modloop-lts console=ttyS0,115200 console=tty0
EOF
''',
    '''LABEL minimalrouter
  MENU LABEL Minimal Router OS Installer (VGA/noVNC)
  KERNEL /boot/vmlinuz-lts
  INITRD /boot/initramfs-lts
  APPEND modules=loop,squashfs,sd-mod,usb-storage modloop=/boot/modloop-lts console=tty0 console=ttyS0,115200

LABEL minimalrouter-serial
  MENU LABEL Minimal Router OS Installer (serial ttyS0 115200)
  KERNEL /boot/vmlinuz-lts
  INITRD /boot/initramfs-lts
  APPEND modules=loop,squashfs,sd-mod,usb-storage modloop=/boot/modloop-lts minimalrouter.console=ttyS0 console=tty0 console=ttyS0,115200
EOF
''',
)
replace_once(
    p,
    '''menuentry "Linux lts" {
linux\t/boot/vmlinuz-lts modules=loop,squashfs,sd-mod,usb-storage console=ttyS0,115200 console=tty0
initrd\t/boot/initramfs-lts
}
EOF
''',
    '''serial --unit=0 --speed=115200 --word=8 --parity=no --stop=1
terminal_input console serial
terminal_output console serial

menuentry "Minimal Router OS Installer (VGA/noVNC)" {
linux\t/boot/vmlinuz-lts modules=loop,squashfs,sd-mod,usb-storage console=tty0 console=ttyS0,115200
initrd\t/boot/initramfs-lts
}

menuentry "Minimal Router OS Installer (serial ttyS0 115200)" {
linux\t/boot/vmlinuz-lts modules=loop,squashfs,sd-mod,usb-storage minimalrouter.console=ttyS0 console=tty0 console=ttyS0,115200
initrd\t/boot/initramfs-lts
}
EOF
''',
)
replace_once(p, "build_apkovl\nbuild_syslinux_config\n", "build_apkovl\nbuild_syslinux_config\nbuild_grub_config\n")
replace_once(
    p,
    '    -map "$BUILD_DIR/syslinux.cfg" /boot/syslinux/syslinux.cfg \\\n    -volid',
    '    -map "$BUILD_DIR/syslinux.cfg" /boot/syslinux/syslinux.cfg \\\n    -map "$BUILD_DIR/grub.cfg" /boot/grub/grub.cfg \\\n    -volid',
)
replace_once(
    p,
    'iso_ls_has /boot modloop-lts || { echo "ERROR: final ISO is missing modloop-lts" >&2; exit 1; }\n',
    'iso_ls_has /boot modloop-lts || { echo "ERROR: final ISO is missing modloop-lts" >&2; exit 1; }\niso_ls_has /boot/grub grub.cfg || { echo "ERROR: final ISO is missing the custom UEFI GRUB config" >&2; exit 1; }\nls "$APK_DIR"/openssh-server-*.apk >/dev/null 2>&1 || { echo "ERROR: final ISO package bundle is missing openssh-server" >&2; exit 1; }\n',
)

# Console wrapper: retain the chosen LAN interface only, never credentials.
p = "packaging/alpine/install-console.sh"
replace_once(
    p,
    "PROVISION_FILE=/run/minimalrouter-console-setup.json\nINTERACTIVE_SETUP=0\n",
    "PROVISION_FILE=/run/minimalrouter-console-setup.json\nLIVE_LAN_FILE=/run/minimalrouter-live-lan\nINTERACTIVE_SETUP=0\n",
)
replace_once(
    p,
    '    "$SETUP_BIN" collect --output "$PROVISION_FILE" --data-dir /var/lib/minimalrouter\nfi\n',
    '''    "$SETUP_BIN" collect --output "$PROVISION_FILE" --data-dir /var/lib/minimalrouter
    if [ "${MINIMALROUTER_ISO_INSTALL:-0}" = "1" ] && [ -s "$PROVISION_FILE" ]; then
        live_lan="$(sed -n 's/.*"lan_interface":"\\([^"]*\\)".*/\\1/p' "$PROVISION_FILE" | head -1)"
        [ -n "$live_lan" ] || { echo "ERROR: selected LAN interface could not be recovered for ISO SSH" >&2; exit 1; }
        printf '%s\\n' "$live_lan" > "$LIVE_LAN_FILE"
        chmod 0600 "$LIVE_LAN_FILE"
    fi
fi
''',
)

# Remove the accidental duplicate message in the wizard.
p = "cmd/router-setup/main.go"
replace_once(
    p,
    '\tfmt.Println("Nothing is committed until you review and confirm the final summary.")\n\tfmt.Println("Nothing is committed until you review and confirm the final summary.")\n',
    '\tfmt.Println("Nothing is committed until you review and confirm the final summary.")\n',
)

# live-installer.sh: real base ISO repo, local target package copy, live SSH,
# persistent target SSH + serial console.
p = "packaging/alpine/live-installer.sh"
replace_once(
    p,
    '''    if ! apk add --no-network --no-cache --force-non-repository --allow-untrusted "$apk_dir"/*.apk >/tmp/minimalrouter-apk-install.log 2>&1; then
        cat /tmp/minimalrouter-apk-install.log >&2 || true
        fail "Unable to install the bundled Alpine packages"
    fi
    # Live okruzenje nema internet: apk repo postaje offline bundle sa medije,
    # da setup-disk-ov apk add (sfdisk/e2fsprogs/syslinux) ne ide na dl-cdn.
    printf '%s\\n' "$apk_dir" > /etc/apk/repositories
    apk update --no-network >/dev/null 2>&1 || true
''',
    '''    if ! apk add --no-network --no-cache --force-non-repository "$apk_dir"/*.apk >/tmp/minimalrouter-apk-install.log 2>&1; then
        cat /tmp/minimalrouter-apk-install.log >&2 || true
        fail "Unable to install the bundled Alpine packages"
    fi

    # setup-disk requires a real APKINDEX. Discover the repository on the actual
    # mounted Alpine ISO instead of assuming /media/cdrom or treating our flat
    # package bundle as a repository.
    base_repo="$(find "$MEDIA/apks" -type f -name APKINDEX.tar.gz -print 2>/dev/null | head -1 | xargs -r dirname)"
    [ -n "$base_repo" ] || fail "The Alpine base repository (APKINDEX.tar.gz) was not found on the boot media"
    printf '%s\\n' "$base_repo" > /etc/apk/repositories
    if ! apk update --no-network >/tmp/minimalrouter-apk-update.log 2>&1; then
        cat /tmp/minimalrouter-apk-update.log >&2 || true
        fail "The Alpine base repository on the ISO could not be opened"
    fi
''',
)
replace_once(
    p,
    'if ! chroot /mnt apk add --no-network --no-cache --force-non-repository --allow-untrusted "$apk_dir_inside"/*.apk >/tmp/minimalrouter-target-apk.log 2>&1; then',
    'if ! chroot /mnt apk add --no-network --no-cache --force-non-repository "$apk_dir_inside"/*.apk >/tmp/minimalrouter-target-apk.log 2>&1; then',
)
insert_before(
    p,
    'MEDIA="$(wait_for_media)" || fail "MinimalRouter payload was not found on the boot media"\n',
    r'''configure_live_ssh() {
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

    if [ -c /dev/ttyS0 ] && ! grep -q '^ttyS0::respawn:' /etc/inittab 2>/dev/null; then
        printf '%s\n' 'ttyS0::respawn:/sbin/getty -L ttyS0 115200 vt100' >> /etc/inittab
        grep -qxF ttyS0 /etc/securetty 2>/dev/null || printf '%s\n' ttyS0 >> /etc/securetty
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

''',
)
replace_once(
    p,
    '[ -x "$DIST/bin/router-setup-amd64" ] || fail "router-setup is missing from the ISO payload"\n\nprepare_packages "$APK_DIR"\n',
    '[ -x "$DIST/bin/router-setup-amd64" ] || fail "router-setup is missing from the ISO payload"\n\nverify_bundle\nprepare_packages "$APK_DIR"\n',
)
replace_once(
    p,
    'MINIMALROUTER_OFFLINE=1 sh "$DIST/install.sh" --offline || fail "MinimalRouter live configuration did not verify successfully"',
    'MINIMALROUTER_ISO_INSTALL=1 MINIMALROUTER_OFFLINE=1 sh "$DIST/install.sh" --offline || fail "MinimalRouter live configuration could not be prepared"',
)
replace_once(
    p,
    "while ! passwd; do\n    printf 'The passwords did not match or were rejected. Try again.\\n'\ndone\n\nBOOT_SOURCE=",
    "while ! passwd; do\n    printf 'The passwords did not match or were rejected. Try again.\\n'\ndone\nconfigure_live_ssh\n\nBOOT_SOURCE=",
)
replace_once(
    p,
    'APK_DIR_INSIDE="$APK_DIR"\ninstall_target_packages "$APK_DIR_INSIDE"\n',
    '''TARGET_APK_DIR=/var/cache/minimalrouter/apks
rm -rf "/mnt$TARGET_APK_DIR"
mkdir -p "/mnt$TARGET_APK_DIR"
cp -a "$APK_DIR"/. "/mnt$TARGET_APK_DIR/"
cp "$ISO_ROOT/APK-SHA256SUMS" "/mnt$TARGET_APK_DIR/APK-SHA256SUMS"
APK_DIR_INSIDE="$TARGET_APK_DIR"
install_target_packages "$APK_DIR_INSIDE"
''',
)
replace_once(
    p,
    'if ! chroot /mnt sh "$TARGET_INSTALLER/install-core.sh" --offline; then\n    cleanup_mounts\n    fail "MinimalRouter core installation into the target system failed"\nfi\n\n# Freeze the verified live configuration',
    'if ! chroot /mnt sh "$TARGET_INSTALLER/install-core.sh" --offline; then\n    cleanup_mounts\n    fail "MinimalRouter core installation into the target system failed"\nfi\nconfigure_target_recovery\n\n# Freeze the verified live configuration',
)
replace_once(
    p,
    "printf '\\033[32m●\\033[0m Dashboard after boot: https://192.168.1.1:8443\\n\\n'\n",
    "printf '\\033[32m●\\033[0m Dashboard after boot: https://192.168.1.1:8443\\n'\nprintf '\\033[32m●\\033[0m SSH after boot: ssh root@192.168.1.1 (LAN/WireGuard only)\\n'\nprintf '\\033[32m●\\033[0m Serial recovery: ttyS0 @ 115200\\n\\n'\n",
)

# Installed package/service policy.
p = "packaging/alpine/install-dist.sh"
replace_once(
    p,
    'REQUIRED_PACKAGES="nftables ppp ppp-pppoe dnsmasq iproute2 iputils-ping iputils-arping ca-certificates wireguard-tools-wg doas squid hostapd hostapd-openrc iw inadyn inadyn-openrc chrony chrony-openrc logrotate"',
    'REQUIRED_PACKAGES="nftables ppp ppp-pppoe dnsmasq iproute2 iputils-ping iputils-arping ca-certificates openssh-server wireguard-tools-wg doas squid hostapd hostapd-openrc iw inadyn inadyn-openrc chrony chrony-openrc logrotate"',
)
replace_once(p, "for svc in dhcpcd sshd dropbear telnetd httpd miniupnpd upnpd rpcbind; do", "for svc in dhcpcd dropbear telnetd httpd miniupnpd upnpd rpcbind; do")
replace_once(p, "rc-update add chronyd default\nrc-update add router-applyd default\n", "rc-update add chronyd default\nrc-update add sshd default\nrc-update add router-applyd default\n")

p = "packaging/alpine/install.sh"
replace_once(
    p,
    "apk add --no-cache nftables ppp ppp-pppoe dnsmasq iproute2 iputils-ping ca-certificates \\\n    wireguard-tools-wg squid",
    "apk add --no-cache nftables ppp ppp-pppoe dnsmasq iproute2 iputils-ping ca-certificates openssh-server \\\n    wireguard-tools-wg squid",
)
replace_once(p, "for unused_service in dhcpcd sshd dropbear telnetd httpd miniupnpd upnpd rpcbind; do", "for unused_service in dhcpcd dropbear telnetd httpd miniupnpd upnpd rpcbind; do")
replace_once(p, "rc-update add chronyd default\nrc-update add router-applyd default\n", "rc-update add chronyd default\nrc-update add sshd default\nrc-update add router-applyd default\n")

# Firewall: port 22 only on management LAN and authenticated WireGuard.
p = "internal/services/nftables.go"
replace_once(
    p,
    'buf.WriteString(fmt.Sprintf("    iifname \\\"%s\\\" tcp dport %d accept\\n", cfg.WireGuard.Interface, cfg.System.HTTPSPort))\n\t\tbuf.WriteString(fmt.Sprintf("    iifname \\\"%s\\\" udp dport 53 accept\\n", cfg.WireGuard.Interface))',
    'buf.WriteString(fmt.Sprintf("    iifname \\\"%s\\\" tcp dport %d accept\\n", cfg.WireGuard.Interface, cfg.System.HTTPSPort))\n\t\tbuf.WriteString(fmt.Sprintf("    iifname \\\"%s\\\" tcp dport 22 accept\\n", cfg.WireGuard.Interface))\n\t\tbuf.WriteString(fmt.Sprintf("    iifname \\\"%s\\\" udp dport 53 accept\\n", cfg.WireGuard.Interface))',
)
replace_once(
    p,
    'buf.WriteString(fmt.Sprintf("    iifname \\\"%s\\\" tcp dport %d accept\\n", cfg.LAN.Interface, cfg.System.HTTPSPort))\n\t\t} else {',
    'buf.WriteString(fmt.Sprintf("    iifname \\\"%s\\\" tcp dport %d accept\\n", cfg.LAN.Interface, cfg.System.HTTPSPort))\n\t\t\tbuf.WriteString(fmt.Sprintf("    iifname \\\"%s\\\" tcp dport 22 accept\\n", cfg.LAN.Interface))\n\t\t} else {',
)

Path("internal/services/nftables_ssh_recovery_test.go").write_text(
    '''package services

import (
    "strings"
    "testing"

    "github.com/vladimirperovic/minimalrouter/internal/config"
)

func TestRecoverySSHIsManagementPlaneOnly(t *testing.T) {
    cfg := config.DefaultConfig()
    cfg.WAN.Interface = "eth0"
    cfg.WAN.Enabled = true
    cfg.LAN.Interface = "eth1"
    rules, err := GenerateNftables(&cfg)
    if err != nil { t.Fatalf("GenerateNftables: %v", err) }
    if !strings.Contains(rules, `iifname "eth1" tcp dport 22 accept`) { t.Fatal("SSH missing from LAN") }
    if strings.Contains(rules, `iifname "eth0" tcp dport 22 accept`) || strings.Contains(rules, `iifname "ppp*" tcp dport 22 accept`) { t.Fatal("SSH exposed on WAN") }
}

func TestRecoverySSHRespectsWireGuardOnly(t *testing.T) {
    cfg := config.DefaultConfig()
    cfg.WAN.Interface = "eth0"
    cfg.WAN.Enabled = true
    cfg.LAN.Interface = "eth1"
    cfg.System.ManagementAccess = "wireguard_only"
    cfg.WireGuard.Enabled = true
    cfg.WireGuard.Interface = "wg0"
    rules, err := GenerateNftables(&cfg)
    if err != nil { t.Fatalf("GenerateNftables: %v", err) }
    if strings.Contains(rules, `iifname "eth1" tcp dport 22 accept`) { t.Fatal("SSH exposed on LAN in wireguard_only mode") }
    if !strings.Contains(rules, `iifname "wg0" tcp dport 22 accept`) { t.Fatal("SSH missing from WireGuard") }
}
'''
)

# ISO CI assertions.
p = ".github/workflows/iso.yml"
replace_once(
    p,
    "          grep -F 'Minimal Router OS v%s' packaging/alpine/install-console.sh\n",
    "          grep -F 'Minimal Router OS v%s' packaging/alpine/install-console.sh\n          grep -F 'openssh-server' packaging/alpine/build-iso.sh\n          grep -F 'configure_live_ssh' packaging/alpine/live-installer.sh\n",
)
replace_once(p, "          # timeout is expected: the installer is interactive on VGA tty1.\n", "          # timeout is expected: the installer is interactive and the test does not answer prompts.\n")
replace_once(
    p,
    "          grep -F 'Launching Minimal Router OS installer on tty1' build/iso/qemu-boot.log\n",
    "          grep -F 'MinimalRouter installer service started; UI=' build/iso/qemu-boot.log\n          grep -F 'serial=ttyS0@115200' build/iso/qemu-boot.log\n",
)
