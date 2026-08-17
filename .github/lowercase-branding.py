from pathlib import Path


def replace_between(path, start, end, replacement):
    p = Path(path)
    s = p.read_text()
    i = s.find(start)
    if i < 0:
        raise SystemExit(f"{path}: start marker missing")
    j = s.find(end, i)
    if j < 0:
        raise SystemExit(f"{path}: end marker missing")
    p.write_text(s[:i] + replacement + s[j:])


def replace_once(path, old, new):
    p = Path(path)
    s = p.read_text()
    n = s.count(old)
    if n != 1:
        raise SystemExit(f"{path}: expected one match, got {n}: {old[:80]!r}")
    p.write_text(s.replace(old, new, 1))

p = "packaging/alpine/install-console.sh"
welcome = r'''show_welcome() {
    command -v clear >/dev/null 2>&1 && clear || true
    cat <<'ASCII'
+----------------------------------------------------------+
|                      minimalrouter                       |
+----------------------------------------------------------+
ASCII
    printf '\nminimalrouter v%s\n' "$MR_VERSION"
    printf 'first-run setup starts automatically\n\n'
    cat <<'EOF'
before we begin
---------------
[ ] Proxmox QEMU/KVM VM with at least 1 GiB RAM and an 8 GiB disk
[ ] two network adapters: one toward the modem/ONT, one toward your LAN
[ ] PPPoE username/password if your ISP uses PPPoE (optional during setup)
[ ] your previous router kept available until this installation is tested

how this works
--------------
- the installer checks the VM, network adapters and installation disk
- safe suggestions can be accepted with Enter
- uncertain WAN/LAN choices require an explicit confirmation
- passwords are required and never shown while you type
- a normal one-disk Proxmox VM installs to its virtual disk automatically
- multi-disk or unusual hardware keeps an extra safety confirmation

Alpine Linux and everything minimalrouter needs are already inside this ISO.
EOF
    printf '\nstarting setup...\n\n'
}

'''
replace_between(p, 'show_welcome() {\n', '[ -x "$SETUP_BIN" ]', welcome)

old_art = r'''    cat <<'ART'
           _       _                 _                 _
 _ __ ___ (_)_ __ (_)_ __ ___   __ _| |_ __ ___  _   _| |_ ___ _ __
| '_ ` _ \| | '_ \| | '_ ` _ \ / _` | | '__/ _ \| | | | __/ _ \ '__|
| | | | | | | | | | | | | | | | (_| | | | | (_) | |_| | ||  __/ |
|_| |_| |_|_|_| |_|_|_| |_| |_|\__,_|_|_|  \___/ \__,_|\__\___|_|
ART
'''
new_art = r'''    cat <<'ART'
+----------------------------------------------------------+
|                      minimalrouter                       |
+----------------------------------------------------------+
ART
'''
replace_once(p, old_art, new_art)
replace_once(p,
"    printf '\\033[32m●\\033[0m Minimal Router OS v%s configuration saved. The first disk boot will finalize the router.\\n' \"$MR_VERSION\"",
"    printf '\\033[32m●\\033[0m minimalrouter v%s configuration saved. The first disk boot will finalize the router.\\n' \"$MR_VERSION\"")

p = "packaging/alpine/live-installer.sh"
old_art = r'''cat <<'ART'
           _       _                 _                 _
 _ __ ___ (_)_ __ (_)_ __ ___   __ _| |_ __ ___  _   _| |_ ___ _ __
| '_ ` _ \| | '_ \| | '_ ` _ \ / _` | | '__/ _ \| | | | __/ _ \ '__|
| | | | | | | | | | | | | | | | (_| | | | | (_) | |_| | ||  __/ |
|_| |_| |_|_|_| |_|_|_| |_| |_|\__,_|_|_|  \___/ \__,_|\__\___|_|
ART
'''
new_art = r'''cat <<'ART'
+----------------------------------------------------------+
|                      minimalrouter                       |
+----------------------------------------------------------+
ART
'''
replace_once(p, old_art, new_art)
replace_once(p,
"printf '\\n\\033[32m●\\033[0m Minimal Router OS v%s installation completed successfully.\\n' \"$VERSION\"",
"printf '\\n\\033[32m●\\033[0m minimalrouter v%s installation completed successfully.\\n' \"$VERSION\"")
