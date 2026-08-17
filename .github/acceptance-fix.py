from pathlib import Path


def replace_once(path, old, new):
    p = Path(path)
    text = p.read_text()
    n = text.count(old)
    if n != 1:
        raise SystemExit(f"{path}: expected one match, found {n}: {old[:100]!r}")
    p.write_text(text.replace(old, new, 1))


# ISO: use Alpine Extended for reliable offline sys installs; keep the live
# console quiet; make VGA and serial explicit alternatives.
p = "packaging/alpine/build-iso.sh"
replace_once(p,
'''# Remasters the verified Alpine 3.22 standard ISO, adds a signed Alpine package
# bundle, the MinimalRouter distribution and an apkovl that starts the installer.''',
'''# Remasters the verified Alpine 3.22 Extended ISO, adds a signed Alpine package
# bundle, the MinimalRouter distribution and an apkovl that starts the installer.
# Extended is deliberate: the appliance must be able to install a complete
# Alpine system to disk without depending on an Internet package repository.''')
replace_once(p,
'ALPINE_ISO_NAME="alpine-standard-${ALPINE_VERSION}-${ALPINE_ARCH}.iso"',
'ALPINE_ISO_NAME="alpine-extended-${ALPINE_VERSION}-${ALPINE_ARCH}.iso"')
replace_once(p,
'''    ebegin "Launching Minimal Router OS installer on ${INSTALL_TTY#/dev/}"

    # The installer owns only its selected TTY. The other console remains a''',
'''    ebegin "Launching Minimal Router OS installer on ${INSTALL_TTY#/dev/}"

    # Kernel diagnostics stay available in dmesg, but must never overwrite a
    # prompt while a person is typing in noVNC or on ttyS0.
    dmesg -n 1 >/dev/null 2>&1 || true

    # The installer owns only its selected TTY. The other console remains a''')
replace_once(p,
'''    (
        exec <"$INSTALL_TTY" >"$INSTALL_TTY" 2>&1
        exec /etc/minimalrouter/live-installer.sh
    ) &''',
'''    (
        export MINIMALROUTER_INSTALL_TTY="$INSTALL_TTY"
        exec <"$INSTALL_TTY" >"$INSTALL_TTY" 2>&1
        exec /etc/minimalrouter/live-installer.sh
    ) &''')
replace_once(p,
'  APPEND modules=loop,squashfs,sd-mod,usb-storage modloop=/boot/modloop-lts console=tty0 console=ttyS0,115200',
'  APPEND modules=loop,squashfs,sd-mod,usb-storage modloop=/boot/modloop-lts quiet loglevel=1 console=tty0')
replace_once(p,
'  APPEND modules=loop,squashfs,sd-mod,usb-storage modloop=/boot/modloop-lts minimalrouter.console=ttyS0 console=tty0 console=ttyS0,115200',
'  APPEND modules=loop,squashfs,sd-mod,usb-storage modloop=/boot/modloop-lts quiet loglevel=1 minimalrouter.console=ttyS0 console=ttyS0,115200')
replace_once(p,
'linux\t/boot/vmlinuz-lts modules=loop,squashfs,sd-mod,usb-storage console=tty0 console=ttyS0,115200',
'linux\t/boot/vmlinuz-lts modules=loop,squashfs,sd-mod,usb-storage quiet loglevel=1 console=tty0')
replace_once(p,
'linux\t/boot/vmlinuz-lts modules=loop,squashfs,sd-mod,usb-storage minimalrouter.console=ttyS0 console=tty0 console=ttyS0,115200',
'linux\t/boot/vmlinuz-lts modules=loop,squashfs,sd-mod,usb-storage quiet loglevel=1 minimalrouter.console=ttyS0 console=ttyS0,115200')
replace_once(p,
'echo "[2/7] Downloading Alpine Linux ${ALPINE_VERSION} standard ISO..."',
'echo "[2/7] Downloading Alpine Linux ${ALPINE_VERSION} Extended ISO..."')

