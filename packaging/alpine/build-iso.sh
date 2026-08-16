#!/bin/sh
# Minimal Router OS — bootable all-in-one ISO builder.
# Remasters the verified Alpine 3.22 standard ISO, adds a signed Alpine package
# bundle, the MinimalRouter distribution and an apkovl that starts the installer.
set -eu

ALPINE_VERSION="${ALPINE_VERSION:-3.22.5}"
ALPINE_BRANCH="v3.22"
ALPINE_ARCH="x86_64"
ALPINE_BASE_URL="https://dl-cdn.alpinelinux.org/alpine/${ALPINE_BRANCH}/releases/${ALPINE_ARCH}"
ALPINE_ISO_NAME="alpine-standard-${ALPINE_VERSION}-${ALPINE_ARCH}.iso"
ALPINE_ISO_URL="${ALPINE_BASE_URL}/${ALPINE_ISO_NAME}"
ALPINE_SHA_URL="${ALPINE_ISO_URL}.sha256"

VERSION="$(tr -d '\r\n' < VERSION)"
[ -n "$VERSION" ] || { echo "ERROR: VERSION is empty" >&2; exit 1; }
VERSION_SAFE="$(printf '%s' "$VERSION" | tr -cd '0-9A-Za-z._-')"
VOLUME_VERSION="$(printf '%s' "$VERSION" | tr -cd '0-9A-Za-z' | cut -c1-12)"

BUILD_DIR="build/iso"
CACHE_DIR="$BUILD_DIR/cache"
APK_DIR="$BUILD_DIR/apks"
INJECT_DIR="$BUILD_DIR/inject"
OVERLAY_DIR="$BUILD_DIR/apkovl-root"
BASE_ISO="$CACHE_DIR/$ALPINE_ISO_NAME"
BASE_SHA="$CACHE_DIR/$ALPINE_ISO_NAME.sha256"
OUT_ISO="$BUILD_DIR/minimalrouter-${VERSION_SAFE}-amd64.iso"
OUT_SHA="$OUT_ISO.sha256"
APK_MANIFEST="$BUILD_DIR/APK-SHA256SUMS"

# linux-firmware-none intentionally satisfies linux-lts' linux-firmware-any
# dependency. MinimalRouter is a wired router appliance and the standard Alpine
# ISO already carries boot-time hardware support; bundling every GPU/Wi-Fi/DSP
# firmware family would add roughly a gigabyte that a Proxmox/VirtIO router can
# never use. Physical appliances needing device-specific firmware can install
# the appropriate signed Alpine firmware package later.
REQUIRED_PACKAGES="alpine-base alpine-conf linux-lts linux-firmware-none e2fsprogs syslinux grub grub-efi dosfstools util-linux nftables ppp ppp-pppoe dnsmasq iproute2 iputils-ping iputils-arping ca-certificates wireguard-tools-wg doas squid hostapd hostapd-openrc iw inadyn inadyn-openrc chrony chrony-openrc logrotate"

need() {
    command -v "$1" >/dev/null 2>&1 || {
        echo "ERROR: required build tool is missing: $1" >&2
        exit 1
    }
}

fetch_file() {
    url="$1"
    output="$2"
    if [ -s "$output" ]; then
        return 0
    fi
    tmp="${output}.tmp"
    rm -f "$tmp"
    curl -fL --retry 3 --retry-delay 2 "$url" -o "$tmp"
    mv "$tmp" "$output"
}

