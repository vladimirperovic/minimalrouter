#!/bin/sh
# Minimal Router OS — bootable all-in-one ISO builder.
# Remasters the verified Alpine 3.22 Extended ISO, adds a signed Alpine package
# bundle, the MinimalRouter distribution and an apkovl that starts the installer.
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

BUILD_DIR="build/iso"
CACHE_DIR="$BUILD_DIR/cache"
APK_DIR="$BUILD_DIR/apks"
APK_REPO_DIR="$BUILD_DIR/apk-repo"
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
REQUIRED_PACKAGES="alpine-base alpine-conf linux-lts linux-firmware-none e2fsprogs grub grub-efi syslinux dosfstools util-linux nftables ppp ppp-pppoe dnsmasq iproute2 iputils-ping iputils-arping ca-certificates openssh-server wireguard-tools-wg doas squid hostapd hostapd-openrc iw inadyn inadyn-openrc chrony chrony-openrc logrotate"

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
        docker run --rm --platform linux/amd64 \
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
                  alpine-base alpine-conf linux-lts linux-firmware-none e2fsprogs grub grub-efi syslinux dosfstools util-linux \
                  nftables ppp ppp-pppoe dnsmasq iproute2 iputils-ping iputils-arping ca-certificates openssh-server \
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

build_offline_repos() {
    # setup-disk performs a normal apk transaction into --root /mnt. Provide
    # normal signed Alpine repositories instead of a flat directory of APKs.
    rm -rf "$APK_REPO_DIR"
    mkdir -p         "$APK_REPO_DIR/main/$ALPINE_ARCH"         "$APK_REPO_DIR/community/$ALPINE_ARCH"

    fetch_file         "https://dl-cdn.alpinelinux.org/alpine/${ALPINE_BRANCH}/main/${ALPINE_ARCH}/APKINDEX.tar.gz"         "$APK_REPO_DIR/main/$ALPINE_ARCH/APKINDEX.tar.gz"
    fetch_file         "https://dl-cdn.alpinelinux.org/alpine/${ALPINE_BRANCH}/community/${ALPINE_ARCH}/APKINDEX.tar.gz"         "$APK_REPO_DIR/community/$ALPINE_ARCH/APKINDEX.tar.gz"

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
        docker run --rm --platform linux/amd64             -v "$repo_root:/work:ro"             "alpine:${ALPINE_BRANCH#v}"             /bin/sh -ec '''
                printf "%s\n"                   "/work/build/iso/apk-repo/main"                   "/work/build/iso/apk-repo/community"                   > /etc/apk/repositories
                apk update --no-network >/dev/null
                mkdir -p /tmp/mr-fetch
                apk fetch --no-network --recursive --output /tmp/mr-fetch                   alpine-base e2fsprogs linux-lts openssl syslinux >/dev/null
                for pkg in alpine-base e2fsprogs linux-lts openssl syslinux; do
                    ls /tmp/mr-fetch/${pkg}-*.apk >/dev/null 2>&1 || {
                        echo "offline repository validation did not fetch $pkg" >&2
                        exit 1
                    }
                done
            '''
    fi
}

build_apkovl() {
    rm -rf "$OVERLAY_DIR"
    mkdir -p \
        "$OVERLAY_DIR/etc/minimalrouter" \
        "$OVERLAY_DIR/etc/init.d" \
        "$OVERLAY_DIR/etc/runlevels/default"
    install -m 0755 packaging/alpine/live-installer.sh "$OVERLAY_DIR/etc/minimalrouter/live-installer.sh"

    # Alpine installs its stock /etc/inittab after apkovl processing, so using a
    # custom tty1 entry there is not reliable. A dedicated OpenRC default service
    # survives package installation, disables tty1's login getty at runtime and
    # attaches the appliance installer directly to tty1. tty2-tty4 remain normal
    # emergency consoles.
    cat > "$OVERLAY_DIR/etc/init.d/minimalrouter-installer" <<'EOF'
#!/sbin/openrc-run
name="minimalrouter-installer"
description="Minimal Router OS appliance installer"

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
    ebegin "Launching Minimal Router OS installer on ${INSTALL_TTY#/dev/}"

    # Keep prompts readable; diagnostics remain available through dmesg.
    dmesg -n 1 >/dev/null 2>&1 || true

    # The installer owns only its selected TTY. The other console remains a
    # recovery path instead of being killed unconditionally.
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
  MENU LABEL Minimal Router OS Installer (VGA/noVNC)
  KERNEL /boot/vmlinuz-lts
  INITRD /boot/initramfs-lts
  APPEND modules=loop,squashfs,sd-mod,usb-storage modloop=/boot/modloop-lts quiet loglevel=1 console=tty0

LABEL minimalrouter-serial
  MENU LABEL Minimal Router OS Installer (serial ttyS0 115200)
  KERNEL /boot/vmlinuz-lts
  INITRD /boot/initramfs-lts
  APPEND modules=loop,squashfs,sd-mod,usb-storage modloop=/boot/modloop-lts quiet loglevel=1 minimalrouter.console=ttyS0 console=ttyS0,115200
EOF
}

