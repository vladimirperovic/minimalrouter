from pathlib import Path

p = Path('packaging/alpine/build-iso.sh')
s = p.read_text()

def one(old, new):
    global s
    n = s.count(old)
    if n != 1:
        raise SystemExit(f'expected one match, got {n}: {old[:100]!r}')
    s = s.replace(old, new, 1)

one('''VERSION="$(tr -d '\\r\\n' < VERSION)"
[ -n "$VERSION" ] || { echo "ERROR: VERSION is empty" >&2; exit 1; }
VERSION_SAFE="$(printf '%s' "$VERSION" | tr -cd '0-9A-Za-z._-')"
''', '''VERSION="${BUILD_VERSION:-$(tr -d '\\r\\n' < VERSION)}"
VERSION="${VERSION#v}"
[ -n "$VERSION" ] || { echo "ERROR: VERSION is empty" >&2; exit 1; }
BUILD_COMMIT="${BUILD_COMMIT:-${GITHUB_SHA:-unknown}}"
BUILD_DATE="${BUILD_DATE:-$(date -u '+%Y-%m-%dT%H:%M:%SZ')}"
VERSION_SAFE="$(printf '%s' "$VERSION" | tr -cd '0-9A-Za-z._-')"
''')

one('''cp VERSION "$INJECT_DIR/minimalrouter/VERSION"
cp "$APK_MANIFEST" "$INJECT_DIR/minimalrouter/APK-SHA256SUMS"
printf '%s\\n' "$ALPINE_VERSION" > "$INJECT_DIR/minimalrouter/ALPINE_VERSION"
printf '%s\\n' "${GITHUB_SHA:-unknown}" > "$INJECT_DIR/minimalrouter/BUILD_COMMIT"
''', '''printf '%s\\n' "$VERSION" > "$INJECT_DIR/minimalrouter/VERSION"
cp "$APK_MANIFEST" "$INJECT_DIR/minimalrouter/APK-SHA256SUMS"
printf '%s\\n' "$ALPINE_VERSION" > "$INJECT_DIR/minimalrouter/ALPINE_VERSION"
printf '%s\\n' "$BUILD_COMMIT" > "$INJECT_DIR/minimalrouter/BUILD_COMMIT"
printf '%s\\n' "$BUILD_DATE" > "$INJECT_DIR/minimalrouter/BUILD_DATE"
cat > "$INJECT_DIR/minimalrouter/BUILD-INFO" <<EOF
product=minimalrouter
version=$VERSION
commit=$BUILD_COMMIT
build_date=$BUILD_DATE
alpine_version=$ALPINE_VERSION
alpine_branch=$ALPINE_BRANCH
architecture=amd64
EOF
''')

one('''iso_ls_has /minimalrouter VERSION || { echo "ERROR: final ISO is missing /minimalrouter/VERSION" >&2; exit 1; }
''', '''iso_ls_has /minimalrouter VERSION || { echo "ERROR: final ISO is missing /minimalrouter/VERSION" >&2; exit 1; }
iso_ls_has /minimalrouter BUILD-INFO || { echo "ERROR: final ISO is missing /minimalrouter/BUILD-INFO" >&2; exit 1; }
''')

p.write_text(s)
