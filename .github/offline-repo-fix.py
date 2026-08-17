from pathlib import Path


def one(path, old, new):
    p = Path(path)
    s = p.read_text()
    n = s.count(old)
    if n != 1:
        raise SystemExit(f"{path}: expected one match, found {n}: {old[:120]!r}")
    p.write_text(s.replace(old, new, 1))


# install-core must determine offline mode before it is allowed to normalize
# repositories. In the ISO path the caller owns a local media repository and
# install-core must leave it untouched.
p = "packaging/alpine/install-dist.sh"
one(p,
'''ALPINE_VERSION="v3.22"
if ! grep -q "$ALPINE_VERSION" /etc/apk/repositories 2>/dev/null; then
    echo "https://dl-cdn.alpinelinux.org/alpine/$ALPINE_VERSION/main" > /etc/apk/repositories
    echo "https://dl-cdn.alpinelinux.org/alpine/$ALPINE_VERSION/community" >> /etc/apk/repositories
fi

OFFLINE_MODE=0
if [ "${1:-}" = "--offline" ]; then
    OFFLINE_MODE=1
elif [ -n "${1:-}" ]; then
    echo "Usage: $0 [--offline]" >&2
    exit 1
fi

if [ "${MINIMALROUTER_OFFLINE:-}" = "1" ]; then
    OFFLINE_MODE=1
fi
''',
'''ALPINE_VERSION="v3.22"
OFFLINE_MODE=0
if [ "${1:-}" = "--offline" ]; then
    OFFLINE_MODE=1
elif [ -n "${1:-}" ]; then
    echo "Usage: $0 [--offline]" >&2
    exit 1
fi

if [ "${MINIMALROUTER_OFFLINE:-}" = "1" ]; then
    OFFLINE_MODE=1
fi

# The all-in-one ISO supplies a caller-managed local Alpine repository. Never
# replace it with CDN URLs in offline mode: setup-disk runs later in the same
# live environment and must remain installable with zero WAN connectivity.
if [ "$OFFLINE_MODE" -eq 0 ] && ! grep -q "$ALPINE_VERSION" /etc/apk/repositories 2>/dev/null; then
    echo "https://dl-cdn.alpinelinux.org/alpine/$ALPINE_VERSION/main" > /etc/apk/repositories
    echo "https://dl-cdn.alpinelinux.org/alpine/$ALPINE_VERSION/community" >> /etc/apk/repositories
fi
''')

# Remember and defensively restore the media repository after the live core
# installer, then once more immediately before setup-disk.
p = "packaging/alpine/live-installer.sh"
one(p,
'''    base_repo="$(find "$MEDIA/apks" -type f -name APKINDEX.tar.gz -print 2>/dev/null | head -1 | xargs -r dirname)"
    [ -n "$base_repo" ] || fail "The Alpine base repository (APKINDEX.tar.gz) was not found on the boot media"
    printf '%s\\n' "$base_repo" > /etc/apk/repositories
''',
'''    base_repo="$(find "$MEDIA/apks" -type f -name APKINDEX.tar.gz -print 2>/dev/null | head -1 | xargs -r dirname)"
    [ -n "$base_repo" ] || fail "The Alpine base repository (APKINDEX.tar.gz) was not found on the boot media"
    ALPINE_MEDIA_REPO="$base_repo"
    printf '%s\\n' "$ALPINE_MEDIA_REPO" > /etc/apk/repositories
''')

marker = '''install_target_packages() {
'''
helper = '''restore_alpine_media_repo() {
    [ -n "${ALPINE_MEDIA_REPO:-}" ] || fail "The Alpine media repository path was lost before disk installation"
    [ -r "$ALPINE_MEDIA_REPO/APKINDEX.tar.gz" ] || fail "The Alpine media APKINDEX is no longer available: $ALPINE_MEDIA_REPO"
    printf '%s\\n' "$ALPINE_MEDIA_REPO" > /etc/apk/repositories
    if ! apk update --no-network >/tmp/minimalrouter-apk-update.log 2>&1; then
        cat /tmp/minimalrouter-apk-update.log >&2 || true
        fail "The Alpine media repository could not be restored for setup-disk"
    fi
}

'''
s = Path(p).read_text()
if "restore_alpine_media_repo()" not in s:
    if marker not in s:
        raise SystemExit(f"{p}: install_target_packages marker missing")
    Path(p).write_text(s.replace(marker, helper + marker, 1))

one(p,
'''MINIMALROUTER_ISO_INSTALL=1 MINIMALROUTER_OFFLINE=1 sh "$DIST/install.sh" --offline || fail "MinimalRouter live configuration could not be prepared"

printf '\\nRecovery / SSH root password\\n'
''',
'''MINIMALROUTER_ISO_INSTALL=1 MINIMALROUTER_OFFLINE=1 sh "$DIST/install.sh" --offline || fail "MinimalRouter live configuration could not be prepared"
# install-core must not touch the caller-owned repo in offline mode. Reassert it
# here anyway so future installer changes cannot silently reintroduce a CDN
# dependency before setup-disk.
restore_alpine_media_repo

printf '\\nRecovery / SSH root password\\n'
''')

one(p,
'''# The operator has already explicitly typed ERASE for this exact device, so the
# setup-disk confirmation can safely be suppressed here.
if ! ERASE_DISKS="$TARGET" SWAP_SIZE=0 setup-disk -v -m sys -k lts -s 0 "$TARGET" >/tmp/minimalrouter-setup-disk.log 2>&1; then
''',
'''# Reassert the local ISO repository at the last possible point. This is also
# what makes the CI full-install test a genuine zero-Internet installation.
restore_alpine_media_repo

# The target was either auto-selected by the guarded one-disk VM path or
# explicitly confirmed by the operator, so setup-disk may proceed non-interactively.
if ! ERASE_DISKS="$TARGET" SWAP_SIZE=0 setup-disk -v -m sys -k lts -s 0 "$TARGET" >/tmp/minimalrouter-setup-disk.log 2>&1; then
''')

# The end-to-end guest has no route to the Internet. QEMU restrict=on preserves
# explicit hostfwd, so the test can still perform real SSH logins.
p = "scripts/ci/iso-full-install.exp"
one(p,
'''    -netdev user,id=wan \\
    -device virtio-net-pci,netdev=wan \\
    -netdev user,id=lan,net=192.168.1.0/24,host=192.168.1.254,dhcpstart=192.168.1.100,hostfwd=tcp:127.0.0.1:2222-192.168.1.1:22 \\
''',
'''    -netdev user,id=wan,restrict=on \\
    -device virtio-net-pci,netdev=wan \\
    -netdev user,id=lan,restrict=on,net=192.168.1.0/24,host=192.168.1.254,dhcpstart=192.168.1.100,hostfwd=tcp:127.0.0.1:2222-192.168.1.1:22 \\
''')
