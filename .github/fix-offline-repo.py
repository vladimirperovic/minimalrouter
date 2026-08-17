from pathlib import Path


def one(path, old, new):
    p = Path(path)
    s = p.read_text()
    n = s.count(old)
    if n != 1:
        raise SystemExit(f"{path}: expected one match, found {n}: {old[:120]!r}")
    p.write_text(s.replace(old, new, 1))


p = "packaging/alpine/build-iso.sh"
one(p,
    'APK_DIR="$BUILD_DIR/apks"\nINJECT_DIR="$BUILD_DIR/inject"',
    'APK_DIR="$BUILD_DIR/apks"\nAPK_REPO_DIR="$BUILD_DIR/apk-repo"\nINJECT_DIR="$BUILD_DIR/inject"')

one(p,
'''    (cd "$APK_DIR" && sha256sum ./*.apk | sort) > "$APK_MANIFEST"
}

build_apkovl() {''',
'''    (cd "$APK_DIR" && sha256sum ./*.apk | sort) > "$APK_MANIFEST"
}

build_offline_repos() {
    # setup-disk does a normal apk transaction into --root /mnt. Give it normal
    # signed Alpine repositories rather than a flat collection of APK files.
    # The APKINDEX files come directly from the same official v3.22 repositories
    # used by apk fetch above, so Alpine's existing trusted keys verify them.
    rm -rf "$APK_REPO_DIR"
    mkdir -p \
        "$APK_REPO_DIR/main/$ALPINE_ARCH" \
        "$APK_REPO_DIR/community/$ALPINE_ARCH"

    fetch_file \
        "https://dl-cdn.alpinelinux.org/alpine/${ALPINE_BRANCH}/main/${ALPINE_ARCH}/APKINDEX.tar.gz" \
        "$APK_REPO_DIR/main/$ALPINE_ARCH/APKINDEX.tar.gz"
    fetch_file \
        "https://dl-cdn.alpinelinux.org/alpine/${ALPINE_BRANCH}/community/${ALPINE_ARCH}/APKINDEX.tar.gz" \
        "$APK_REPO_DIR/community/$ALPINE_ARCH/APKINDEX.tar.gz"

    # Keep only one physical copy of every APK in the ISO. Rock Ridge preserves
    # these relative symlinks and apk follows them when resolving package files.
    for repo in main community; do
        for apk in "$APK_DIR"/*.apk; do
            name="$(basename "$apk")"
            ln -s "../../../apks/$name" "$APK_REPO_DIR/$repo/$ALPINE_ARCH/$name"
        done
    done

    # Prove before remastering that the signed indexes can resolve and fetch the
    # exact packages setup-disk needs, using only the repository tree we will put
    # on the ISO. This also catches a rare CDN index/package race during a build.
    if command -v docker >/dev/null 2>&1; then
        repo_root="$(pwd)"
        docker run --rm --platform linux/amd64 \
            -v "$repo_root:/work:ro" \
            "alpine:${ALPINE_BRANCH#v}" \
            /bin/sh -ec '\''
                printf "%s\\n" \
                  "/work/build/iso/apk-repo/main" \
                  "/work/build/iso/apk-repo/community" \
                  > /etc/apk/repositories
                apk update --no-network >/dev/null
                mkdir -p /tmp/mr-fetch
                apk fetch --no-network --recursive --output /tmp/mr-fetch \
                  alpine-base e2fsprogs linux-lts openssl syslinux >/dev/null
                for pkg in alpine-base e2fsprogs linux-lts openssl syslinux; do
                    ls /tmp/mr-fetch/${pkg}-*.apk >/dev/null 2>&1 || {
                        echo "offline repository validation did not fetch $pkg" >&2
                        exit 1
                    }
                done
            '\''
    fi
}

build_apkovl() {''')

one(p,
'''fetch_apks

echo "[4/7] Building MinimalRouter boot overlay..."''',
'''fetch_apks
build_offline_repos

echo "[4/7] Building MinimalRouter boot overlay..."''')

one(p,
'''cp -a "$APK_DIR" "$INJECT_DIR/minimalrouter/apks"
cp VERSION "$INJECT_DIR/minimalrouter/VERSION"''',
'''cp -a "$APK_DIR" "$INJECT_DIR/minimalrouter/apks"
cp -a "$APK_REPO_DIR" "$INJECT_DIR/minimalrouter/repo"
cp VERSION "$INJECT_DIR/minimalrouter/VERSION"''')

