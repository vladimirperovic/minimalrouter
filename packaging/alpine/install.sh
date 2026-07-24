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

# 2. Install system dependencies from Alpine pinned repository
apk update
apk add --no-cache nftables ppp ppp-pppoe dnsmasq ca-certificates

# 3. Create unprivileged routerd user/group
if ! id -u routerd >/dev/null 2>&1; then
    addgroup -S routerd
    adduser -S -D -H -h /var/lib/minimalrouter -s /sbin/nologin -G routerd routerd
fi

# 4. Create required runtime directories
mkdir -p /var/lib/minimalrouter /run/minimalrouter
chown -R routerd:routerd /var/lib/minimalrouter

# 5. Copy init scripts
cp packaging/alpine/router-applyd.initd /etc/init.d/router-applyd
cp packaging/alpine/routerd.initd /etc/init.d/routerd
chmod +x /etc/init.d/router-applyd /etc/init.d/routerd

# 6. Enable services in OpenRC default runlevel
rc-update add router-applyd default
rc-update add routerd default

echo "=== Installation complete on Alpine $ALPINE_VERSION ==="
echo "Start services with: rc-service router-applyd start && rc-service routerd start"
