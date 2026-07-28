#!/bin/sh
# Minimal Router OS — Alpine Linux Installer Script (Pinned to Alpine v3.22 stable)
set -e

ALPINE_VERSION="v3.22"
echo "=== Installing Minimal Router OS on Alpine Linux ($ALPINE_VERSION) ==="

# 1. Pin repositories.
if ! grep -q "$ALPINE_VERSION" /etc/apk/repositories 2>/dev/null; then
    echo "https://dl-cdn.alpinelinux.org/alpine/$ALPINE_VERSION/main" > /etc/apk/repositories
    echo "https://dl-cdn.alpinelinux.org/alpine/$ALPINE_VERSION/community" >> /etc/apk/repositories
fi

# 2. Install system dependencies from the pinned repository.
apk update
apk add --no-cache nftables ppp ppp-pppoe dnsmasq iproute2 ca-certificates \
    wireguard-tools-wg squid hostapd hostapd-openrc iw inadyn inadyn-openrc

# 3. Create unprivileged routerd user/group
if ! id -u routerd >/dev/null 2>&1; then
    addgroup -S routerd
    adduser -S -D -H -h /var/lib/minimalrouter -s /sbin/nologin -G routerd routerd
fi

# 4. Create required runtime directories
install -d -m 0700 -o routerd -g routerd /var/lib/minimalrouter
install -d -m 0700 -o root -g root /var/lib/minimalrouter-applyd
install -d -m 0750 -o root -g routerd /run/minimalrouter
install -d -m 0750 -o root -g inadyn /etc/inadyn
install -d -m 0755 -o root -g root /usr/share/minimalrouter/web /etc/minimalrouter
install -d -m 0700 -o root -g root /etc/ppp/peers /etc/hostapd
install -d -m 0755 -o root -g root /etc/dnsmasq.d /etc/modules-load.d

# Binaries are architecture-specific so a stale artifact from another target
# can never be installed accidentally.
case "$(uname -m)" in
    x86_64) BIN_ARCH="amd64"; BUILD_TARGET="build-linux" ;;
    aarch64) BIN_ARCH="arm64"; BUILD_TARGET="build-linux-arm64" ;;
    *) echo "ERROR: Unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac
[ -f "bin/routerd-linux-${BIN_ARCH}" ] || {
    echo "ERROR: Run make ${BUILD_TARGET} first." >&2
    exit 1
}
install -m 0755 -o root -g root "bin/routerd-linux-${BIN_ARCH}" /usr/bin/routerd
install -m 0755 -o root -g root "bin/router-applyd-linux-${BIN_ARCH}" /usr/sbin/router-applyd
if [ -f web/dist/index.html ]; then
    cp -R web/dist/. /usr/share/minimalrouter/web/
    chown -R root:root /usr/share/minimalrouter/web
else
    echo "ERROR: static appliance dashboard is missing (web/dist/index.html)." >&2
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
