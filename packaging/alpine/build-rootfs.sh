#!/bin/sh
set -eu
VERSION="$(tr -d '\r\n' < VERSION)"
OUT="build/iso/minimalrouter-rootfs-${VERSION}-amd64.tar.gz"
DIST="build/dist/minimalrouter-linux-amd64"
[ -d "$DIST" ] || { echo "ERROR: build distribution first" >&2; exit 1; }
command -v docker >/dev/null 2>&1 || { echo "ERROR: Docker is required to build the rootfs" >&2; exit 1; }
rm -f "$OUT"
repo="$(pwd)"
docker run --rm --platform linux/amd64 \
  -v "$repo:/work" -w /work alpine:3.22 /bin/sh -ec '
    ROOT=/tmp/rootfs
    rm -rf "$ROOT"; mkdir -p "$ROOT/etc/apk"
    printf "%s\n" \
      https://dl-cdn.alpinelinux.org/alpine/v3.22/main \
      https://dl-cdn.alpinelinux.org/alpine/v3.22/community > /tmp/repos
    apk --root "$ROOT" --arch x86_64 --initdb --repositories-file /tmp/repos \
      --no-cache add alpine-base alpine-conf linux-lts linux-firmware-none e2fsprogs \
      grub grub-efi syslinux dosfstools util-linux nftables ppp ppp-pppoe dnsmasq \
      iproute2 iputils-ping iputils-arping ca-certificates openssh-server \
      wireguard-tools-wg doas squid hostapd hostapd-openrc iw inadyn inadyn-openrc \
      chrony chrony-openrc logrotate
    cp /tmp/repos "$ROOT/etc/apk/repositories"
    mkdir -p "$ROOT/root/minimalrouter-installer"
    cp -a /work/build/dist/minimalrouter-linux-amd64/. "$ROOT/root/minimalrouter-installer/"
    # chroot gets a minimal pseudo-runtime sufficient for package/service registration.
    mkdir -p "$ROOT/proc" "$ROOT/sys" "$ROOT/dev" "$ROOT/run"
    mount -t proc proc "$ROOT/proc"
    mount --rbind /dev "$ROOT/dev"
    mount --rbind /sys "$ROOT/sys"
    MINIMALROUTER_IMAGE_BUILD=1 chroot "$ROOT" /bin/sh -c \
      "cd /root/minimalrouter-installer && MINIMALROUTER_IMAGE_BUILD=1 ./install.sh --offline"
    umount -R "$ROOT/sys" 2>/dev/null || true
    umount -R "$ROOT/dev" 2>/dev/null || true
    umount "$ROOT/proc" 2>/dev/null || true
    rm -rf "$ROOT/root/minimalrouter-installer" "$ROOT/var/cache/apk"/*
    rm -f "$ROOT/etc/ssh"/ssh_host_* "$ROOT/etc/machine-id"
    mkdir -p "$ROOT/etc/minimalrouter"
    printf "%s\n" "'$VERSION'" > "$ROOT/etc/minimalrouter/VERSION"
    tar -C "$ROOT" -czf /work/build/iso/minimalrouter-rootfs-'$VERSION'-amd64.tar.gz .
  '
[ -s "$OUT" ] || { echo "ERROR: rootfs archive was not created" >&2; exit 1; }
tar -tzf "$OUT" | grep -q '^./boot/vmlinuz-lts$' || { echo "ERROR: rootfs missing linux-lts kernel" >&2; exit 1; }
tar -tzf "$OUT" | grep -q '^./usr/sbin/router-applyd$' || { echo "ERROR: rootfs missing minimalrouter" >&2; exit 1; }
sha256sum "$OUT" > "$OUT.sha256"
echo "Built appliance rootfs: $OUT"
