from pathlib import Path


def one(path, old, new):
    p = Path(path)
    s = p.read_text()
    if s.count(old) != 1:
        raise SystemExit(f"{path}: expected one match, found {s.count(old)}: {old[:100]!r}")
    p.write_text(s.replace(old, new, 1))


def between(path, start, end, new):
    p = Path(path)
    s = p.read_text()
    i = s.find(start)
    if i < 0:
        raise SystemExit(f"{path}: start marker missing")
    j = s.find(end, i)
    if j < 0:
        raise SystemExit(f"{path}: end marker missing")
    p.write_text(s[:i] + new + s[j:])


# Alpine Extended + clean explicit VGA/serial consoles.
p = "packaging/alpine/build-iso.sh"
one(p, "# Remasters the verified Alpine 3.22 standard ISO, adds a signed Alpine package\n# bundle, the MinimalRouter distribution and an apkovl that starts the installer.",
       "# Remasters the verified Alpine 3.22 Extended ISO, adds a signed Alpine package\n# bundle, the MinimalRouter distribution and an apkovl that starts the installer.\n# Extended is used so a complete Alpine system can be installed offline.")
one(p, 'ALPINE_ISO_NAME="alpine-standard-${ALPINE_VERSION}-${ALPINE_ARCH}.iso"', 'ALPINE_ISO_NAME="alpine-extended-${ALPINE_VERSION}-${ALPINE_ARCH}.iso"')
one(p, '    ebegin "Launching Minimal Router OS installer on ${INSTALL_TTY#/dev/}"\n\n    # The installer owns only its selected TTY.',
       '    ebegin "Launching Minimal Router OS installer on ${INSTALL_TTY#/dev/}"\n\n    # Keep prompts readable in noVNC/serial. Diagnostics remain in dmesg.\n    dmesg -n 1 >/dev/null 2>&1 || true\n\n    # The installer owns only its selected TTY.')
one(p, '    (\n        exec <"$INSTALL_TTY" >"$INSTALL_TTY" 2>&1\n        exec /etc/minimalrouter/live-installer.sh\n    ) &',
       '    (\n        export MINIMALROUTER_INSTALL_TTY="$INSTALL_TTY"\n        exec <"$INSTALL_TTY" >"$INSTALL_TTY" 2>&1\n        exec /etc/minimalrouter/live-installer.sh\n    ) &')
one(p, '  APPEND modules=loop,squashfs,sd-mod,usb-storage modloop=/boot/modloop-lts console=tty0 console=ttyS0,115200',
       '  APPEND modules=loop,squashfs,sd-mod,usb-storage modloop=/boot/modloop-lts quiet loglevel=1 console=tty0')
one(p, '  APPEND modules=loop,squashfs,sd-mod,usb-storage modloop=/boot/modloop-lts minimalrouter.console=ttyS0 console=tty0 console=ttyS0,115200',
       '  APPEND modules=loop,squashfs,sd-mod,usb-storage modloop=/boot/modloop-lts quiet loglevel=1 minimalrouter.console=ttyS0 console=ttyS0,115200')
one(p, 'linux\t/boot/vmlinuz-lts modules=loop,squashfs,sd-mod,usb-storage console=tty0 console=ttyS0,115200',
       'linux\t/boot/vmlinuz-lts modules=loop,squashfs,sd-mod,usb-storage quiet loglevel=1 console=tty0')
one(p, 'linux\t/boot/vmlinuz-lts modules=loop,squashfs,sd-mod,usb-storage minimalrouter.console=ttyS0 console=tty0 console=ttyS0,115200',
       'linux\t/boot/vmlinuz-lts modules=loop,squashfs,sd-mod,usb-storage quiet loglevel=1 minimalrouter.console=ttyS0 console=ttyS0,115200')
one(p, 'echo "[2/7] Downloading Alpine Linux ${ALPINE_VERSION} standard ISO..."', 'echo "[2/7] Downloading Alpine Linux ${ALPINE_VERSION} Extended ISO..."')

