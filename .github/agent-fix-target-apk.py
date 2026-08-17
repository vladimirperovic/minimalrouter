from pathlib import Path

p = Path('packaging/alpine/live-installer.sh')
s = p.read_text()
old = '''install_target_packages() {
    apk_dir_inside="$1"
    set -- "$apk_dir_inside"/*.apk
    [ -f "/mnt$1" ] || fail "Target package path is unavailable inside chroot: $apk_dir_inside"
    if ! chroot /mnt apk add --no-network --no-cache --force-non-repository "$apk_dir_inside"/*.apk >/tmp/minimalrouter-target-apk.log 2>&1; then
        cat /tmp/minimalrouter-target-apk.log >&2 || true
        fail "Unable to install bundled packages into the target system"
    fi
}
'''
new = '''install_target_packages() {
    apk_dir_inside="$1"

    # Validate against the mounted target, then deliberately expand *.apk only
    # after entering the chroot. Expanding it in the live shell would look for
    # /var/cache/minimalrouter/apks on the live tmpfs instead of on /mnt.
    set -- "/mnt$apk_dir_inside"/*.apk
    [ -f "$1" ] || fail "Target package path is unavailable inside chroot: $apk_dir_inside"

    if ! chroot /mnt /bin/sh -c '\''
        apk_dir="$1"
        set -- "$apk_dir"/*.apk
        [ -f "$1" ] || exit 66
        exec apk add --no-network --no-cache --force-non-repository "$@"
    '\'' sh "$apk_dir_inside" >/tmp/minimalrouter-target-apk.log 2>&1; then
        cat /tmp/minimalrouter-target-apk.log >&2 || true
        fail "Unable to install bundled packages into the target system"
    fi
}
'''
if s.count(old) != 1:
    raise SystemExit(f'expected exactly one install_target_packages function, found {s.count(old)}')
p.write_text(s.replace(old, new, 1))
