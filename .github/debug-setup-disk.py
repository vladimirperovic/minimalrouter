from pathlib import Path

p = Path('packaging/alpine/live-installer.sh')
s = p.read_text()
old = '''# The operator has already explicitly typed ERASE for this exact device, so the
# setup-disk confirmation can safely be suppressed here.
if ! ERASE_DISKS="$TARGET" SWAP_SIZE=0 setup-disk -m sys -k lts -s 0 "$TARGET"; then
    fail "Alpine system-disk installation failed"
fi
'''
new = '''# The target has either passed the conservative virtual-disk guard or the
# operator explicitly confirmed it. Capture Alpine's verbose installer output:
# if setup-disk ever fails, the ISO/CI log must contain the actual reason rather
# than only a generic MinimalRouter error.
SETUP_DISK_LOG=/tmp/minimalrouter-setup-disk.log
rm -f "$SETUP_DISK_LOG"
if ! ERASE_DISKS="$TARGET" SWAP_SIZE=0 setup-disk -v -m sys -k lts -s 0 "$TARGET" >"$SETUP_DISK_LOG" 2>&1; then
    printf '\\n--- Alpine setup-disk diagnostic log ---\\n' >&2
    cat "$SETUP_DISK_LOG" >&2 || true
    printf '%s\\n' '--- installer environment ---' >&2
    printf 'kernel: %s\\n' "$(uname -r)" >&2
    printf 'target: %s\\n' "$TARGET" >&2
    printf 'repositories:\\n' >&2
    cat /etc/apk/repositories >&2 2>/dev/null || true
    printf 'target disk:\\n' >&2
    lsblk -f "$TARGET" >&2 2>/dev/null || true
    printf 'required tools:\\n' >&2
    for tool in setup-disk sfdisk mkfs.ext4 extlinux grub-install; do
        command -v "$tool" >&2 2>/dev/null || printf 'missing: %s\\n' "$tool" >&2
    done
    printf '%s\\n' '--- end setup-disk diagnostics ---' >&2
    fail "Alpine system-disk installation failed"
fi
'''
if s.count(old) != 1:
    raise SystemExit(f'expected one setup-disk block, found {s.count(old)}')
p.write_text(s.replace(old, new, 1))
