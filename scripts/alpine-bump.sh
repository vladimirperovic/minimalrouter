#!/bin/sh
# Moves the Golden appliance image to another Alpine release, after checking
# that the release can actually build it:
#   - every image package (packaging/alpine/rootfs-packages) and runtime
#     package (REQUIRED_PACKAGES in install-dist.sh) exists for x86_64;
#   - the Alpine Extended installer ISO and its SHA-256 file are published;
#   - the exact builder image alpine:<version> exists.
#
# Usage:
#   sh scripts/alpine-bump.sh <version>          check, then switch
#   sh scripts/alpine-bump.sh --check <version>  check only
#
# Switching rewrites packaging/alpine/alpine-release and adds the new branch
# to the installers' SUPPORTED_ALPINE_BRANCHES (older branches stay supported
# for appliances already running them). It changes nothing when a check
# fails. The result still needs the full Appliance ISO workflow before merge
# (docs/GOLDEN-IMAGE.md).
set -eu
cd "$(dirname "$0")/.."

usage() {
    echo "usage: $0 [--check] <alpine-version, for example 3.26.1>" >&2
    exit 2
}

CHECK_ONLY=0
if [ "${1:-}" = "--check" ]; then
    CHECK_ONLY=1
    shift
fi
NEW="${1:-}"
printf '%s' "$NEW" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$' || usage
NEW_BRANCH="v${NEW%.*}"
. packaging/alpine/alpine-release
OLD="$ALPINE_VERSION"
MIRROR="${ALPINE_MIRROR:-https://dl-cdn.alpinelinux.org/alpine}"
WORK=$(mktemp -d "${TMPDIR:-/tmp}/alpine-bump.XXXXXX")
trap 'rm -rf "$WORK"' EXIT HUP INT TERM

echo "Checking Alpine $NEW ($NEW_BRANCH); the image is on $OLD."
: > "$WORK/index"
for repo in main community; do
    curl -fsSL --retry 3 -o "$WORK/$repo.tar.gz" "$MIRROR/$NEW_BRANCH/$repo/x86_64/APKINDEX.tar.gz" || {
        echo "ERROR: no $repo package index for $NEW_BRANCH" >&2
        exit 1
    }
    tar -xzf "$WORK/$repo.tar.gz" -C "$WORK" APKINDEX
    cat "$WORK/APKINDEX" >> "$WORK/index"
done

package_version() {
    awk -v want="$1" 'BEGIN { RS = ""; FS = "\n" }
        { name = ""; version = ""
          for (i = 1; i <= NF; i++) {
              if ($i ~ /^P:/) name = substr($i, 3)
              if ($i ~ /^V:/) version = substr($i, 3)
          }
          if (name == want) { print version; exit } }' "$WORK/index"
}

packages=$(
    sed -e 's/#.*//' packaging/alpine/rootfs-packages
    sed -n 's/^REQUIRED_PACKAGES="\(.*\)"$/\1/p' packaging/alpine/install-dist.sh
)
missing=""
for package in $(printf '%s\n' $packages | sort -u); do
    version=$(package_version "$package")
    if [ -z "$version" ]; then
        missing="$missing $package"
    else
        printf '  %-24s %s\n' "$package" "$version"
    fi
done
[ -z "$missing" ] || { echo "ERROR: missing from $NEW_BRANCH:$missing" >&2; exit 1; }

iso="alpine-extended-$NEW-x86_64.iso"
for file in "$iso" "$iso.sha256"; do
    curl -fsSI --retry 3 "$MIRROR/$NEW_BRANCH/releases/x86_64/$file" >/dev/null || {
        echo "ERROR: $MIRROR/$NEW_BRANCH/releases/x86_64/$file is not published" >&2
        exit 1
    }
done
curl -fsS --retry 3 -o /dev/null "https://hub.docker.com/v2/namespaces/library/repositories/alpine/tags/$NEW" || {
    echo "ERROR: builder image alpine:$NEW does not exist" >&2
    exit 1
}
echo "All checks passed: packages, $iso, alpine:$NEW. Kernel: linux-lts $(package_version linux-lts)."
[ "$CHECK_ONLY" -eq 0 ] || exit 0

# replace FILE SED-EXPRESSION, portable across GNU and BSD sed.
replace() {
    sed -e "$2" "$1" > "$WORK/edit"
    cat "$WORK/edit" > "$1"
}
replace packaging/alpine/alpine-release "s/^ALPINE_VERSION=.*/ALPINE_VERSION=$NEW/"
for installer in packaging/alpine/install-dist.sh packaging/alpine/install.sh; do
    supported=$(sed -n 's/^SUPPORTED_ALPINE_BRANCHES="\(.*\)"$/\1/p' "$installer")
    case " $supported " in
        *" $NEW_BRANCH "*) ;;
        *) replace "$installer" "s/^SUPPORTED_ALPINE_BRANCHES=\".*\"\$/SUPPORTED_ALPINE_BRANCHES=\"$supported $NEW_BRANCH\"/" ;;
    esac
done
sh scripts/ci/alpine-release_test.sh

echo
echo "Switched the image from Alpine $OLD to $NEW. Still to do:"
echo "  - review documentation that names the old release:"
git grep -n -E "Alpine ${OLD%.*}([^.0-9]|$)|v${OLD%.*}" -- README.md docs .github/ISSUE_TEMPLATE | sed 's/^/      /' || true
echo "  - add a CHANGELOG entry (new installs only; existing appliances keep their release);"
echo "  - run the Appliance ISO workflow, which must pass before merge (docs/GOLDEN-IMAGE.md)."
