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
    # setup-disk performs a normal apk transaction into --root /mnt. Provide
    # normal signed Alpine repositories instead of a flat directory of APKs.
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

    # Keep one physical APK copy. Both repository trees point at the verified
    # bundle; apk simply ignores packages not present in a given signed index.
    for repo in main community; do
        for apk in "$APK_DIR"/*.apk; do
            name="$(basename "$apk")"
            ln -s "../../../apks/$name" "$APK_REPO_DIR/$repo/$ALPINE_ARCH/$name"
        done
    done

    # Prove the exact local repository tree is usable before remastering.
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
'''    # setup-disk performs a normal apk transaction into /mnt. Use the signed
    # local repositories assembled into this ISO by build-iso.sh.
    repo_main="$MEDIA/minimalrouter/repo/main"
    repo_community="$MEDIA/minimalrouter/repo/community"
    for repo in "$repo_main" "$repo_community"; do
        [ -f "$repo/x86_64/APKINDEX.tar.gz" ] || fail "Signed offline Alpine repository index is missing: $repo"
    done
    ALPINE_MEDIA_REPOS="$repo_main $repo_community"
    restore_alpine_media_repo''')

one(p,
'''restore_alpine_media_repo() {
    [ -n "${ALPINE_MEDIA_REPO:-}" ] || fail "The Alpine media repository path was lost before disk installation"
    [ -r "$ALPINE_MEDIA_REPO/APKINDEX.tar.gz" ] || fail "The Alpine media APKINDEX is no longer available: $ALPINE_MEDIA_REPO"
    printf '%s\\n' "$ALPINE_MEDIA_REPO" > /etc/apk/repositories
    if ! apk update --no-network >/tmp/minimalrouter-apk-update.log 2>&1; then
        cat /tmp/minimalrouter-apk-update.log >&2 || true
        fail "The Alpine media repository could not be restored for setup-disk"
    fi
}''',
'''restore_alpine_media_repo() {
    [ -n "${ALPINE_MEDIA_REPOS:-}" ] || fail "The signed Alpine media repository paths were lost before disk installation"
    : > /etc/apk/repositories
    for repo in $ALPINE_MEDIA_REPOS; do
        [ -r "$repo/x86_64/APKINDEX.tar.gz" ] || fail "The signed Alpine APKINDEX is no longer available: $repo"
        printf '%s\\n' "$repo" >> /etc/apk/repositories
    done
    if ! apk update --no-network >/tmp/minimalrouter-apk-update.log 2>&1; then
        cat /tmp/minimalrouter-apk-update.log >&2 || true
        fail "The signed Alpine media repositories could not be restored for setup-disk"
    fi
    for pkg in alpine-base e2fsprogs linux-lts openssl syslinux; do
        apk search --no-network -x "$pkg" 2>/dev/null | grep -q . || fail "Offline repository cannot resolve required target package: $pkg"
    done
}''')
