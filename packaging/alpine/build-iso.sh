#!/bin/sh
# MinimalRouter bootable appliance ISO builder.
# The final ISO contains one CI-built bootable golden disk image plus a tiny
# live flasher. No Alpine or MinimalRouter installation runs on the user's VM.
set -eu

ALPINE_VERSION="${ALPINE_VERSION:-3.22.5}"
ALPINE_BRANCH="v3.22"
ALPINE_ARCH="x86_64"
ALPINE_BASE_URL="https://dl-cdn.alpinelinux.org/alpine/${ALPINE_BRANCH}/releases/${ALPINE_ARCH}"
ALPINE_ISO_NAME="alpine-extended-${ALPINE_VERSION}-${ALPINE_ARCH}.iso"
ALPINE_ISO_URL="${ALPINE_BASE_URL}/${ALPINE_ISO_NAME}"
ALPINE_SHA_URL="${ALPINE_ISO_URL}.sha256"

VERSION="$(tr -d '\r\n' < VERSION)"
[ -n "$VERSION" ] || { echo "ERROR: VERSION is empty" >&2; exit 1; }
VERSION_SAFE="$(printf '%s' "$VERSION" | tr -cd '0-9A-Za-z._-')"
VOLUME_VERSION="$(printf '%s' "$VERSION" | tr -cd '0-9A-Za-z' | cut -c1-12)"
BUILD_COMMIT="${BUILD_COMMIT:-${GITHUB_SHA:-unknown}}"
BUILD_DATE="${BUILD_DATE:-$(date -u '+%Y-%m-%dT%H:%M:%SZ')}"
USE_EXISTING_DIST="${MINIMALROUTER_USE_EXISTING_DIST:-0}"
REQUIRE_SIGNED_DIST="${MINIMALROUTER_REQUIRE_SIGNED_DIST:-0}"

BUILD_DIR="build/iso"
CACHE_DIR="$BUILD_DIR/cache"
INJECT_DIR="$BUILD_DIR/inject"
OVERLAY_DIR="$BUILD_DIR/apkovl-root"
BASE_ISO="$CACHE_DIR/$ALPINE_ISO_NAME"
BASE_SHA="$CACHE_DIR/$ALPINE_ISO_NAME.sha256"
OUT_ISO="$BUILD_DIR/minimalrouter-${VERSION_SAFE}-amd64.iso"
OUT_SHA="$OUT_ISO.sha256"
GOLDEN_IMAGE="$BUILD_DIR/minimalrouter-golden-${VERSION}-amd64.img.gz"
GOLDEN_SHA="$GOLDEN_IMAGE.sha256"
GOLDEN_BYTES="$BUILD_DIR/minimalrouter-golden-${VERSION}-amd64.img.bytes"
DIST_DIR="build/dist/minimalrouter-linux-amd64"

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

build_apkovl() {
    rm -rf "$OVERLAY_DIR"
    mkdir -p \
        "$OVERLAY_DIR/etc/minimalrouter" \
        "$OVERLAY_DIR/etc/init.d" \
        "$OVERLAY_DIR/etc/runlevels/default"

    install -m 0755 packaging/alpine/live-installer.sh "$OVERLAY_DIR/etc/minimalrouter/live-installer.sh"

    # This is the only live service added to Alpine. It owns one console and
    # executes the raw-image flasher; no package manager is invoked.
    cat > "$OVERLAY_DIR/etc/init.d/minimalrouter-installer" <<'EOF'
#!/sbin/openrc-run
name="minimalrouter-installer"
description="MinimalRouter golden-image flasher"
pidfile="/run/minimalrouter-installer.pid"

depend() {
    need localmount
    after bootmisc
}

start() {
    INSTALL_TTY="/dev/tty1"
    case " $(cat /proc/cmdline 2>/dev/null || true) " in
        *" minimalrouter.console=ttyS0 "*) INSTALL_TTY="/dev/ttyS0" ;;
        *) [ -c /dev/tty1 ] || INSTALL_TTY="/dev/ttyS0" ;;
    esac
    ebegin "Launching MinimalRouter appliance installer on ${INSTALL_TTY#/dev/}"
    dmesg -n 1 >/dev/null 2>&1 || true

    if [ -f /etc/inittab ]; then
        if [ "$INSTALL_TTY" = "/dev/ttyS0" ]; then
            sed -i 's#^ttyS0::respawn:#\# MinimalRouter installer owns ttyS0: #g' /etc/inittab 2>/dev/null || true
        else
            sed -i 's#^tty1::respawn:#\# MinimalRouter installer owns tty1: #g' /etc/inittab 2>/dev/null || true
        fi
        kill -HUP 1 2>/dev/null || true
    fi
    pkill -TERM -f "[g]etty.*${INSTALL_TTY#/dev/}" 2>/dev/null || true

    if [ -c /dev/ttyS0 ]; then
        printf 'MinimalRouter installer service started; UI=%s; serial=ttyS0@115200\n' "${INSTALL_TTY#/dev/}" >/dev/ttyS0 2>/dev/null || true
    fi

    (
        export MINIMALROUTER_INSTALL_TTY="$INSTALL_TTY"
        exec <"$INSTALL_TTY" >"$INSTALL_TTY" 2>&1
        exec /etc/minimalrouter/live-installer.sh
    ) &
    echo $! > "$pidfile"
    chmod 0600 "$pidfile"
    eend 0
}

