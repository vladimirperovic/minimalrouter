#!/bin/sh
set -eu
VERSION="$(tr -d '\r\n' < VERSION)"
OUT="build/iso/minimalrouter-rootfs-${VERSION}-amd64.tar.gz"
DIST="build/dist/minimalrouter-linux-amd64"
[ -d "$DIST" ] || { echo "ERROR: build distribution first" >&2; exit 1; }
command -v docker >/dev/null 2>&1 || { echo "ERROR: Docker is required to build the rootfs" >&2; exit 1; }
rm -f "$OUT"
repo="$(pwd)"
. packaging/alpine/alpine-release
BUILDER_IMAGE="alpine:$ALPINE_VERSION"

# Trust starts with the pulled official builder keyring. Repository metadata
# and any key upgrades must verify under those keys; never recover a key
# mismatch by accepting an unauthenticated replacement trust root.
docker pull --platform linux/amd64 "$BUILDER_IMAGE" >/dev/null

docker run --rm --platform linux/amd64 -e "ALPINE_BRANCH=$ALPINE_BRANCH" \
  -v "$repo:/work" -w /work "$BUILDER_IMAGE" /bin/sh -ec '
    apk update
    apk add --no-cache --upgrade alpine-keys
    ROOT=/tmp/rootfs
    rm -rf "$ROOT"; mkdir -p "$ROOT/etc/apk/keys"
    printf "%s\n" \
      "https://dl-cdn.alpinelinux.org/alpine/$ALPINE_BRANCH/main" \
      "https://dl-cdn.alpinelinux.org/alpine/$ALPINE_BRANCH/community" > /tmp/repos

    # apk --root verifies repositories using the target roots keyring, not the
    # apk clients own /etc/apk/keys. Seed the signature-verified official
    # Alpine keys before the very first target-root package transaction.
    cp -a /etc/apk/keys/. "$ROOT/etc/apk/keys/"

    PACKAGES="$(sed -e "s/#.*//" /work/packaging/alpine/rootfs-packages)"
    if ! apk --root "$ROOT" --arch x86_64 --initdb --repositories-file /tmp/repos \
        --no-cache add $PACKAGES > /tmp/apk-add.log 2>&1; then
      cat /tmp/apk-add.log
      # apk 3 (Alpine 3.24) reports a failing package trigger as an error. The
      # grub and syslinux triggers only try to detect a boot device in this
      # build root, which has none (no /dev or /proc); the Golden image installs
      # its bootloader later, in build-golden-image.sh. Tolerate exactly those
      # two, fail on any other error, and prove every package is installed.
      unexpected="$(grep "^ERROR:" /tmp/apk-add.log | grep -vE "exec/(grub|syslinux)-[^ ]*[.]trigger" || true)"
      [ -z "$unexpected" ] || { echo "ERROR: unexpected apk errors:" >&2; echo "$unexpected" >&2; exit 1; }
      for package in $PACKAGES; do
        apk --root "$ROOT" info -e "$package" > /dev/null || { echo "ERROR: package $package was not installed" >&2; exit 1; }
      done
      echo "NOTE: tolerated only the grub/syslinux boot-device trigger errors above"
    else
      cat /tmp/apk-add.log
    fi
    cp /tmp/repos "$ROOT/etc/apk/repositories"

    # First boot verifies the prebuilt ext4 filesystem with resize2fs before any
    # router state is written. Alpine ships that binary in e2fsprogs-extra, not
    # the base e2fsprogs package, so fail the image build here instead of leaving
    # an unbootable appliance for the live flasher to discover later.
    [ -x "$ROOT/usr/sbin/resize2fs" ] || {
      echo "ERROR: installed rootfs is missing /usr/sbin/resize2fs" >&2
      exit 1
    }

    mkdir -p "$ROOT/root/minimalrouter-installer"
    cp -a /work/build/dist/minimalrouter-linux-amd64/. "$ROOT/root/minimalrouter-installer/"

    # Image-build mode in install.sh deliberately skips runtime kernel/module and
    # sysctl checks, so a privileged container and bind-mounted /proc,/sys,/dev
    # are unnecessary. alpine-baselayout normally creates these basic device
    # nodes during apk --root; create only a missing node instead of replacing it.
    mkdir -p "$ROOT/dev" "$ROOT/proc" "$ROOT/sys" "$ROOT/run"
    [ -c "$ROOT/dev/null" ] || { rm -f "$ROOT/dev/null"; mknod -m 666 "$ROOT/dev/null" c 1 3; }
    [ -c "$ROOT/dev/zero" ] || { rm -f "$ROOT/dev/zero"; mknod -m 666 "$ROOT/dev/zero" c 1 5; }
    [ -c "$ROOT/dev/random" ] || { rm -f "$ROOT/dev/random"; mknod -m 666 "$ROOT/dev/random" c 1 8; }
    [ -c "$ROOT/dev/urandom" ] || { rm -f "$ROOT/dev/urandom"; mknod -m 666 "$ROOT/dev/urandom" c 1 9; }

    MINIMALROUTER_IMAGE_BUILD=1 chroot "$ROOT" /bin/sh -c \
      "cd /root/minimalrouter-installer && MINIMALROUTER_IMAGE_BUILD=1 ./install.sh --offline"

    # Remove kernel subsystems a headless router never loads, proving first
    # that no kept or required module depends on them, then rebuild the
    # initramfs from that same tree so kernel, initramfs and /lib/modules stay
    # one validated set. kmod is a builder tool only, not a target package.
    apk add --no-cache kmod >/dev/null
    sh /work/packaging/alpine/prune-kernel-modules.sh "$ROOT" \
      /work/packaging/alpine/minimalrouter.modules \
      /work/packaging/alpine/kernel-modules.required
    KERNEL_RELEASE="$(basename "$(find "$ROOT/lib/modules" -mindepth 1 -maxdepth 1 -type d)")"
    chroot "$ROOT" mkinitfs -o /boot/initramfs-lts "$KERNEL_RELEASE"

    # apk --root --initdb does not create the runlevels that setup-alpine and
    # setup-disk would. Without mdev + hwdrivers in sysinit, only NIC drivers
    # the initramfs already loaded (virtio_net) ever appear: Hyper-V, e1000 and
    # physical Intel/Realtek ports stayed invisible and firstboot stopped with
    # "at least two usable network interfaces are required". Without the
    # shutdown set, reboot never remounted the root filesystem read-only.
    for svc in devfs dmesg mdev hwdrivers; do
      chroot "$ROOT" rc-update add "$svc" sysinit >/dev/null
    done
    for svc in killprocs mount-ro savecache; do
      chroot "$ROOT" rc-update add "$svc" shutdown >/dev/null
    done

    rm -rf "$ROOT/root/minimalrouter-installer" "$ROOT/var/cache/apk"/*
    rm -f "$ROOT/etc/ssh"/ssh_host_* "$ROOT/etc/machine-id"
    mkdir -p "$ROOT/etc/minimalrouter"
    printf "%s\n" "'$VERSION'" > "$ROOT/etc/minimalrouter/VERSION"

    # Keep the management instructions visible after installation as well as on
    # the final installer screen. Alpine getty displays /etc/issue before login,
    # and /etc/motd repeats the same information after a console/SSH login.
    cat > "$ROOT/etc/issue" <<"ISSUE"
+----------------------------------------------------------+
|                      minimalrouter                       |
+----------------------------------------------------------+

Router management after boot:
  Web Dashboard: https://192.168.1.1:8443
  SSH recovery:  ssh root@192.168.1.1
  Serial:        ttyS0 @ 115200

Connect your computer to the LAN interface, then open the Web Dashboard URL
exactly as written above, including https:// and port :8443.

ISSUE
    cp "$ROOT/etc/issue" "$ROOT/etc/motd"

    tar -C "$ROOT" -czf /work/build/iso/minimalrouter-rootfs-'$VERSION'-amd64.tar.gz .
  '
[ -s "$OUT" ] || { echo "ERROR: rootfs archive was not created" >&2; exit 1; }
tar -tzf "$OUT" | grep -q '^./boot/vmlinuz-lts$' || { echo "ERROR: rootfs missing linux-lts kernel" >&2; exit 1; }
tar -tzf "$OUT" | grep -q '^./usr/sbin/router-applyd$' || { echo "ERROR: rootfs missing minimalrouter" >&2; exit 1; }
tar -tzf "$OUT" | grep -q '^./usr/sbin/resize2fs$' || { echo "ERROR: rootfs missing resize2fs" >&2; exit 1; }
for entry in sysinit/devfs sysinit/dmesg sysinit/mdev sysinit/hwdrivers \
  shutdown/killprocs shutdown/mount-ro shutdown/savecache; do
  tar -tzf "$OUT" | grep -qx "./etc/runlevels/$entry" || {
    echo "ERROR: rootfs runlevel $entry is missing; non-VirtIO NICs would not be detected" >&2
    exit 1
  }
done

# This text is part of the appliance UX, not optional documentation. Prove the
# exact installed rootfs contains the address the user must open after reboot.
tar -xOf "$OUT" ./etc/issue | grep -Fq 'Web Dashboard: https://192.168.1.1:8443' || {
  echo "ERROR: installed console is missing the Web Dashboard management URL" >&2
  exit 1
}
tar -xOf "$OUT" ./etc/motd | grep -Fq 'including https:// and port :8443' || {
  echo "ERROR: installed MOTD is missing the dashboard port guidance" >&2
  exit 1
}

# build-iso.sh renames this archive to /minimalrouter/rootfs.tar.gz. The checksum
# manifest must therefore name the shipped file, not this builder-side path.
rootfs_sha="$(sha256sum "$OUT" | awk '{print $1}')"
printf '%s  rootfs.tar.gz\n' "$rootfs_sha" > "$OUT.sha256"
echo "Built appliance rootfs: $OUT"
