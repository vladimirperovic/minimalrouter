#!/bin/sh
set -eu

version="${1:-3.22.5}"
cache_dir="${2:-/private/tmp/minimalrouter-alpine-${version}}"
base_url="https://dl-cdn.alpinelinux.org/alpine/v3.22/releases/aarch64"
iso_name="alpine-virt-${version}-aarch64.iso"
iso_path="${cache_dir}/${iso_name}"
boot_dir="${cache_dir}/boot"

mkdir -p "$cache_dir" "$boot_dir"

curl --fail --location --proto '=https' --tlsv1.2 \
    --output "$iso_path" "${base_url}/${iso_name}"
curl --fail --location --proto '=https' --tlsv1.2 \
    --output "${iso_path}.sha256" "${base_url}/${iso_name}.sha256"
(
    cd "$cache_dir"
    shasum -a 256 -c "${iso_name}.sha256"
)

bsdtar -xf "$iso_path" -C "$cache_dir" boot/vmlinuz-virt boot/initramfs-virt
vmlinuz="${cache_dir}/boot/vmlinuz-virt"
gzip_magic="$(printf '\037\213\010')"
gzip_offset="$(LC_ALL=C grep -aob "$gzip_magic" "$vmlinuz" | sed -n '1s/:.*//p')"
if [ -z "$gzip_offset" ]; then
    echo "Could not locate the compressed Linux Image in ${vmlinuz}" >&2
    exit 1
fi

dd if="$vmlinuz" bs=1 skip="$gzip_offset" 2>/dev/null |
    gzip -dc > "${boot_dir}/Image"

echo "Prepared verified Alpine ${version} ARM64 VM assets:"
echo "  kernel:    ${boot_dir}/Image"
echo "  initramfs: ${boot_dir}/initramfs-virt"
echo "  ISO:       ${iso_path}"
