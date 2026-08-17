from pathlib import Path


def repl(path, old, new):
    p=Path(path); s=p.read_text()
    if old not in s: raise SystemExit(f'marker missing in {path}: {old[:80]!r}')
    p.write_text(s.replace(old,new,1))

# 1) Allow building the installed appliance filesystem in a chroot without
# probing the build host kernel/network runtime.
p='packaging/alpine/install-dist.sh'
s=Path(p).read_text()
if 'IMAGE_BUILD=' not in s:
    s=s.replace('ALPINE_VERSION="v3.22"\nOFFLINE_MODE=0\n', 'ALPINE_VERSION="v3.22"\nIMAGE_BUILD="${MINIMALROUTER_IMAGE_BUILD:-0}"\nOFFLINE_MODE=0\n',1)
    old='''while IFS= read -r module; do
    case "$module" in ""|\\#*) continue ;; esac
    if ! modprobe "$module" 2>/dev/null && ! find /lib/modules -name "${module}.ko*" 2>/dev/null | grep -q .; then
        if [ "$module" = "pppoe" ]; then
            echo "ERROR: the running Alpine kernel does not provide the required PPPoE module." >&2
            echo "The 2026-08-01 Proxmox pilot required linux-lts; boot linux-lts, confirm 'modprobe pppoe', then rerun this installer." >&2
        else
            echo "ERROR: required kernel module '$module' could not be loaded." >&2
        fi
        exit 1
    fi
done < modules/minimalrouter.conf
'''
    new='''if [ "$IMAGE_BUILD" != "1" ]; then
while IFS= read -r module; do
    case "$module" in ""|\\#*) continue ;; esac
    if ! modprobe "$module" 2>/dev/null && ! find /lib/modules -name "${module}.ko*" 2>/dev/null | grep -q .; then
        if [ "$module" = "pppoe" ]; then
            echo "ERROR: the running Alpine kernel does not provide the required PPPoE module." >&2
        else
            echo "ERROR: required kernel module '$module' could not be loaded." >&2
        fi
        exit 1
    fi
done < modules/minimalrouter.conf
fi
'''
    if old not in s: raise SystemExit('kernel preflight marker missing')
    s=s.replace(old,new,1)
    start=s.index('echo "[6/7] Loading router kernel modules and sysctls..."')
    end=s.index('\n\n# MinimalRouter owns every WAN/LAN/tunnel interface:', start)
    block=s[start:end]
    s=s[:start]+'echo "[6/7] Preparing kernel/module configuration..."\nif [ "$IMAGE_BUILD" != "1" ]; then\n'+block.split('\n',1)[1]+'\nfi'+s[end:]
    # Generic image must not bake the builder container NIC name into interfaces.
    marker='''install -d -m 0755 -o root -g root /etc/network
{
    echo "# Managed by MinimalRouter. Interfaces are owned by router-applyd,"
    echo "# pppd and wg(8); do not add addresses here."
    echo "auto lo"
    echo "iface lo inet loopback"
    echo ""
    for managed_interface_path in /sys/class/net/*; do
        [ -e "$managed_interface_path" ] || continue
        managed_interface=${managed_interface_path##*/}
        case "$managed_interface" in
            lo|ppp*|wg*|ifb*|veth*|docker*|br-*) continue ;;
        esac
        echo "iface $managed_interface inet manual"
    done
} > /etc/network/interfaces
'''
    replacement='''install -d -m 0755 -o root -g root /etc/network
{
    echo "# Managed by minimalrouter. Physical/tunnel interfaces are configured by router-applyd/pppd/wg."
    echo "auto lo"
    echo "iface lo inet loopback"
    if [ "$IMAGE_BUILD" != "1" ]; then
        echo ""
        for managed_interface_path in /sys/class/net/*; do
            [ -e "$managed_interface_path" ] || continue
            managed_interface=${managed_interface_path##*/}
            case "$managed_interface" in lo|ppp*|wg*|ifb*|veth*|docker*|br-*) continue ;; esac
            echo "iface $managed_interface inet manual"
        done
    fi
} > /etc/network/interfaces
'''
    if marker not in s: raise SystemExit('network marker missing')
    s=s.replace(marker,replacement,1)
    Path(p).write_text(s)

# 2) Rootfs builder. It creates the same installed filesystem once during CI.
Path('packaging/alpine/build-rootfs.sh').write_text(r'''#!/bin/sh
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
''')

# 3) Build ISO also creates and injects rootfs; APK route remains as fallback for now.
p='packaging/alpine/build-iso.sh'; s=Path(p).read_text()
if 'build-rootfs.sh' not in s:
    s=s.replace('cp VERSION "$DIST_DIR/VERSION"\n', 'cp VERSION "$DIST_DIR/VERSION"\nsh packaging/alpine/build-rootfs.sh\nROOTFS_ARCHIVE="$BUILD_DIR/minimalrouter-rootfs-${VERSION}-amd64.tar.gz"\n',1)
    s=s.replace('cp VERSION "$INJECT_DIR/minimalrouter/VERSION"\n', 'cp VERSION "$INJECT_DIR/minimalrouter/VERSION"\ncp "$ROOTFS_ARCHIVE" "$INJECT_DIR/minimalrouter/rootfs.tar.gz"\ncp "$ROOTFS_ARCHIVE.sha256" "$INJECT_DIR/minimalrouter/rootfs.tar.gz.sha256"\n',1)
    Path(p).write_text(s)

