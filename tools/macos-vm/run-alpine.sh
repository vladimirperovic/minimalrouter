#!/bin/sh
set -eu

script_dir="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
repository_dir="$(CDPATH= cd -- "${script_dir}/../.." && pwd)"
version="${1:-3.22.5}"
cache_dir="${2:-/private/tmp/minimalrouter-alpine-${version}}"
runner="${cache_dir}/minimalrouter-alpine-vm"

for required in "${cache_dir}/boot/Image" "${cache_dir}/boot/initramfs-virt" \
    "${cache_dir}/alpine-virt-${version}-aarch64.iso"; do
    if [ ! -f "$required" ]; then
        echo "Missing ${required}; run tools/macos-vm/prepare-alpine.sh first." >&2
        exit 1
    fi
done

xcrun swiftc -parse-as-library -framework Virtualization \
    "$script_dir/MinimalRouterAlpineVM.swift" -o "$runner"
codesign --force --sign - \
    --entitlements "$script_dir/MinimalRouterAlpineVM.entitlements" "$runner"

exec "$runner" \
    "${cache_dir}/boot/Image" \
    "${cache_dir}/boot/initramfs-virt" \
    "${cache_dir}/alpine-virt-${version}-aarch64.iso" \
    "$repository_dir"