# Friendly appliance entry: logo immediately, no extra gate before setup.
p = "packaging/alpine/install-console.sh"
old_start = '''show_welcome() {
    command -v clear >/dev/null 2>&1 && clear || true
    cat <<'ASCII'
 __  __ _       _                 _ ____             _
|  \\/  (_)_ __ (_)_ __ ___   __ _| |  _ \\ ___  _   _| |_ ___ _ __
| |\\/| | | '_ \\| | '_ ` _ \\ / _` | | |_) / _ \\| | | | __/ _ \\ '__|
| |  | | | | | | | | | | | | | (_| | |  _ < (_) | |_| | ||  __/ |
|_|  |_|_|_| |_|_|_| |_| |_|\\__,_|_|_| \\_\\___/ \\__,_|\\__\\___|_|
ASCII
    printf '\\nMinimal Router OS v%s\\n' "$MR_VERSION"
    printf 'Welcome to Minimal Router OS.\\n\\n'
    cat <<'EOF'
Before you continue
-------------------
If you are installing on Proxmox VE, you should already have:
  - a QEMU/KVM virtual machine (not an LXC container);
  - at least 1 vCPU, 1 GiB RAM and an 8 GiB virtual disk;
  - CPU type "host" recommended for a fixed home/lab node;
  - two network adapters (VirtIO is recommended);
  - one adapter connected to the WAN bridge leading to the ISP modem/ONT;
  - one adapter connected to an isolated LAN bridge for your clients;
  - working Proxmox console access and a rollback/snapshot path.

Network preparation:
  - the ISP modem/ONT must expose PPPoE to the WAN adapter using bridge or
    pass-through mode;
  - have the PPPoE username and password ready;
  - keep your previous router available until this installation is verified;
  - do not connect the new MinimalRouter LAN to the same broadcast domain as
    another active DHCP server during the first installation.

This ISO already contains Alpine Linux, the linux-lts kernel, the required
router packages, MinimalRouter and the Web Dashboard. You do not need to
install Alpine separately.

The installer will test the network adapters for PPPoE, propose WAN/LAN roles,
and ask you to confirm or change them. PPPoE credentials and the dashboard
administrator password are entered locally and are never echoed on screen.
EOF
    printf '\\nPress Enter to continue, or Ctrl+C to abort. '
    IFS= read -r _mr_continue
    printf '\\n'
}
'''
new_start = '''show_welcome() {
    command -v clear >/dev/null 2>&1 && clear || true
    cat <<'ASCII'
 __  __ _       _                 _ ____             _
|  \\/  (_)_ __ (_)_ __ ___   __ _| |  _ \\ ___  _   _| |_ ___ _ __
| |\\/| | | '_ \\| | '_ ` _ \\ / _` | | |_) / _ \\| | | | __/ _ \\ '__|
| |  | | | | | | | | | | | | | (_| | |  _ < (_) | |_| | ||  __/ |
|_|  |_|_|_| |_|_|_| |_| |_|\\__,_|_|_| \\_\\___/ \\__,_|\\__\\___|_|
ASCII
    printf '\\nMinimal Router OS v%s\\n' "$MR_VERSION"
    printf 'First-run setup starts automatically.\\n\\n'
    cat <<'EOF'
You should already have:
  - a Proxmox QEMU/KVM VM with two network adapters and an 8 GiB+ disk;
  - WAN connected toward the modem/ONT and LAN connected to your private bridge;
  - your PPPoE username/password if your ISP uses PPPoE (you may skip it);
  - your old router available until this new installation is tested.

How to answer:
  - when a suggested choice is correct, press Enter to accept it;
  - if WAN/LAN detection is uncertain, the installer will explain what to choose;
  - passwords are required and are never shown while you type;
  - on a normal one-disk Proxmox VM, the attached VM disk is installed automatically;
  - on unusual hardware or multi-disk systems, a safety confirmation is required.

This ISO already includes Alpine Linux and everything MinimalRouter needs.
EOF
    printf '\\nStarting setup...\\n\\n'
}
'''
replace_once(p, old_start, new_start)
replace_once(p,
'    "$SETUP_BIN" collect --output "$PROVISION_FILE" --data-dir /var/lib/minimalrouter',
'    MINIMALROUTER_WELCOME_SHOWN=1 "$SETUP_BIN" collect --output "$PROVISION_FILE" --data-dir /var/lib/minimalrouter')