stop() {
    if [ -r "$pidfile" ]; then
        pid="$(cat "$pidfile" 2>/dev/null || true)"
        [ -n "$pid" ] && kill "$pid" 2>/dev/null || true
        rm -f "$pidfile"
    fi
    return 0
}
EOF
    chmod 0755 "$OVERLAY_DIR/etc/init.d/minimalrouter-installer"
    ln -s /etc/init.d/minimalrouter-installer "$OVERLAY_DIR/etc/runlevels/default/minimalrouter-installer"
    printf 'minimalrouter-installer\n' > "$OVERLAY_DIR/etc/hostname"
    printf 'MinimalRouter appliance installer\n' > "$OVERLAY_DIR/etc/issue"
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
  MENU LABEL MinimalRouter Installer (VGA/noVNC)
  KERNEL /boot/vmlinuz-lts
  INITRD /boot/initramfs-lts
  APPEND modules=loop,squashfs,sd-mod,usb-storage modloop=/boot/modloop-lts quiet loglevel=1 console=tty0

LABEL minimalrouter-serial
  MENU LABEL MinimalRouter Installer (serial ttyS0 115200)
  KERNEL /boot/vmlinuz-lts
  INITRD /boot/initramfs-lts
  APPEND modules=loop,squashfs,sd-mod,usb-storage modloop=/boot/modloop-lts quiet loglevel=1 minimalrouter.console=ttyS0 console=ttyS0,115200
EOF
}

