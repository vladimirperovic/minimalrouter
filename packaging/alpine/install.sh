#!/bin/sh
# Minimal Router OS — Alpine Linux Installer Script (Pinned to Alpine v3.22 stable)
set -e

ALPINE_VERSION="v3.22"
echo "=== Installing Minimal Router OS on Alpine Linux ($ALPINE_VERSION) ==="

# 1. Ensure Alpine v3.22 stable repository is pinned
if ! grep -q "$ALPINE_VERSION" /etc/apk/repositories 2>/dev/null; then
    echo "https://dl-cdn.alpinelinux.org/alpine/$ALPINE_VERSION/main" > /etc/apk/repositories
    echo "https://dl-cdn.alpinelinux.org/alpine/$ALPINE_VERSION/community" >> /etc/apk/repositories
fi

# 2. Install system dependencies & LUKS encryption tools from Alpine pinned repository
apk update
apk add --no-cache nftables ppp ppp-pppoe dnsmasq iproute2 ca-certificates \
    cryptsetup e2fsprogs lvm2 wireguard-tools-wg-quick squid

is_backed_by_luks() {
    device_path="$1"
    resolved_path="$(readlink -f "$device_path" 2>/dev/null || true)"
    block_name="$(basename "$resolved_path")"
    [ -n "$block_name" ] || return 1

    dm_uuid_path="/sys/class/block/$block_name/dm/uuid"
    if [ -r "$dm_uuid_path" ]; then
        case "$(cat "$dm_uuid_path")" in
            CRYPT-LUKS*) return 0 ;;
        esac
    fi

    slaves_path="/sys/class/block/$block_name/slaves"
    [ -d "$slaves_path" ] || return 1
    for slave_path in "$slaves_path"/*; do
        [ -e "$slave_path" ] || continue
        if is_backed_by_luks "/dev/$(basename "$slave_path")"; then
            return 0
        fi
    done
    return 1
}

ROOT_SOURCE="$(awk '$2 == "/" { print $1; exit }' /proc/mounts)"
if ! is_backed_by_luks "$ROOT_SOURCE"; then
    if [ "${MINIMALROUTER_ALLOW_UNENCRYPTED:-0}" != "1" ]; then
        echo "ERROR: The root block-device chain has no CRYPT-LUKS layer." >&2
        echo "Install Alpine on LUKS first, or set MINIMALROUTER_ALLOW_UNENCRYPTED=1 for an isolated lab only." >&2
        exit 1
    fi
    echo "WARNING: Installing on unencrypted storage (lab override enabled)." >&2
fi

# 3. Create unprivileged routerd user/group
if ! id -u routerd >/dev/null 2>&1; then
    addgroup -S routerd
    adduser -S -D -H -h /var/lib/minimalrouter -s /sbin/nologin -G routerd routerd
fi

# 4. Create required runtime directories
install -d -m 0700 -o routerd -g routerd /var/lib/minimalrouter
install -d -m 0700 -o root -g root /var/lib/minimalrouter-applyd
install -d -m 0750 -o root -g routerd /run/minimalrouter
install -d -m 0755 -o root -g root /usr/share/minimalrouter/web /etc/minimalrouter
install -d -m 0700 -o root -g root /etc/ppp/peers /etc/wireguard /etc/hostapd
install -d -m 0755 -o root -g root /etc/dnsmasq.d /etc/modules-load.d

# Binaries must have been built with `make build-linux`.
install -m 0755 -o root -g root bin/routerd /usr/bin/routerd
install -m 0755 -o root -g root bin/router-applyd /usr/sbin/router-applyd
if [ -f web/dist/client/index.html ]; then
    cp -R web/dist/client/. /usr/share/minimalrouter/web/
    chown -R root:root /usr/share/minimalrouter/web
else
    echo "ERROR: static appliance dashboard is missing (web/dist/client/index.html)." >&2
    exit 1
fi

# 5. Copy init scripts
cp packaging/alpine/router-applyd.initd /etc/init.d/router-applyd
cp packaging/alpine/routerd.initd /etc/init.d/routerd
cp packaging/alpine/pppoe-wan.initd /etc/init.d/pppoe-wan
cp packaging/alpine/99-minimalrouter.conf /etc/sysctl.d/99-minimalrouter.conf
cp packaging/alpine/minimalrouter.modules /etc/modules-load.d/minimalrouter.conf
chmod 0755 /etc/init.d/router-applyd /etc/init.d/routerd /etc/init.d/pppoe-wan
chmod 0644 /etc/sysctl.d/99-minimalrouter.conf /etc/modules-load.d/minimalrouter.conf

# Alpine's OpenRC modules service reads /etc/modules. Keep this fallback for
# installed systems that do not process modules-load.d directly.
while IFS= read -r kernel_module; do
    case "$kernel_module" in
        ""|\#*) continue ;;
    esac
    grep -qxF "$kernel_module" /etc/modules 2>/dev/null || printf '%s\n' "$kernel_module" >> /etc/modules
done < packaging/alpine/minimalrouter.modules

# 6. Enable services in OpenRC default runlevel
for unused_service in sshd dropbear telnetd httpd miniupnpd upnpd rpcbind; do
    rc-service "$unused_service" stop >/dev/null 2>&1 || true
    rc-update del "$unused_service" default >/dev/null 2>&1 || true
done
rc-update add router-applyd default
rc-update add routerd default

echo "=== Installation complete on Alpine $ALPINE_VERSION ==="
echo "Start services with: rc-service router-applyd start && rc-service routerd start"