# Avoid a second logo and make default behavior explicit to non-technical users.
p = "cmd/router-setup/main.go"
replace_once(p,
'''\tfmt.Println()
\tprintBanner()
\tfmt.Println()
''',
'''\tfmt.Println()
\tif os.Getenv("MINIMALROUTER_WELCOME_SHOWN") != "1" {
\t\tprintBanner()
\t\tfmt.Println()
\t}
''')
replace_once(p,
'''\tfmt.Println("What happens now:")
\tfmt.Println("  1. PPPoE credentials — leave empty to configure them later in the Web Dashboard")
\tfmt.Println("  2. WAN/LAN roles — WAN faces the ISP, LAN faces your clients (auto-assigned with two adapters)")
\tfmt.Println("  3. Dashboard administrator password (minimum 12 characters)")
\tfmt.Println("  4. Recovery console password — used only for local console recovery")
\tfmt.Println("  5. A final summary — nothing is applied before you confirm it")
\tfmt.Println()
\tfmt.Println("Nothing is committed until you review and confirm the final summary.")
''',
'''\tfmt.Println("Setup asks only for what it needs:")
\tfmt.Println("  1. PPPoE — press Enter to skip it if you do not want to configure it now")
\tfmt.Println("  2. WAN/LAN — the installer suggests the likely ports; press Enter if correct")
\tfmt.Println("  3. Dashboard password — required, minimum 12 characters")
\tfmt.Println("  4. A final review before the configuration is saved")
\tfmt.Println()
\tfmt.Println("Tip: [Y/n] means Yes is the default; just press Enter to accept it.")
''')

# Add tests proving Enter/default and invalid-answer behavior.
p = "cmd/router-setup/main_test.go"
s = Path(p).read_text()
if "TestConfirmEnterAcceptsDefaultYes" not in s:
    s = s.replace('import (\n', 'import (\n\t"bufio"\n', 1)
    s = s.replace('\t"path/filepath"\n', '\t"path/filepath"\n\t"strings"\n', 1)
    s += r'''

func TestConfirmEnterAcceptsDefaultYes(t *testing.T) {
    ui := &consoleUI{reader: bufio.NewReader(strings.NewReader("\n"))}
    got, err := ui.confirm("Continue?", true)
    if err != nil { t.Fatal(err) }
    if !got { t.Fatal("empty input must accept default yes") }
}

func TestConfirmEnterAcceptsDefaultNo(t *testing.T) {
    ui := &consoleUI{reader: bufio.NewReader(strings.NewReader("\n"))}
    got, err := ui.confirm("Continue?", false)
    if err != nil { t.Fatal(err) }
    if got { t.Fatal("empty input must accept default no") }
}

func TestConfirmExplicitNoOverridesDefaultYes(t *testing.T) {
    ui := &consoleUI{reader: bufio.NewReader(strings.NewReader("n\n"))}
    got, err := ui.confirm("Continue?", true)
    if err != nil { t.Fatal(err) }
    if got { t.Fatal("explicit no must override default yes") }
}

func TestConfirmInvalidAnswerReprompts(t *testing.T) {
    ui := &consoleUI{reader: bufio.NewReader(strings.NewReader("maybe\n\n"))}
    got, err := ui.confirm("Continue?", true)
    if err != nil { t.Fatal(err) }
    if !got { t.Fatal("empty input after invalid answer must accept default yes") }
}
'''
    Path(p).write_text(s)