build_grub_config() {
    cat > "$BUILD_DIR/grub.cfg" <<'EOF'
set timeout=1
serial --unit=0 --speed=115200 --word=8 --parity=no --stop=1
terminal_input console serial
terminal_output console serial

menuentry "MinimalRouter Installer (VGA/noVNC)" {
linux /boot/vmlinuz-lts modules=loop,squashfs,sd-mod,usb-storage quiet loglevel=1 console=tty0
initrd /boot/initramfs-lts
}

menuentry "MinimalRouter Installer (serial ttyS0 115200)" {
linux /boot/vmlinuz-lts modules=loop,squashfs,sd-mod,usb-storage quiet loglevel=1 minimalrouter.console=ttyS0 console=ttyS0,115200
initrd /boot/initramfs-lts
}
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
need docker

mkdir -p "$BUILD_DIR" "$CACHE_DIR"

echo "=== MinimalRouter v$VERSION Golden Appliance ISO Builder ==="
echo "[1/6] Preparing MinimalRouter distribution and installed rootfs..."
if [ "$USE_EXISTING_DIST" = "1" ]; then
    echo "Using existing distribution: $DIST_DIR"
else
    make \
        BUILD_VERSION="$VERSION" \
        BUILD_COMMIT="$BUILD_COMMIT" \
        BUILD_DATE="$BUILD_DATE" \
        dist-amd64
fi
[ -d "$DIST_DIR" ] || { echo "ERROR: distribution directory is missing" >&2; exit 1; }
[ -x "$DIST_DIR/bin/routerd-amd64" ] || { echo "ERROR: distribution is missing routerd-amd64" >&2; exit 1; }
[ -x "$DIST_DIR/bin/router-applyd-amd64" ] || { echo "ERROR: distribution is missing router-applyd-amd64" >&2; exit 1; }
if [ "$REQUIRE_SIGNED_DIST" = "1" ]; then
    [ -s "$DIST_DIR/firmware-signing.pub" ] || {
        echo "ERROR: release ISO requires a signed distribution with firmware-signing.pub" >&2
        exit 1
    }
fi
printf '%s\n' "$VERSION" > "$DIST_DIR/VERSION"
sh packaging/alpine/build-rootfs.sh

echo "[2/6] Building the bootable golden disk image..."
sh packaging/alpine/build-golden-image.sh
[ -s "$GOLDEN_IMAGE" ] || { echo "ERROR: golden image was not produced" >&2; exit 1; }
[ -s "$GOLDEN_SHA" ] || { echo "ERROR: golden image checksum was not produced" >&2; exit 1; }
[ -s "$GOLDEN_BYTES" ] || { echo "ERROR: golden image size was not produced" >&2; exit 1; }

echo "[3/6] Downloading verified Alpine ${ALPINE_VERSION} installer shell..."
fetch_file "$ALPINE_ISO_URL" "$BASE_ISO"
fetch_file "$ALPINE_SHA_URL" "$BASE_SHA"
(
    cd "$CACHE_DIR"
    sha256sum -c "$(basename "$BASE_SHA")"
)

echo "[4/6] Building the tiny live flasher overlay..."
build_apkovl
build_syslinux_config
build_grub_config

echo "[5/6] Injecting the golden image and remastering ISO..."
rm -rf "$INJECT_DIR"
mkdir -p "$INJECT_DIR/minimalrouter"
printf '%s\n' "$VERSION" > "$INJECT_DIR/minimalrouter/VERSION"
printf '%s\n' "$BUILD_COMMIT" > "$INJECT_DIR/minimalrouter/BUILD_COMMIT"
printf '%s\n' "$BUILD_DATE" > "$INJECT_DIR/minimalrouter/BUILD_DATE"
cat > "$INJECT_DIR/minimalrouter/BUILD-INFO" <<EOF
product=minimalrouter
version=$VERSION
commit=$BUILD_COMMIT
build_date=$BUILD_DATE
alpine_version=$ALPINE_VERSION
architecture=amd64
install_model=golden-image
EOF
cp "$GOLDEN_IMAGE" "$INJECT_DIR/minimalrouter/golden.img.gz"
cp "$GOLDEN_SHA" "$INJECT_DIR/minimalrouter/golden.img.gz.sha256"
# Uncompressed size, so the flasher can prove it wrote the whole image.
cp "$GOLDEN_BYTES" "$INJECT_DIR/minimalrouter/golden.img.bytes"

rm -f "$OUT_ISO" "$OUT_SHA"
xorriso \
    -indev "$BASE_ISO" \
    -outdev "$OUT_ISO" \
    -boot_image any replay \
    -map "$INJECT_DIR/minimalrouter" /minimalrouter \
    -map "$BUILD_DIR/minimalrouter.apkovl.tar.gz" /minimalrouter.apkovl.tar.gz \
    -map "$BUILD_DIR/syslinux.cfg" /boot/syslinux/syslinux.cfg \
    -map "$BUILD_DIR/grub.cfg" /boot/grub/grub.cfg \
    -volid "MR_${VOLUME_VERSION}" \
    -commit \
    -end

iso_ls_has /minimalrouter VERSION || { echo "ERROR: final ISO is missing VERSION" >&2; exit 1; }
iso_ls_has /minimalrouter BUILD-INFO || { echo "ERROR: final ISO is missing BUILD-INFO" >&2; exit 1; }
iso_ls_has /minimalrouter golden.img.gz || { echo "ERROR: final ISO is missing golden.img.gz" >&2; exit 1; }
iso_ls_has /minimalrouter golden.img.gz.sha256 || { echo "ERROR: final ISO is missing golden image checksum" >&2; exit 1; }
iso_ls_has /minimalrouter golden.img.bytes || { echo "ERROR: final ISO is missing golden image size" >&2; exit 1; }
iso_ls_has / minimalrouter.apkovl.tar.gz || { echo "ERROR: final ISO is missing live flasher overlay" >&2; exit 1; }
iso_ls_has /boot vmlinuz-lts || { echo "ERROR: final ISO is missing live vmlinuz-lts" >&2; exit 1; }
iso_ls_has /boot initramfs-lts || { echo "ERROR: final ISO is missing live initramfs-lts" >&2; exit 1; }
iso_ls_has /boot/grub grub.cfg || { echo "ERROR: final ISO is missing UEFI GRUB config" >&2; exit 1; }

sha256sum "$OUT_ISO" > "$OUT_SHA"

echo "[6/6] ISO complete."
ls -lh "$OUT_ISO" "$OUT_SHA" "$GOLDEN_IMAGE"
echo "ISO: $OUT_ISO"
echo "SHA256: $OUT_SHA"
