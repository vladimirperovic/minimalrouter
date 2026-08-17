#!/bin/sh
# Build the exact bootable disk image written by the MinimalRouter ISO.
# All Alpine packages, kernel/initramfs and MinimalRouter files are assembled
# here in CI. The user's VM never runs apk, mkinitfs, setup-disk or install-core.
set -eu

VERSION="$(tr -d '\r\n' < VERSION)"
[ -n "$VERSION" ] || { echo "ERROR: VERSION is empty" >&2; exit 1; }

BUILD_DIR="build/iso"
ROOTFS="$BUILD_DIR/minimalrouter-rootfs-${VERSION}-amd64.tar.gz"
RAW="$BUILD_DIR/minimalrouter-golden-${VERSION}-amd64.img"
OUT="$RAW.gz"
OUT_SHA="$OUT.sha256"
MNT="$BUILD_DIR/golden-mnt"
IMAGE_BYTES="${MINIMALROUTER_GOLDEN_BYTES:-8589934592}"

[ -s "$ROOTFS" ] || { echo "ERROR: build the appliance rootfs first: $ROOTFS" >&2; exit 1; }
[ "$IMAGE_BYTES" -ge 8589934592 ] || { echo "ERROR: golden image must be at least 8 GiB" >&2; exit 1; }

for cmd in losetup sfdisk mkfs.ext4 mount umount extlinux gzip sha256sum tar; do
    command -v "$cmd" >/dev/null 2>&1 || { echo "ERROR: required golden-image build tool is missing: $cmd" >&2; exit 1; }
done

if [ "$(id -u)" -eq 0 ]; then
    SUDO=""
elif command -v sudo >/dev/null 2>&1; then
    SUDO="sudo"
else
    echo "ERROR: root or sudo is required to create the bootable disk image" >&2
    exit 1
fi