# Recovery, VM-safe automatic disk selection, and serial race prevention.
p = "packaging/alpine/live-installer.sh"
insert_marker = '''configure_live_ssh() {
'''
insert_text = r'''is_qemu_vm() {
    for f in /sys/class/dmi/id/sys_vendor /sys/class/dmi/id/product_name /sys/class/dmi/id/board_vendor; do
        [ -r "$f" ] || continue
        if grep -Eiq 'qemu|kvm|proxmox' "$f" 2>/dev/null; then
            return 0
        fi
    done
    return 1
}

safe_auto_vm_disk() {
    [ "$COUNT" -eq 1 ] || return 1
    is_qemu_vm || return 1
    candidate="$(printf '%s\\n' "$CANDIDATES" | awk 'NF {print; exit}')"
    [ -b "$candidate" ] || return 1
    case "$candidate" in
        /dev/vd*|/dev/sd*|/dev/nvme*) ;;
        *) return 1 ;;
    esac
    printf '%s\\n' "$candidate"
}

'''
text = Path(p).read_text()
if "safe_auto_vm_disk()" not in text:
    if insert_marker not in text:
        raise SystemExit(f"{p}: live ssh marker missing")
    text = text.replace(insert_marker, insert_text + insert_marker, 1)
    Path(p).write_text(text)
replace_once(p,
'''    if [ -c /dev/ttyS0 ] && ! grep -q '^ttyS0::respawn:' /etc/inittab 2>/dev/null; then
        printf '%s\\n' 'ttyS0::respawn:/sbin/getty -L ttyS0 115200 vt100' >> /etc/inittab
        grep -qxF ttyS0 /etc/securetty 2>/dev/null || printf '%s\\n' ttyS0 >> /etc/securetty
        kill -HUP 1 2>/dev/null || true
    fi
''',
'''    # ttyS0 is an independent recovery login only when the wizard is on VGA.
    # On the dedicated serial installer path, spawning getty would race the
    # wizard for the same keystrokes.
    if [ "${MINIMALROUTER_INSTALL_TTY:-/dev/tty1}" != "/dev/ttyS0" ] \\
       && [ -c /dev/ttyS0 ] \\
       && ! grep -q '^ttyS0::respawn:' /etc/inittab 2>/dev/null; then
        printf '%s\\n' 'ttyS0::respawn:/sbin/getty -L ttyS0 115200 vt100' >> /etc/inittab
        grep -qxF ttyS0 /etc/securetty 2>/dev/null || printf '%s\\n' ttyS0 >> /etc/securetty
        kill -HUP 1 2>/dev/null || true
    fi
''')
replace_once(p, "printf '\\nRecovery console password\\n'", "printf '\\nRecovery / SSH root password\\n'")
replace_once(p, "printf '%s\\n' '-------------------------'", "printf '%s\\n' '----------------------------'")
replace_once(p,
"printf 'Set the local Linux root password used only for console recovery.\\n'",
"printf 'Set the Linux root password used for emergency console and trusted-LAN SSH recovery.\\n'")
replace_once(p,
"printf 'It is separate from the Web Dashboard administrator password.\\n\\n'",
"printf 'It is separate from the Web Dashboard administrator password and is never exposed on WAN.\\n\\n'")
old_disk = '''show_disk_table
DEFAULT_DISK=""
COUNT="$(printf '%s\\n' "$CANDIDATES" | awk 'NF {n++} END {print n+0}')"
[ "$COUNT" -eq 1 ] && DEFAULT_DISK="$(printf '%s\\n' "$CANDIDATES" | head -n 1)"

while :; do
    if [ -n "$DEFAULT_DISK" ]; then
        printf 'Install Minimal Router OS v%s to disk [%s]: ' "$VERSION" "$DEFAULT_DISK"
    else
        printf 'Install Minimal Router OS v%s to disk: ' "$VERSION"
    fi
    IFS= read -r TARGET
    [ -n "$TARGET" ] || TARGET="$DEFAULT_DISK"
    if printf '%s\\n' "$CANDIDATES" | grep -qxF "$TARGET"; then
        break
    fi
    printf 'Please choose one of the listed installation disks.\\n'
done

printf '\\nSelected disk: %s\\n' "$TARGET"
lsblk "$TARGET" 2>/dev/null || true
printf '\\nWARNING: every partition and all data on %s will be erased.\\n' "$TARGET"
printf 'Type ERASE to continue: '
IFS= read -r CONFIRM
case "$CONFIRM" in
    [Ee][Rr][Aa][Ss][Ee]) ;;
    *) fail "Disk installation was cancelled" ;;
esac
'''
new_disk = '''show_disk_table
DEFAULT_DISK=""
COUNT="$(printf '%s\\n' "$CANDIDATES" | awk 'NF {n++} END {print n+0}')"
[ "$COUNT" -eq 1 ] && DEFAULT_DISK="$(printf '%s\\n' "$CANDIDATES" | head -n 1)"

TARGET="$(safe_auto_vm_disk 2>/dev/null || true)"
if [ -n "$TARGET" ]; then
    printf '\\nProxmox/QEMU VM detected.\\n'
    printf 'Using the only attached installation disk automatically: %s\\n' "$TARGET"
    printf 'Only disks visible inside this VM are considered.\\n'
else
    while :; do
        if [ -n "$DEFAULT_DISK" ]; then
            printf 'Install Minimal Router OS v%s to disk [%s]: ' "$VERSION" "$DEFAULT_DISK"
        else
            printf 'Install Minimal Router OS v%s to disk: ' "$VERSION"
        fi
        IFS= read -r TARGET
        [ -n "$TARGET" ] || TARGET="$DEFAULT_DISK"
        if printf '%s\\n' "$CANDIDATES" | grep -qxF "$TARGET"; then
            break
        fi
        printf 'Please choose one of the listed installation disks.\\n'
    done

    printf '\\nSelected disk: %s\\n' "$TARGET"
    lsblk "$TARGET" 2>/dev/null || true
    printf '\\nThis is not a simple one-disk Proxmox/QEMU layout.\\n'
    printf 'Every partition and all data on %s will be erased.\\n' "$TARGET"
    printf 'Type ERASE to continue: '
    IFS= read -r CONFIRM
    case "$CONFIRM" in
        [Ee][Rr][Aa][Ss][Ee]) ;;
        *) fail "Disk installation was cancelled" ;;
    esac
fi
'''
replace_once(p, old_disk, new_disk)

