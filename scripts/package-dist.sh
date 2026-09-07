#!/bin/sh
# Package already-built binaries/assets. Signing remains a separate release step.
set -eu
# BSD tar must not inject unsigned AppleDouble metadata into a signed inventory.
export COPYFILE_DISABLE=1
[ "$#" -eq 2 ] || { echo 'usage: package-dist.sh amd64|arm64 version' >&2; exit 2; }
arch=$1
case "$arch" in amd64|arm64) ;; *) echo 'unsupported distribution architecture' >&2; exit 2 ;; esac
version=${2#v}
[ -n "$version" ] || { echo 'distribution version is required' >&2; exit 2; }
dist="build/dist/minimalrouter-linux-$arch"
archive="build/minimalrouter-linux-$arch.tar.gz"
# Refuse an incomplete build before replacing an existing distribution.
for command in routerd router-applyd router-recovery router-update router-setup; do
    [ -f "bin/$command-linux-$arch" ] || { echo "missing built $command for $arch" >&2; exit 1; }
done
[ -f web/dist/index.html ] || { echo 'dashboard build is missing' >&2; exit 1; }
rm -rf "$dist"
mkdir -p "$dist/bin" "$dist/web/dist" "$dist/init.d" "$dist/sysctl" "$dist/modules" "$dist/logrotate"
printf '%s\n' "$version" > "$dist/VERSION"
for command in routerd router-applyd router-recovery router-update router-setup; do
    cp "bin/$command-linux-$arch" "$dist/bin/$command-$arch"
done
chmod 0755 "$dist/bin/router-recovery-$arch"
chmod 0750 "$dist/bin/router-update-$arch"
sh scripts/fetch-cloudflared.sh "$arch" "$dist/bin/cloudflared-$arch"
cp -R web/dist/. "$dist/web/dist/"
cp packaging/alpine/slot-exec "$dist/slot-exec"
cp packaging/alpine/firstboot-ready "$dist/firstboot-ready"
cp packaging/alpine/firstboot.sh "$dist/firstboot"
cp packaging/alpine/firstboot.initd "$dist/init.d/minimalrouter-firstboot"
chmod 0755 "$dist/firstboot-ready" "$dist/firstboot" "$dist/init.d/minimalrouter-firstboot"
cp packaging/alpine/compatibility.json "$dist/compatibility.json"
for service in routerd router-applyd pppoe-wan cloudflared; do
    cp "packaging/alpine/$service.initd" "$dist/init.d/$service"
done
cp packaging/alpine/99-minimalrouter.conf "$dist/sysctl/99-minimalrouter.conf"
cp packaging/alpine/minimalrouter.modules "$dist/modules/minimalrouter.conf"
cp packaging/alpine/minimalrouter.logrotate "$dist/logrotate/minimalrouter"
cp packaging/alpine/ip-up.d-minimalrouter-qos "$dist/ip-up.d-minimalrouter-qos"
cp packaging/alpine/install-console.sh "$dist/install.sh"
cp packaging/alpine/install-dist.sh "$dist/install-core.sh"
chmod +x "$dist/install.sh" "$dist/install-core.sh" "$dist/slot-exec" "$dist/init.d/routerd" \
    "$dist/init.d/router-applyd" "$dist/init.d/pppoe-wan" "$dist/init.d/cloudflared" "$dist/ip-up.d-minimalrouter-qos"
tar czf "$archive" -C build/dist "minimalrouter-linux-$arch"
sh scripts/checksum-file.sh "$archive" "$archive.sha256"
echo "=== Distribution: $archive ==="
ls -lh "$archive" "$archive.sha256"
