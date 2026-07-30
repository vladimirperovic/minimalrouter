#!/bin/sh
# Minimal Router OS — Self-contained dist installer
# Runs from extracted tarball (no source repo needed)
# Usage: tar xzf minimalrouter-linux-amd64.tar.gz && cd minimalrouter-linux-amd64 && sudo sh install.sh
set -eu

if [ "$(id -u)" -ne 0 ]; then
    echo "ERROR: install.sh must run as root" >&2
    exit 1
fi
if [ ! -f /etc/alpine-release ] || ! command -v apk >/dev/null 2>&1; then
    echo "ERROR: this distribution installer supports Alpine Linux only" >&2
    exit 1
fi

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

# Verify dist contents before changing the host.
for required in \
    "bin/routerd-${BIN_ARCH}" \
    "bin/router-applyd-${BIN_ARCH}" \
    "web/dist/index.html" \
    "init.d/routerd" \
    "init.d/router-applyd" \
    "init.d/pppoe-wan" \
    "sysctl/99-minimalrouter.conf" \
    "modules/minimalrouter.conf"
do
    [ -f "$required" ] || {
        echo "ERROR: Missing distribution file: $required" >&2
        exit 1
    }
done

ALPINE_VERSION="v3.22"

# 1. Pin Alpine repositories.
if ! grep -q "$ALPINE_VERSION" /etc/apk/repositories 2>/dev/null; then
    echo "https://dl-cdn.alpinelinux.org/alpine/$ALPINE_VERSION/main" > /etc/apk/repositories
    echo "https://dl-cdn.alpinelinux.org/alpine/$ALPINE_VERSION/community" >> /etc/apk/repositories
fi

# 2. System dependencies
echo "[1/7] Installing dependencies..."
apk update
apk add --no-cache nftables ppp ppp-pppoe dnsmasq iproute2 ca-certificates \
    wireguard-tools-wg squid hostapd hostapd-openrc iw inadyn inadyn-openrc tzdata chrony

# 3. Routerd user
echo "[2/7] Creating user..."
if ! id -u routerd >/dev/null 2>&1; then
    addgroup -S routerd
    adduser -S -D -H -h /var/lib/minimalrouter -s /sbin/nologin -G routerd routerd
fi

# 4. Directories + binaries
echo "[3/7] Installing binaries..."
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
echo "[4/7] Installing dashboard..."
rm -rf /usr/share/minimalrouter/web/*
cp -R web/dist/. /usr/share/minimalrouter/web/
chown -R root:root /usr/share/minimalrouter/web

# 6. Init scripts and kernel policy
echo "[5/7] Installing service and kernel configuration..."
cp init.d/routerd /etc/init.d/routerd
cp init.d/router-applyd /etc/init.d/router-applyd
cp init.d/pppoe-wan /etc/init.d/pppoe-wan
cp sysctl/99-minimalrouter.conf /etc/sysctl.d/99-minimalrouter.conf
cp modules/minimalrouter.conf /etc/modules-load.d/minimalrouter.conf
chmod 0755 /etc/init.d/router-applyd /etc/init.d/routerd /etc/init.d/pppoe-wan
chmod 0644 /etc/sysctl.d/99-minimalrouter.conf /etc/modules-load.d/minimalrouter.conf

# Persist and load every required module now so an immediate service start has
# the same kernel capabilities as the next boot.
echo "[6/7] Loading router kernel modules and sysctls..."
while IFS= read -r module; do
    case "$module" in ""|\#*) continue ;; esac
    grep -qxF "$module" /etc/modules 2>/dev/null || printf '%s\n' "$module" >> /etc/modules
    modprobe "$module"
done < modules/minimalrouter.conf

sysctl -p /etc/sysctl.d/99-minimalrouter.conf >/dev/null
[ "$(sysctl -n net.ipv4.ip_forward)" = "1" ] || {
    echo "ERROR: IPv4 forwarding did not activate" >&2
    exit 1
}

# 7. Services
echo "[7/7] Enabling services..."
for svc in sshd dropbear telnetd httpd miniupnpd upnpd rpcbind; do
    rc-service "$svc" stop >/dev/null 2>&1 || true
    rc-update del "$svc" default >/dev/null 2>&1 || true
done
rc-update add chronyd default
rc-update add router-applyd default
rc-update add routerd default

echo "=== Installation complete ==="
echo "Start now: rc-service router-applyd start && rc-service routerd start"
echo "Or reboot once; both services are enabled for the default runlevel."
LAN_IP="$(ip -4 addr show 2>/dev/null | grep -o 'inet [0-9.]*' | grep -v '127.0.0.1' | head -1 | cut -d' ' -f2)"
[ -n "$LAN_IP" ] && echo "Current management candidate: https://${LAN_IP}:8443"
echo "Default first-run management address after routerd reconciliation: https://192.168.1.1:8443"