# Immediate friendly first screen; no extra Enter before setup.
p = "packaging/alpine/install-console.sh"
welcome = r'''show_welcome() {
    command -v clear >/dev/null 2>&1 && clear || true
    cat <<'ASCII'
 __  __ _       _                 _ ____             _
|  \/  (_)_ __ (_)_ __ ___   __ _| |  _ \ ___  _   _| |_ ___ _ __
| |\/| | | '_ \| | '_ ` _ \ / _` | | |_) / _ \| | | | __/ _ \ '__|
| |  | | | | | | | | | | | | (_| | |  _ < (_) | |_| | ||  __/ |
|_|  |_|_|_| |_|_|_| |_| |_|\__,_|_|_| \_\___/ \__,_|\__\___|_|
ASCII
    printf '\nMinimal Router OS v%s\n' "$MR_VERSION"
    printf 'First-run setup starts automatically.\n\n'
    cat <<'EOF'
You should already have:
  - a Proxmox QEMU/KVM VM with two network adapters and an 8 GiB+ disk;
  - WAN connected toward the modem/ONT and LAN connected to your private bridge;
  - your PPPoE username/password if your ISP uses PPPoE (you may skip it);
  - your old router available until this new installation is tested.

How to answer:
  - when a suggested choice is correct, press Enter to accept it;
  - if WAN/LAN detection is uncertain, the installer explains what to choose;
  - passwords are required and are never shown while you type;
  - on a normal one-disk Proxmox VM, the attached VM disk installs automatically;
  - unusual hardware or multi-disk systems keep an explicit safety confirmation.

This ISO already includes Alpine Linux and everything MinimalRouter needs.
EOF
    printf '\nStarting setup...\n\n'
}

'''
between(p, 'show_welcome() {\n', '[ -x "$SETUP_BIN" ]', welcome)
one(p, '    "$SETUP_BIN" collect --output "$PROVISION_FILE" --data-dir /var/lib/minimalrouter',
       '    MINIMALROUTER_WELCOME_SHOWN=1 "$SETUP_BIN" collect --output "$PROVISION_FILE" --data-dir /var/lib/minimalrouter')

# One banner, concise prompts, explicit meaning of Enter.
p = "cmd/router-setup/main.go"
one(p, '\tfmt.Println()\n\tprintBanner()\n\tfmt.Println()\n',
       '\tfmt.Println()\n\tif os.Getenv("MINIMALROUTER_WELCOME_SHOWN") != "1" {\n\t\tprintBanner()\n\t\tfmt.Println()\n\t}\n')
one(p,
'''\tfmt.Println("What happens now:")
\tfmt.Println("  1. PPPoE credentials — leave empty to configure them later in the Web Dashboard")
\tfmt.Println("  2. WAN/LAN roles — WAN faces the ISP, LAN faces your clients (auto-assigned with two adapters)")
\tfmt.Println("  3. Dashboard administrator password (minimum 12 characters)")
\tfmt.Println("  4. Recovery console password — used only for local console recovery")
\tfmt.Println("  5. A final summary — nothing is applied before you confirm it")
\tfmt.Println()
\tfmt.Println("Nothing is committed until you review and confirm the final summary.")''',
'''\tfmt.Println("Setup asks only for what it needs:")
\tfmt.Println("  1. PPPoE — press Enter to skip it if you do not want to configure it now")
\tfmt.Println("  2. WAN/LAN — the installer suggests the likely ports; press Enter if correct")
\tfmt.Println("  3. Dashboard password — required, minimum 12 characters")
\tfmt.Println("  4. A final review before the configuration is saved")
\tfmt.Println()
\tfmt.Println("Tip: [Y/n] means Yes is the default; just press Enter to accept it.")''')

# Unit tests for Enter/default semantics.
p = "cmd/router-setup/main_test.go"
s = Path(p).read_text()
if "TestConfirmEnterAcceptsDefaultYes" not in s:
    s = s.replace('import (\n', 'import (\n\t"bufio"\n', 1)
    s = s.replace('\t"path/filepath"\n', '\t"path/filepath"\n\t"strings"\n', 1)
    s += r'''

func TestConfirmEnterAcceptsDefaultYes(t *testing.T) {
    ui := &consoleUI{reader: bufio.NewReader(strings.NewReader("\n"))}
    got, err := ui.confirm("Continue?", true)
    if err != nil || !got { t.Fatalf("got %v, %v; want true, nil", got, err) }
}

func TestConfirmEnterAcceptsDefaultNo(t *testing.T) {
    ui := &consoleUI{reader: bufio.NewReader(strings.NewReader("\n"))}
    got, err := ui.confirm("Continue?", false)
    if err != nil || got { t.Fatalf("got %v, %v; want false, nil", got, err) }
}

func TestConfirmExplicitNoOverridesDefaultYes(t *testing.T) {
    ui := &consoleUI{reader: bufio.NewReader(strings.NewReader("n\n"))}
    got, err := ui.confirm("Continue?", true)
    if err != nil || got { t.Fatalf("got %v, %v; want false, nil", got, err) }
}

func TestConfirmInvalidAnswerReprompts(t *testing.T) {
    ui := &consoleUI{reader: bufio.NewReader(strings.NewReader("maybe\n\n"))}
    got, err := ui.confirm("Continue?", true)
    if err != nil || !got { t.Fatalf("got %v, %v; want true, nil", got, err) }
}
'''
    Path(p).write_text(s)

