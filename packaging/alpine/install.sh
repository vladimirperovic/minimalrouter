#!/bin/sh
# Minimal Router OS — Alpine Linux Installer Script
set -e

echo "=== Installing Minimal Router OS on Alpine Linux ==="

# 1. Install system dependencies from Alpine stable repo
apk add --no-cache nftables ppp ppp-pppoe dnsmasq ca-certificates

# 2. Create unprivileged routerd user/group
if ! id -u routerd >/dev/null 2>&1; then
    addgroup -S routerd
    adduser -S -D -H -h /var/lib/minimalrouter -s /sbin/nologin -G routerd routerd
fi

# 3. Create required runtime directories
mkdir -p /var/lib/minimalrouter /run/minimalrouter
chown -R routerd:routerd /var/lib/minimalrouter

# 4. Copy init scripts
cp packaging/alpine/router-applyd.initd /etc/init.d/router-applyd
cp packaging/alpine/routerd.initd /etc/init.d/routerd
chmod +x /etc/init.d/router-applyd /etc/init.d/routerd

# 5. Enable services in OpenRC default runlevel
rc-update add router-applyd default
rc-update add routerd default

echo "=== Installation complete ==="
echo "Start services with: rc-service router-applyd start && rc-service routerd start"