LOOP=""
cleanup() {
    set +e
    mountpoint -q "$MNT" 2>/dev/null && $SUDO umount "$MNT"
    [ -n "$LOOP" ] && $SUDO losetup -d "$LOOP" >/dev/null 2>&1
    rmdir "$MNT" >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

rm -f "$RAW" "$OUT" "$OUT_SHA"
mkdir -p "$BUILD_DIR" "$MNT"
truncate -s "$IMAGE_BYTES" "$RAW"

# Build the final MBR + ext4 layout once in CI. One active Linux partition keeps
# the Proxmox/SeaBIOS appliance path deliberately small and auditable.
LOOP="$($SUDO losetup --find --show "$RAW")"
printf 'label: dos\nstart=1MiB, type=83, bootable\n' | $SUDO sfdisk "$LOOP" >/tmp/minimalrouter-golden-sfdisk.log 2>&1 || {
    cat /tmp/minimalrouter-golden-sfdisk.log >&2 || true
    echo "ERROR: could not partition golden image" >&2
    exit 1
}
$SUDO losetup -d "$LOOP"
LOOP=""

LOOP="$($SUDO losetup --find --show --partscan "$RAW")"
ROOTPART="${LOOP}p1"
for _ in 1 2 3 4 5; do
    [ -b "$ROOTPART" ] && break
    sleep 1
done
[ -b "$ROOTPART" ] || { echo "ERROR: golden root partition did not appear: $ROOTPART" >&2; exit 1; }

$SUDO mkfs.ext4 -F -L minimalrouter-root "$ROOTPART" >/tmp/minimalrouter-golden-mkfs.log 2>&1 || {
    cat /tmp/minimalrouter-golden-mkfs.log >&2 || true
    echo "ERROR: could not create golden ext4 filesystem" >&2
    exit 1
}
$SUDO mount "$ROOTPART" "$MNT"
$SUDO tar --numeric-owner -xzf "$ROOTFS" -C "$MNT"

# Golden-image-only first boot. router-setup is intentionally retained here even
# though the normal distribution installer does not need it after provisioning.
$SUDO install -m 0755 packaging/alpine/firstboot.sh "$MNT/usr/libexec/minimalrouter/firstboot"
$SUDO install -m 0755 packaging/alpine/firstboot.initd "$MNT/etc/init.d/minimalrouter-firstboot"
$SUDO ln -sf /etc/init.d/minimalrouter-firstboot "$MNT/etc/runlevels/default/minimalrouter-firstboot"
$SUDO install -m 0755 "build/dist/minimalrouter-linux-amd64/bin/router-setup-amd64" "$MNT/usr/sbin/router-setup"
$SUDO rm -f "$MNT/etc/minimalrouter/firstboot-complete"

cat > "$BUILD_DIR/golden-fstab" <<'EOF'
LABEL=minimalrouter-root / ext4 defaults,noatime 0 1
EOF
$SUDO install -m 0644 "$BUILD_DIR/golden-fstab" "$MNT/etc/fstab"

cat > "$BUILD_DIR/golden-installed" <<EOF
version=$VERSION
installed_by=golden-image
image_layout=mbr-ext4
EOF
$SUDO install -m 0644 "$BUILD_DIR/golden-installed" "$MNT/etc/minimalrouter/installed"

# The rootfs builder already generated initramfs from the exact linux-lts package
# installed in this image. Never regenerate it against the live ISO kernel.
[ -s "$MNT/boot/vmlinuz-lts" ] || { echo "ERROR: golden image is missing vmlinuz-lts" >&2; exit 1; }
[ -s "$MNT/boot/initramfs-lts" ] || { echo "ERROR: golden image is missing initramfs-lts" >&2; exit 1; }
KERNEL_RELEASE="$(basename "$(find "$MNT/lib/modules" -mindepth 1 -maxdepth 1 -type d | head -1)")"
[ -n "$KERNEL_RELEASE" ] || { echo "ERROR: golden image is missing kernel modules" >&2; exit 1; }

$SUDO mkdir -p "$MNT/boot/extlinux"
cat > "$BUILD_DIR/golden-extlinux.conf" <<'EOF'
DEFAULT minimalrouter
PROMPT 0
TIMEOUT 10
SERIAL 0 115200

LABEL minimalrouter
  LINUX /boot/vmlinuz-lts
  INITRD /boot/initramfs-lts
  APPEND root=LABEL=minimalrouter-root rootfstype=ext4 modules=sd-mod,virtio_blk,virtio_pci,ext4 quiet console=tty0 console=ttyS0,115200
EOF
$SUDO install -m 0644 "$BUILD_DIR/golden-extlinux.conf" "$MNT/boot/extlinux/extlinux.conf"
$SUDO extlinux --install "$MNT/boot/extlinux" >/tmp/minimalrouter-golden-extlinux.log 2>&1 || {
    cat /tmp/minimalrouter-golden-extlinux.log >&2 || true
    echo "ERROR: could not install extlinux into golden image" >&2
    exit 1
}

MBR="$(find /usr/lib/syslinux /usr/share/syslinux /usr/lib/EXTLINUX -type f -name mbr.bin 2>/dev/null | head -1 || true)"
[ -n "$MBR" ] || { echo "ERROR: syslinux mbr.bin was not found on the CI builder" >&2; exit 1; }
$SUDO dd if="$MBR" of="$LOOP" bs=440 count=1 conv=notrunc status=none

# A cloned appliance must generate its own identity on first boot.
$SUDO rm -f "$MNT/etc/ssh"/ssh_host_* "$MNT/etc/machine-id"
$SUDO sync
$SUDO umount "$MNT"
$SUDO losetup -d "$LOOP"
LOOP=""

# Compression makes the 8 GiB logical appliance practical to ship in the ISO;
# zero/unallocated blocks collapse while the flasher still writes one exact disk.
gzip -1 -c "$RAW" > "$OUT"
rm -f "$RAW"
[ -s "$OUT" ] || { echo "ERROR: compressed golden image was not created" >&2; exit 1; }
gzip -t "$OUT"
GOLDEN_SHA="$(sha256sum "$OUT" | awk '{print $1}')"
printf '%s  golden.img.gz\n' "$GOLDEN_SHA" > "$OUT_SHA"

printf 'Built golden image: %s\n' "$OUT"
printf 'Kernel/modules: %s\n' "$KERNEL_RELEASE"
ls -lh "$OUT" "$OUT_SHA"
