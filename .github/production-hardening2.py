from pathlib import Path


def replace_once(path, old, new):
    p = Path(path)
    s = p.read_text()
    n = s.count(old)
    if n != 1:
        raise SystemExit(f"{path}: expected 1 match, found {n}: {old[:100]!r}")
    p.write_text(s.replace(old, new, 1))


def insert_before(path, marker, text):
    p = Path(path)
    s = p.read_text()
    if marker not in s:
        raise SystemExit(f"{path}: marker missing: {marker!r}")
    p.write_text(s.replace(marker, text + marker, 1))

# Network UX: expose MAC identity and require explicit confirmation when the
# installer has no strong PPPoE/default-route/carrier evidence.
p = "cmd/router-setup/main.go"
replace_once(p,
'''\twan, lan, reason := recommendRoles(recommendation, probeResults)
\tfmt.Println()
\tfmt.Printf("Suggested WAN: %s\\n", wan)
\tfmt.Printf("Suggested LAN: %s\\n", lan)
\tfmt.Printf("Reason: %s\\n", reason)
\tpppoeBased := reason == "only this interface answered PPPoE discovery" || strings.HasPrefix(reason, "PPPoE answered on multiple interfaces")
''',
'''\twan, lan, reason := recommendRoles(recommendation, probeResults)
\twanMAC, lanMAC := "unknown", "unknown"
\tstrongSignal := false
\tfor _, item := range recommendation.Interfaces {
\t\tif item.Name == wan {
\t\t\twanMAC = fallback(item.MACAddress, "unknown")
\t\t\tstrongSignal = strongSignal || item.DefaultRoute || item.Carrier
\t\t}
\t\tif item.Name == lan {
\t\t\tlanMAC = fallback(item.MACAddress, "unknown")
\t\t\tstrongSignal = strongSignal || item.Carrier
\t\t}
\t}
\tfmt.Println()
\tfmt.Printf("Suggested WAN: %s  (MAC %s)\\n", wan, wanMAC)
\tfmt.Printf("Suggested LAN: %s  (MAC %s)\\n", lan, lanMAC)
\tfmt.Printf("Reason: %s\\n", reason)
\tpppoeBased := reason == "only this interface answered PPPoE discovery" || strings.HasPrefix(reason, "PPPoE answered on multiple interfaces")
\tlowConfidence := !pppoeBased && !strongSignal
''')
replace_once(p,
'''\tif pppoeBased {
\t\tfmt.Println()
\t\tui.warn("PPPoE discovery only detects that a service answered; it does not start a PPPoE session.")
\t\tui.warn("An existing router or shared upstream can make PPPoE visible during migration.")
\t\tfmt.Println("Please explicitly confirm the WAN/LAN roles; pressing Enter alone will NOT accept this PPPoE-based suggestion.")
\t}

\tuseSuggested, err := ui.confirm("Use these WAN/LAN roles?", !pppoeBased)
''',
'''\tif pppoeBased {
\t\tfmt.Println()
\t\tui.warn("PPPoE discovery only detects that a service answered; it does not start a PPPoE session.")
\t\tui.warn("An existing router or shared upstream can make PPPoE visible during migration.")
\t\tfmt.Println("Please explicitly confirm the WAN/LAN roles; pressing Enter alone will NOT accept this PPPoE-based suggestion.")
\t}
\tif lowConfidence {
\t\tfmt.Println()
\t\tui.warn("The installer does not have enough link evidence to identify WAN/LAN with high confidence.")
\t\tui.warn("Check the MAC addresses against the two Proxmox NICs before confirming.")
\t\tfmt.Println("Please explicitly confirm the roles; pressing Enter alone will NOT accept this low-confidence suggestion.")
\t}

\tuseSuggested, err := ui.confirm("Use these WAN/LAN roles?", !pppoeBased && !lowConfidence)
''')

p = "scripts/ci/iso-full-install.exp"
replace_once(p, 'wait_and_send {Use these WAN/LAN roles} "\\r"', 'wait_and_send {Use these WAN/LAN roles} "y\\r"')

# Live installer safety: resource preflight, repeat-install guard and durable
# install marker.
p = "packaging/alpine/live-installer.sh"
insert_before(p, 'configure_live_ssh() {\n', r'''preflight_host() {
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

''')
replace_once(p,
'''verify_bundle
prepare_packages "$APK_DIR"

# The normal installer owns''',
'''verify_bundle
prepare_packages "$APK_DIR"
preflight_host
guard_existing_install

# The normal installer owns''')
replace_once(p,
'''fi

printf '\\nInstalling Alpine Linux 3.22 + Minimal Router OS v%s to %s...\\n' "$VERSION" "$TARGET"
''',
'''fi

validate_target_disk "$TARGET"
printf '\\nInstalling Alpine Linux 3.22 + minimalrouter v%s to %s...\\n' "$VERSION" "$TARGET"
''')
replace_once(p,
'''mkdir -p /mnt/etc/minimalrouter
printf '%s\\n' "$VERSION" > /mnt/etc/minimalrouter/VERSION
chmod 0644 /mnt/etc/minimalrouter/VERSION
rm -rf "/mnt$TARGET_INSTALLER"
''',
'''mkdir -p /mnt/etc/minimalrouter
printf '%s\\n' "$VERSION" > /mnt/etc/minimalrouter/VERSION
cat > /mnt/etc/minimalrouter/installed <<EOF
version=$VERSION
installed_by=all-in-one-iso
EOF
chmod 0644 /mnt/etc/minimalrouter/VERSION /mnt/etc/minimalrouter/installed
rm -rf "/mnt$TARGET_INSTALLER"
''')
replace_once(p, "printf 'The machine will reboot now. The first boot finalizes Minimal Router OS.\\n'", "printf 'The machine will reboot now. The first boot finalizes minimalrouter.\\n'")