build_grub_config() {
    # UEFI/OVMF boot path gets the same serial console as the BIOS/syslinux path.
    cat > "$BUILD_DIR/grub.cfg" <<'EOF'
set timeout=1

serial --unit=0 --speed=115200 --word=8 --parity=no --stop=1
terminal_input console serial
terminal_output console serial

menuentry "Minimal Router OS Installer (VGA/noVNC)" {
linux	/boot/vmlinuz-lts modules=loop,squashfs,sd-mod,usb-storage quiet loglevel=1 console=tty0
initrd	/boot/initramfs-lts
}

menuentry "Minimal Router OS Installer (serial ttyS0 115200)" {
linux	/boot/vmlinuz-lts modules=loop,squashfs,sd-mod,usb-storage quiet loglevel=1 minimalrouter.console=ttyS0 console=ttyS0,115200
initrd	/boot/initramfs-lts
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

mkdir -p "$BUILD_DIR" "$CACHE_DIR"

echo "=== Minimal Router OS v$VERSION ISO Builder ==="
echo "[1/7] Building the MinimalRouter AMD64 distribution..."
make dist-amd64
DIST_DIR="build/dist/minimalrouter-linux-amd64"
[ -d "$DIST_DIR" ] || { echo "ERROR: distribution directory is missing" >&2; exit 1; }
cp VERSION "$DIST_DIR/VERSION"

echo "[2/7] Downloading Alpine Linux ${ALPINE_VERSION} Extended ISO..."
fetch_file "$ALPINE_ISO_URL" "$BASE_ISO"
fetch_file "$ALPINE_SHA_URL" "$BASE_SHA"
(
    cd "$CACHE_DIR"
    sha256sum -c "$(basename "$BASE_SHA")"
)

echo "[3/7] Fetching the complete offline Alpine package bundle..."
fetch_apks
build_offline_repos

echo "[4/7] Building MinimalRouter boot overlay..."
build_apkovl
build_syslinux_config
build_grub_config

echo "[5/7] Preparing ISO payload..."
rm -rf "$INJECT_DIR"
mkdir -p "$INJECT_DIR/minimalrouter"
cp -a "$DIST_DIR" "$INJECT_DIR/minimalrouter/minimalrouter-linux-amd64"
cp -a "$APK_DIR" "$INJECT_DIR/minimalrouter/apks"
cp -a "$APK_REPO_DIR" "$INJECT_DIR/minimalrouter/repo"
cp VERSION "$INJECT_DIR/minimalrouter/VERSION"
cp "$APK_MANIFEST" "$INJECT_DIR/minimalrouter/APK-SHA256SUMS"
printf '%s\n' "$ALPINE_VERSION" > "$INJECT_DIR/minimalrouter/ALPINE_VERSION"
printf '%s\n' "${GITHUB_SHA:-unknown}" > "$INJECT_DIR/minimalrouter/BUILD_COMMIT"

rm -f "$OUT_ISO" "$OUT_SHA"

echo "[6/7] Remastering the bootable Alpine ISO..."
# The live kernel stays the base ISO's own (6.12.94): the base initramfs
# carries storage modules built for exactly that kernel, and booting a newer
# kernel makes the initramfs fail to load them ("mounting boot media failed").
# The live environment gets the booted kernel's modules from the base ISO's
# modloop-lts, mounted explicitly by the installer (see live-installer.sh).

# xorriso replay preserves the original Alpine BIOS/UEFI hybrid boot equipment
# (kernel, initramfs and modloop stay the base ISO's own — the live boot is a
# pure installer; the installed system runs its own bundle-matched kernel).
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

# Verify the actual ISO directories rather than relying on xorriso -find output,
# whose printed path format differs across xorriso releases.
iso_ls_has /minimalrouter VERSION || { echo "ERROR: final ISO is missing /minimalrouter/VERSION" >&2; exit 1; }
iso_ls_has /minimalrouter minimalrouter-linux-amd64 || { echo "ERROR: final ISO is missing the MinimalRouter distribution" >&2; exit 1; }
iso_ls_has /minimalrouter/minimalrouter-linux-amd64 install.sh || { echo "ERROR: final ISO is missing install.sh" >&2; exit 1; }
iso_ls_has /minimalrouter/minimalrouter-linux-amd64 install-core.sh || { echo "ERROR: final ISO is missing install-core.sh" >&2; exit 1; }
iso_ls_has /minimalrouter/minimalrouter-linux-amd64/bin router-setup-amd64 || { echo "ERROR: final ISO is missing router-setup-amd64" >&2; exit 1; }
iso_ls_has /minimalrouter/repo/main/x86_64 APKINDEX.tar.gz || { echo "ERROR: final ISO is missing the signed Alpine main index" >&2; exit 1; }
iso_ls_has /minimalrouter/repo/community/x86_64 APKINDEX.tar.gz || { echo "ERROR: final ISO is missing the signed Alpine community index" >&2; exit 1; }
iso_ls_has / minimalrouter.apkovl.tar.gz || { echo "ERROR: final ISO is missing the boot overlay" >&2; exit 1; }
iso_ls_has /boot vmlinuz-lts || { echo "ERROR: final ISO is missing vmlinuz-lts" >&2; exit 1; }
iso_ls_has /boot initramfs-lts || { echo "ERROR: final ISO is missing initramfs-lts" >&2; exit 1; }
iso_ls_has /boot modloop-lts || { echo "ERROR: final ISO is missing modloop-lts" >&2; exit 1; }
iso_ls_has /boot/grub grub.cfg || { echo "ERROR: final ISO is missing the custom UEFI GRUB config" >&2; exit 1; }
ls "$APK_DIR"/openssh-server-*.apk >/dev/null 2>&1 || { echo "ERROR: final ISO package bundle is missing openssh-server" >&2; exit 1; }

sha256sum "$OUT_ISO" > "$OUT_SHA"

echo "[7/7] ISO complete."
ls -lh "$OUT_ISO" "$OUT_SHA"
echo "ISO: $OUT_ISO"
echo "SHA256: $OUT_SHA"