# 4) Live installer: replace setup-disk target creation with deterministic rootfs extraction.
p='packaging/alpine/live-installer.sh'; s=Path(p).read_text()
if 'install_appliance_rootfs()' not in s:
    insert=r'''
install_appliance_rootfs() {
    disk="$1"
    rootfs="$ISO_ROOT/rootfs.tar.gz"
    rootfs_sha="$ISO_ROOT/rootfs.tar.gz.sha256"
    [ -s "$rootfs" ] || fail "Appliance rootfs is missing from the ISO"
    [ -r "$rootfs_sha" ] || fail "Appliance rootfs checksum is missing"
    (cd "$ISO_ROOT" && sha256sum -c rootfs.tar.gz.sha256) >/tmp/minimalrouter-rootfs-sha.log 2>&1 || {
        cat /tmp/minimalrouter-rootfs-sha.log >&2 || true
        fail "Appliance rootfs checksum verification failed"
    }

    printf '\nPreparing installation disk...\n'
    wipefs -a "$disk" >/dev/null 2>&1 || true
    # Single ext4 system partition is intentionally simple and robust for the
    # Proxmox/SeaBIOS appliance path. The ISO remains UEFI bootable for recovery;
    # UEFI target-disk installation is added after this path is proven in CI.
    printf 'label: dos\n,;,83,*\n' | sfdisk "$disk" >/tmp/minimalrouter-sfdisk.log 2>&1 || {
        cat /tmp/minimalrouter-sfdisk.log >&2 || true; fail "Could not partition installation disk"; }
    partprobe "$disk" 2>/dev/null || true
    sleep 1
    case "$disk" in /dev/nvme*) rootpart="${disk}p1" ;; *) rootpart="${disk}1" ;; esac
    [ -b "$rootpart" ] || fail "Root partition did not appear: $rootpart"
    mkfs.ext4 -F -L minimalrouter-root "$rootpart" >/tmp/minimalrouter-mkfs.log 2>&1 || {
        cat /tmp/minimalrouter-mkfs.log >&2 || true; fail "Could not create root filesystem"; }
    mkdir -p /mnt
    mount "$rootpart" /mnt || fail "Could not mount target root filesystem"
    tar -xzf "$rootfs" -C /mnt || fail "Could not extract appliance rootfs"

    mkdir -p /mnt/dev /mnt/proc /mnt/sys /mnt/run
    mount --rbind /dev /mnt/dev
    mount --rbind /proc /mnt/proc
    mount --rbind /sys /mnt/sys
    mount --rbind /run /mnt/run
    printf '%s / ext4 defaults,noatime 0 1\n' "$(blkid -s UUID -o value "$rootpart")" > /mnt/etc/fstab
    chroot /mnt ssh-keygen -A >/dev/null 2>&1 || fail "Could not generate SSH host keys"
    chroot /mnt mkinitfs >/tmp/minimalrouter-mkinitfs.log 2>&1 || {
        cat /tmp/minimalrouter-mkinitfs.log >&2 || true; fail "Could not generate initramfs"; }

    mkdir -p /mnt/boot/extlinux
    extlinux --install /mnt/boot/extlinux >/tmp/minimalrouter-extlinux.log 2>&1 || {
        cat /tmp/minimalrouter-extlinux.log >&2 || true; fail "Could not install extlinux"; }
    mbr="$(find /usr/share/syslinux -name mbr.bin 2>/dev/null | head -1)"
    [ -n "$mbr" ] || fail "Syslinux MBR bootstrap is missing"
    dd if="$mbr" of="$disk" bs=440 count=1 conv=notrunc status=none || fail "Could not install boot MBR"
    cat > /mnt/boot/extlinux/extlinux.conf <<'EOF'
DEFAULT minimalrouter
PROMPT 0
TIMEOUT 10
SERIAL 0 115200
LABEL minimalrouter
  LINUX /boot/vmlinuz-lts
  INITRD /boot/initramfs-lts
  APPEND root=LABEL=minimalrouter-root modules=sd-mod,virtio_blk,virtio_pci,ext4 quiet console=tty0 console=ttyS0,115200
EOF
    sync
}

'''
    marker='configure_target_recovery() {'
    s=s.replace(marker,insert+marker,1)
    start=s.index('# Reassert the local ISO repository at the last possible point.')
    end=s.index('\nmount_target_root "$TARGET"', start)
    replacement='''# Install the prebuilt appliance filesystem. No package resolution or setup-disk\n# occurs on the user machine. The exact filesystem installed here was assembled\n# and validated once by CI.\ninstall_appliance_rootfs "$TARGET"\n'''
    s=s[:start]+replacement+s[end:]
    # mount_target_root now sees /mnt already mounted and returns cleanly.
    Path(p).write_text(s)
