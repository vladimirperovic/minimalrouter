#!/bin/sh
# Minimal Router OS — Self-contained dist installer
# Runs from extracted tarball (no source repo needed)
# Usage: tar xzf minimalrouter-linux-arm64.tar.gz && cd minimalrouter-linux-arm64 && sudo sh install.sh
set -e

echo "=== Minimal Router OS Distribution Installer ==="
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

# Detect architecture
ARCH="$(uname -m)"
case "$ARCH" in
    x86_64)  BIN_ARCH="amd64" ;;
    aarch64) BIN_ARCH="arm64" ;;
    *)       echo "ERROR: Unsupported architecture: $ARCH" >&2; exit 1 ;;
esac
echo "Architecture: $ARCH ($BIN_ARCH)"

# Verify dist contents
[ -f "bin/routerd-${BIN_ARCH}" ] || { echo "ERROR: Missing bin/routerd-${BIN_ARCH}" >&2; exit 1; }
[ -f "bin/router-applyd-${BIN_ARCH}" ] || { echo "ERROR: Missing bin/router-applyd-${BIN_ARCH}" >&2; exit 1; }
[ -f "web/dist/index.html" ] || { echo "ERROR: Missing web/dist/index.html" >&2; exit 1; }
[ -f "init.d/routerd" ] || { echo "ERROR: Missing init.d/routerd" >&2; exit 1; }

ALPINE_VERSION="v3.22"

# 1. Pin Alpine repositories.
if ! grep -q "$ALPINE_VERSION" /etc/apk/repositories 2>/dev/null; then
    echo "https://dl-cdn.alpinelinux.org/alpine/$ALPINE_VERSION/main" > /etc/apk/repositories
    echo "https://dl-cdn.alpinelinux.org/alpine/$ALPINE_VERSION/community" >> /etc/apk/repositories
fi

# 2. System dependencies
echo "[1/6] Installing dependencies..."
apk update
apk add --no-cache nftables ppp ppp-pppoe dnsmasq iproute2 ca-certificates \
    wireguard-tools-wg squid hostapd hostapd-openrc iw inadyn inadyn-openrc

# 3. Routerd user
echo "[2/6] Creating user..."
if ! id -u routerd >/dev/null 2>&1; then
    addgroup -S routerd
    adduser -S -D -H -h /var/lib/minimalrouter -s /sbin/nologin -G routerd routerd
fi

# 4. Directories + binaries
echo "[3/6] Installing binaries..."
install -d -m 0700 -o routerd -g routerd /var/lib/minimalrouter
install -d -m 0700 -o root -g root /var/lib/minimalrouter-applyd
install -d -m 0750 -o root -g routerd /run/minimalrouter
install -d -m 0750 -o root -g inadyn /etc/inadyn
install -d -m 0755 -o root -g root /usr/share/minimalrouter/web /etc/minimalrouter
install -d -m 0700 -o root -g root /etc/ppp/peers /etc/hostapd
install -d -m 0755 -o root -g root /etc/dnsmasq.d /etc/modules-load.d

install -m 0755 "bin/routerd-${BIN_ARCH}" /usr/bin/routerd
install -m 0755 "bin/router-applyd-${BIN_ARCH}" /usr/sbin/router-applyd

# 5. Web dashboard
echo "[4/6] Installing dashboard..."
cp -R web/dist/. /usr/share/minimalrouter/web/
chown -R root:root /usr/share/minimalrouter/web

# 6. Init scripts
echo "[5/6] Installing init scripts..."
cp init.d/routerd /etc/init.d/routerd
cp init.d/router-applyd /etc/init.d/router-applyd
cp init.d/pppoe-wan /etc/init.d/pppoe-wan
cp sysctl/99-minimalrouter.conf /etc/sysctl.d/99-minimalrouter.conf
cp modules/minimalrouter.conf /etc/modules-load.d/minimalrouter.conf
chmod 0755 /etc/init.d/router-applyd /etc/init.d/routerd /etc/init.d/pppoe-wan
chmod 0644 /etc/sysctl.d/99-minimalrouter.conf /etc/modules-load.d/minimalrouter.conf

# Load modules
while IFS= read -r m; do
    case "$m" in ""|\#*) continue ;; esac
    grep -qxF "$m" /etc/modules 2>/dev/null || printf '%s\n' "$m" >> /etc/modules
done < modules/minimalrouter.conf

# 7. Services
echo "[6/6] Enabling services..."
for svc in sshd dropbear telnetd httpd miniupnpd upnpd rpcbind; do
    rc-service "$svc" stop >/dev/null 2>&1 || true
    rc-update del "$svc" default >/dev/null 2>&1 || true
done
rc-update add router-applyd default
rc-update add routerd default

echo "=== Installation complete ==="
echo "Start:  rc-service router-applyd start && rc-service routerd start"
LAN_IP="$(ip -4 addr show 2>/dev/null | grep -o 'inet [0-9.]*' | grep -v '127.0.0.1' | head -1 | cut -d' ' -f2)"
[ -n "$LAN_IP" ] && echo "Dashboard: https://${LAN_IP}:8443"
