from pathlib import Path
import re

p = Path('packaging/alpine/live-installer.sh')
s = p.read_text()
pattern = re.compile(r'''safe_auto_vm_disk\(\) \{\n.*?\n\}\n\nconfigure_live_ssh\(\) \{''', re.S)
replacement = r'''safe_auto_vm_disk() {
    [ "$COUNT" -eq 1 ] || return 1
    is_qemu_vm || return 1

    candidate="$(printf '%s\n' "$CANDIDATES" | awk 'NF {print; exit}')"
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
            printf '%s\n' "$identity" | grep -Eiq 'qemu|virtio|virtual' || return 1
            ;;
        *) return 1 ;;
    esac

    printf '%s\n' "$candidate"
}

configure_live_ssh() {'''
s2, n = pattern.subn(replacement, s, count=1)
if n != 1:
    raise SystemExit(f'expected one safe_auto_vm_disk block, found {n}')
p.write_text(s2)
