from pathlib import Path
import runpy


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
        raise SystemExit(f"{path}: start marker missing: {start!r}")
    j = s.find(end, i)
    if j < 0:
        raise SystemExit(f"{path}: end marker missing: {end!r}")
    p.write_text(s[:i] + new + s[j:])


# The marker-based patch intentionally stops on the ttyS0 block in the current
# branch. Everything before that point is deterministic and remains applied in
# this working tree. Continue from the exact known stop point below.
try:
    runpy.run_path('.github/acceptance-fix2.py', run_name='__main__')
except SystemExit as exc:
    if 'ttyS0::respawn' not in str(exc):
        raise

p = 'packaging/alpine/live-installer.sh'
serial_start = "    if [ -c /dev/ttyS0 ] && ! grep -q '^ttyS0::respawn:' /etc/inittab 2>/dev/null; then\n"
serial_end = "\n\n    printf '\\nRecovery access is active on the selected LAN:\\n'"
serial_new = r'''    # Do not spawn a getty on ttyS0 while the serial wizard owns ttyS0.
    if [ "${MINIMALROUTER_INSTALL_TTY:-/dev/tty1}" != "/dev/ttyS0" ] \
       && [ -c /dev/ttyS0 ] \
       && ! grep -q '^ttyS0::respawn:' /etc/inittab 2>/dev/null; then
        printf '%s\n' 'ttyS0::respawn:/sbin/getty -L ttyS0 115200 vt100' >> /etc/inittab
        grep -qxF ttyS0 /etc/securetty 2>/dev/null || printf '%s\n' ttyS0 >> /etc/securetty
        kill -HUP 1 2>/dev/null || true
    fi'''
between(p, serial_start, serial_end, serial_new)

one(p, "printf '\\nRecovery console password\\n'", "printf '\\nRecovery / SSH root password\\n'")
one(p, "printf '%s\\n' '-------------------------'", "printf '%s\\n' '----------------------------'")
one(p,
    "printf 'Set the local Linux root password used only for console recovery.\\n'",
    "printf 'Set the Linux root password used for emergency console and trusted-LAN SSH recovery.\\n'")
one(p,
    "printf 'It is separate from the Web Dashboard administrator password.\\n\\n'",
    "printf 'It is separate from the Web Dashboard administrator password and is never exposed on WAN.\\n\\n'")

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

p = 'scripts/ci/iso-full-install.exp'
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
