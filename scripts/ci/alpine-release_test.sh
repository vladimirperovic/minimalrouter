#!/bin/sh
# The Alpine release is pinned in exactly one place,
# packaging/alpine/alpine-release. Fail if another script pins a release or
# builder image, or if an installer does not support the image's branch.
set -eu
cd "$(dirname "$0")/../.."

. packaging/alpine/alpine-release
printf '%s' "$ALPINE_VERSION" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$' || {
    echo "ERROR: ALPINE_VERSION must look like 3.24.3, not '$ALPINE_VERSION'" >&2
    exit 1
}
[ "$ALPINE_BRANCH" = "v${ALPINE_VERSION%.*}" ] || { echo "ERROR: ALPINE_BRANCH does not match ALPINE_VERSION" >&2; exit 1; }

for installer in packaging/alpine/install-dist.sh packaging/alpine/install.sh; do
    supported=$(sed -n 's/^SUPPORTED_ALPINE_BRANCHES="\(.*\)"$/\1/p' "$installer")
    case " $supported " in
        *" $ALPINE_BRANCH "*) ;;
        *)
            echo "ERROR: $installer does not support the image branch $ALPINE_BRANCH (has: $supported)" >&2
            exit 1
            ;;
    esac
done

if git grep -n -E 'alpine:3\.[0-9]|alpine/v3\.[0-9]+/|alpine-(extended|virt|standard)-3\.[0-9]' -- \
    packaging scripts tools quick-test.sh .github/workflows ':!packaging/alpine/alpine-release'; then
    echo "ERROR: pin the Alpine release only in packaging/alpine/alpine-release" >&2
    exit 1
fi
echo "alpine-release: one pin ($ALPINE_VERSION), supported by both installers"
