from pathlib import Path

p = Path('packaging/alpine/live-installer.sh')
s = p.read_text()
old = '''    log "Preparing the offline installation environment..."
    if ! apk add --no-network --no-cache --force-non-repository "$apk_dir"/*.apk >/tmp/minimalrouter-apk-install.log 2>&1; then
        cat /tmp/minimalrouter-apk-install.log >&2 || true
        fail "Unable to install the bundled Alpine packages"
    fi

    # setup-disk requires a real APKINDEX. Discover the repository on the actual
'''
new = '''    log "Preparing the offline installation environment..."

    # Installing APK files by path adds exact local-package constraints to
    # /etc/apk/world. Those live-only recovery tools must never become the
    # desired package set for setup-disk (older failed ISOs exposed these as
    # checksum-like ><Q1 world entries). Preserve the pristine Alpine live world,
    # install the tools, then restore world before setup-disk builds the target.
    world_backup=/tmp/minimalrouter-apk-world.before
    if [ -f /etc/apk/world ]; then
        cp /etc/apk/world "$world_backup"
    else
        : > "$world_backup"
    fi

    if ! apk add --no-network --no-cache --force-non-repository "$apk_dir"/*.apk >/tmp/minimalrouter-apk-install.log 2>&1; then
        cp "$world_backup" /etc/apk/world
        chmod 0644 /etc/apk/world
        cat /tmp/minimalrouter-apk-install.log >&2 || true
        fail "Unable to install the bundled Alpine packages"
    fi
    cp "$world_backup" /etc/apk/world
    chmod 0644 /etc/apk/world

    # setup-disk requires a real APKINDEX. Discover the repository on the actual
'''
if s.count(old) != 1:
    raise SystemExit(f'expected one prepare_packages apk block, found {s.count(old)}')
p.write_text(s.replace(old, new, 1))