# CI full install: setup starts immediately and a one-disk QEMU guest auto-selects
# its disk, so no Enter gate / disk prompt / ERASE response exists anymore.
p = "scripts/ci/iso-full-install.exp"
replace_once(p, 'wait_and_send {Press Enter to continue} "\\r"\n', '')
replace_once(p,
'''wait_and_send {Install Minimal Router OS.*to disk.*vda} "\\r"
wait_and_send {Type ERASE to continue} "ERASE\\r"
''',
'''expect {
    -re {Using the only attached installation disk automatically: /dev/vda} {}
    timeout { puts stderr "timeout waiting for safe VM auto-disk selection"; exit 26 }
    eof { puts stderr "QEMU exited before VM auto-disk selection"; exit 27 }
}
''')

# CI workflow should assert the new entry behavior and Extended base image.
p = ".github/workflows/iso.yml"
replace_once(p,
"          grep -F 'Press Enter to continue' packaging/alpine/install-console.sh\n",
"          ! grep -F 'Press Enter to continue' packaging/alpine/install-console.sh\n          grep -F 'First-run setup starts automatically' packaging/alpine/install-console.sh\n          grep -F 'alpine-extended-' packaging/alpine/build-iso.sh\n          grep -F 'dmesg -n 1' packaging/alpine/build-iso.sh\n          grep -F 'safe_auto_vm_disk' packaging/alpine/live-installer.sh\n")