one(p,
'''iso_ls_has /minimalrouter/minimalrouter-linux-amd64/bin router-setup-amd64 || { echo "ERROR: final ISO is missing router-setup-amd64" >&2; exit 1; }
iso_ls_has / minimalrouter.apkovl.tar.gz''',
'''iso_ls_has /minimalrouter/minimalrouter-linux-amd64/bin router-setup-amd64 || { echo "ERROR: final ISO is missing router-setup-amd64" >&2; exit 1; }
iso_ls_has /minimalrouter/repo/main/x86_64 APKINDEX.tar.gz || { echo "ERROR: final ISO is missing the signed Alpine main index" >&2; exit 1; }
iso_ls_has /minimalrouter/repo/community/x86_64 APKINDEX.tar.gz || { echo "ERROR: final ISO is missing the signed Alpine community index" >&2; exit 1; }
iso_ls_has / minimalrouter.apkovl.tar.gz''')

p = "packaging/alpine/live-installer.sh"
one(p,
'''    # setup-disk requires a real APKINDEX. Discover the repository on the actual
    # mounted Alpine ISO instead of assuming /media/cdrom or treating our flat
    # package bundle as a repository.
    base_repo="$(find "$MEDIA/apks" -type f -name APKINDEX.tar.gz -print 2>/dev/null | head -1 | xargs -r dirname)"
    [ -n "$base_repo" ] || fail "The Alpine base repository (APKINDEX.tar.gz) was not found on the boot media"
    ALPINE_MEDIA_REPO="$base_repo"
    printf '%s\\n' "$ALPINE_MEDIA_REPO" > /etc/apk/repositories
    if ! apk update --no-network >/tmp/minimalrouter-apk-update.log 2>&1; then
        cat /tmp/minimalrouter-apk-update.log >&2 || true
        fail "The Alpine base repository on the ISO could not be opened"
    fi''',
'''    # setup-disk performs a normal apk transaction into /mnt, so provide the
    # two signed local repositories assembled by build-iso.sh. Their indexes are
    # the official Alpine v3.22 indexes; package files are the checksum-verified,
    # Alpine-signed APKs already carried by this ISO.
    repo_main="$MEDIA/minimalrouter/repo/main"
    repo_community="$MEDIA/minimalrouter/repo/community"
    for repo in "$repo_main" "$repo_community"; do
        [ -f "$repo/$ALPINE_ARCH/APKINDEX.tar.gz" ] || fail "Signed offline Alpine repository index is missing: $repo"
    done
    ALPINE_MEDIA_REPOS="$repo_main $repo_community"
    printf '%s\\n%s\\n' "$repo_main" "$repo_community" > /etc/apk/repositories
    if ! apk update --no-network >/tmp/minimalrouter-apk-update.log 2>&1; then
        cat /tmp/minimalrouter-apk-update.log >&2 || true
        fail "The signed offline Alpine repositories on the ISO could not be opened"
    fi
    # Fail here, before touching a disk, if the target base packages are not
    # resolvable from the local media.
    for pkg in alpine-base e2fsprogs linux-lts openssl syslinux; do
        apk search --no-network -x "$pkg" 2>/dev/null | grep -q . || fail "Offline repository cannot resolve required target package: $pkg"
    done''')

# The script currently restores one local repository immediately before
# setup-disk. Replace that guard with the complete signed pair.
one(p,
'''printf '%s\\n' "$ALPINE_MEDIA_REPO" > /etc/apk/repositories
if ! apk update --no-network >/tmp/minimalrouter-apk-update-before-disk.log 2>&1; then''',
'''printf '%s\\n%s\\n' "$repo_main" "$repo_community" > /etc/apk/repositories
if ! apk update --no-network >/tmp/minimalrouter-apk-update-before-disk.log 2>&1; then''')

one(p,
'''printf '%s\\n' "$ALPINE_MEDIA_REPO" > /etc/apk/repositories
if ! setup-disk -v -m sys "$TARGET" >/tmp/minimalrouter-setup-disk.log 2>&1; then''',
'''printf '%s\\n%s\\n' "$repo_main" "$repo_community" > /etc/apk/repositories
if ! setup-disk -v -m sys "$TARGET" >/tmp/minimalrouter-setup-disk.log 2>&1; then''')