# VM-aware disk behavior and recovery correctness.
p = "packaging/alpine/live-installer.sh"
s = Path(p).read_text()
if "safe_auto_vm_disk()" not in s:
    marker = "configure_live_ssh() {\n"
    helper = r'''is_qemu_vm() {
    for f in /sys/class/dmi/id/sys_vendor /sys/class/dmi/id/product_name /sys/class/dmi/id/board_vendor; do
        [ -r "$f" ] || continue
        grep -Eiq 'qemu|kvm|proxmox' "$f" 2>/dev/null && return 0
    done
    return 1
}

safe_auto_vm_disk() {
    [ "$COUNT" -eq 1 ] || return 1
    is_qemu_vm || return 1
    candidate="$(printf '%s\n' "$CANDIDATES" | awk 'NF {print; exit}')"
    [ -b "$candidate" ] || return 1
    case "$candidate" in /dev/vd*|/dev/sd*|/dev/nvme*) ;; *) return 1 ;; esac
    printf '%s\n' "$candidate"
}

'''
    if marker not in s:
        raise SystemExit(f"{p}: live SSH marker missing")
    Path(p).write_text(s.replace(marker, helper + marker, 1))
one(p,
'''    if [ -c /dev/ttyS0 ] && ! grep -q '^ttyS0::respawn:' /etc/inittab 2>/dev/null; then
        printf '%s\n' 'ttyS0::respawn:/sbin/getty -L ttyS0 115200 vt100' >> /etc/inittab
        grep -qxF ttyS0 /etc/securetty 2>/dev/null || printf '%s\n' ttyS0 >> /etc/securetty
        kill -HUP 1 2>/dev/null || true
    fi''',
'''    # Do not spawn a getty on ttyS0 while the serial wizard owns ttyS0.
    if [ "${MINIMALROUTER_INSTALL_TTY:-/dev/tty1}" != "/dev/ttyS0" ] \
       && [ -c /dev/ttyS0 ] \
       && ! grep -q '^ttyS0::respawn:' /etc/inittab 2>/dev/null; then
        printf '%s\n' 'ttyS0::respawn:/sbin/getty -L ttyS0 115200 vt100' >> /etc/inittab
        grep -qxF ttyS0 /etc/securetty 2>/dev/null || printf '%s\n' ttyS0 >> /etc/securetty
        kill -HUP 1 2>/dev/null || true
    fi''')
one(p, "printf '\\nRecovery console password\\n'", "printf '\\nRecovery / SSH root password\\n'")
one(p, "printf '%s\\n' '-------------------------'", "printf '%s\\n' '----------------------------'")
one(p, "printf 'Set the local Linux root password used only for console recovery.\\n'", "printf 'Set the Linux root password used for emergency console and trusted-LAN SSH recovery.\\n'")
one(p, "printf 'It is separate from the Web Dashboard administrator password.\\n\\n'", "printf 'It is separate from the Web Dashboard administrator password and is never exposed on WAN.\\n\\n'")

start = 'show_disk_table\nDEFAULT_DISK=""\n'
end = "printf '\\nInstalling Alpine Linux 3.22 + Minimal Router OS v%s to %s...\\n'"
disk_block = r'''show_disk_table
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

'''
between(p, start, end, disk_block)

# CI drives the exact serial wizard: no gate and no ERASE on one-disk QEMU.
p = "scripts/ci/iso-full-install.exp"
one(p, 'wait_and_send {Press Enter to continue} "\\r"\n', '')
one(p,
'''wait_and_send {Install Minimal Router OS.*to disk.*vda} "\r"
wait_and_send {Type ERASE to continue} "ERASE\r"
''',
'''expect {
    -re {Using the only attached installation disk automatically: /dev/vda} {}
    timeout { puts stderr "timeout waiting for safe VM auto-disk selection"; exit 26 }
    eof { puts stderr "QEMU exited before VM auto-disk selection"; exit 27 }
}
''')