fetch_apks() {
    rm -rf "$APK_DIR"
    mkdir -p "$APK_DIR"

    if command -v docker >/dev/null 2>&1; then
        repo_root="$(pwd)"
        # The official Alpine container is used only as an apk client. Every APK
        # remains signed by Alpine; no locally built or unsigned package enters
        # the ISO. Installing linux-firmware-none into the resolver environment
        # forces the small explicit linux-firmware-any provider before fetching.
        docker run --rm \
            -v "$repo_root:/work" \
            -w /work \
            "alpine:${ALPINE_BRANCH#v}" \
            /bin/sh -ec '
                printf "%s\n" \
                  "https://dl-cdn.alpinelinux.org/alpine/v3.22/main" \
                  "https://dl-cdn.alpinelinux.org/alpine/v3.22/community" \
                  > /etc/apk/repositories
                apk update >/dev/null
                apk add --no-cache linux-firmware-none >/dev/null
                apk fetch --recursive --output /work/build/iso/apks \
                  alpine-base alpine-conf linux-lts linux-firmware-none e2fsprogs syslinux grub grub-efi dosfstools util-linux \
                  nftables ppp ppp-pppoe dnsmasq iproute2 iputils-ping iputils-arping ca-certificates \
                  wireguard-tools-wg doas squid hostapd hostapd-openrc iw inadyn inadyn-openrc \
                  chrony chrony-openrc logrotate
            '
    elif command -v apk >/dev/null 2>&1; then
        apk add --no-cache linux-firmware-none >/dev/null
        apk fetch --recursive --output "$APK_DIR" $REQUIRED_PACKAGES
    else
        echo "ERROR: building the offline APK bundle requires Docker or Alpine apk(8)" >&2
        exit 1
    fi

    set -- "$APK_DIR"/*.apk
    [ -f "$1" ] || { echo "ERROR: no APKs were fetched" >&2; exit 1; }
    (cd "$APK_DIR" && sha256sum ./*.apk | sort) > "$APK_MANIFEST"
}

build_apkovl() {
    rm -rf "$OVERLAY_DIR"
    mkdir -p "$OVERLAY_DIR/etc/minimalrouter"
    install -m 0755 packaging/alpine/live-installer.sh "$OVERLAY_DIR/etc/minimalrouter/live-installer.sh"

    cat > "$OVERLAY_DIR/etc/inittab" <<'EOF'
::sysinit:/sbin/openrc sysinit
::sysinit:/sbin/openrc boot
::wait:/sbin/openrc default

# tty1 is owned by the MinimalRouter installer. Other consoles remain available
# as emergency Alpine shells if the installer cannot start.
tty1::respawn:/etc/minimalrouter/live-installer.sh
tty2::respawn:/sbin/getty 38400 tty2
tty3::respawn:/sbin/getty 38400 tty3
tty4::respawn:/sbin/getty 38400 tty4

::ctrlaltdel:/sbin/reboot
::shutdown:/sbin/openrc shutdown
EOF
    chmod 0644 "$OVERLAY_DIR/etc/inittab"
    printf 'minimalrouter-installer\n' > "$OVERLAY_DIR/etc/hostname"
    printf 'Minimal Router OS installer\n' > "$OVERLAY_DIR/etc/issue"

    tar -czf "$BUILD_DIR/minimalrouter.apkovl.tar.gz" -C "$OVERLAY_DIR" .
}

build_syslinux_config() {
    cat > "$BUILD_DIR/syslinux.cfg" <<'EOF'
SERIAL 0 115200
CONSOLE 0
PROMPT 0
TIMEOUT 20
DEFAULT minimalrouter

LABEL minimalrouter
  MENU LABEL Minimal Router OS Installer
  KERNEL /boot/vmlinuz-lts
  INITRD /boot/initramfs-lts
  APPEND modules=loop,squashfs,sd-mod,usb-storage modloop=/boot/modloop-lts console=tty0 console=ttyS0,115200
EOF
}

iso_ls_has() {
    dir="$1"
    name="$2"
    xorriso -indev "$OUT_ISO" -ls "$dir" 2>/dev/null | grep -qF "$name"
}

need curl
need sha256sum
need tar
need xorriso

mkdir -p "$BUILD_DIR" "$CACHE_DIR"

echo "=== Minimal Router OS v$VERSION ISO Builder ==="
echo "[1/7] Building the MinimalRouter AMD64 distribution..."
make dist-amd64
DIST_DIR="build/dist/minimalrouter-linux-amd64"
[ -d "$DIST_DIR" ] || { echo "ERROR: distribution directory is missing" >&2; exit 1; }
cp VERSION "$DIST_DIR/VERSION"

echo "[2/7] Downloading Alpine Linux ${ALPINE_VERSION} standard ISO..."
fetch_file "$ALPINE_ISO_URL" "$BASE_ISO"
fetch_file "$ALPINE_SHA_URL" "$BASE_SHA"
(
    cd "$CACHE_DIR"
    sha256sum -c "$(basename "$BASE_SHA")"
)

echo "[3/7] Fetching the complete offline Alpine package bundle..."
fetch_apks

echo "[4/7] Building MinimalRouter boot overlay..."
build_apkovl
build_syslinux_config

echo "[5/7] Preparing ISO payload..."
rm -rf "$INJECT_DIR"
mkdir -p "$INJECT_DIR/minimalrouter"
cp -a "$DIST_DIR" "$INJECT_DIR/minimalrouter/minimalrouter-linux-amd64"
cp -a "$APK_DIR" "$INJECT_DIR/minimalrouter/apks"
cp VERSION "$INJECT_DIR/minimalrouter/VERSION"
cp "$APK_MANIFEST" "$INJECT_DIR/minimalrouter/APK-SHA256SUMS"
printf '%s\n' "$ALPINE_VERSION" > "$INJECT_DIR/minimalrouter/ALPINE_VERSION"
printf '%s\n' "${GITHUB_SHA:-unknown}" > "$INJECT_DIR/minimalrouter/BUILD_COMMIT"

rm -f "$OUT_ISO" "$OUT_SHA"

echo "[6/7] Remastering the bootable Alpine ISO..."
# xorriso replay preserves the original Alpine BIOS/UEFI hybrid boot equipment
# while mapping our payload and apkovl into the ISO filesystem.
xorriso \
    -indev "$BASE_ISO" \
    -outdev "$OUT_ISO" \
    -boot_image any replay \
    -map "$INJECT_DIR/minimalrouter" /minimalrouter \
    -map "$BUILD_DIR/minimalrouter.apkovl.tar.gz" /minimalrouter.apkovl.tar.gz \
    -map "$BUILD_DIR/syslinux.cfg" /boot/syslinux/syslinux.cfg \
    -volid "MR_${VOLUME_VERSION}" \
    -commit \
    -end

# Verify the actual ISO directories rather than relying on xorriso -find output,
# whose printed path format differs across xorriso releases.
iso_ls_has /minimalrouter VERSION || { echo "ERROR: final ISO is missing /minimalrouter/VERSION" >&2; exit 1; }
iso_ls_has /minimalrouter minimalrouter-linux-amd64 || { echo "ERROR: final ISO is missing the MinimalRouter distribution" >&2; exit 1; }
iso_ls_has /minimalrouter/minimalrouter-linux-amd64 install.sh || { echo "ERROR: final ISO is missing install.sh" >&2; exit 1; }
iso_ls_has /minimalrouter/minimalrouter-linux-amd64 install-core.sh || { echo "ERROR: final ISO is missing install-core.sh" >&2; exit 1; }
iso_ls_has /minimalrouter/minimalrouter-linux-amd64/bin router-setup-amd64 || { echo "ERROR: final ISO is missing router-setup-amd64" >&2; exit 1; }
iso_ls_has / minimalrouter.apkovl.tar.gz || { echo "ERROR: final ISO is missing the boot overlay" >&2; exit 1; }
iso_ls_has /boot vmlinuz-lts || { echo "ERROR: final ISO is missing vmlinuz-lts" >&2; exit 1; }
iso_ls_has /boot initramfs-lts || { echo "ERROR: final ISO is missing initramfs-lts" >&2; exit 1; }
iso_ls_has /boot modloop-lts || { echo "ERROR: final ISO is missing modloop-lts" >&2; exit 1; }

sha256sum "$OUT_ISO" > "$OUT_SHA"

echo "[7/7] ISO complete."
ls -lh "$OUT_ISO" "$OUT_SHA"
echo "ISO: $OUT_ISO"
echo "SHA256: $OUT_SHA"
