#!/bin/sh
# Minimal Router OS — Proxmox VE Optimization & Helper Script
# Usage: Run on Alpine Linux / Minimal Router OS inside a Proxmox VE VM or LXC container.

set -e

echo "[PROXMOX] Detecting virtualization environment..."

if command -v systemd-detect-virt >/dev/null 2>&1; then
    virtualization_type="$(systemd-detect-virt 2>/dev/null || true)"
    echo "Virtualization: ${virtualization_type:-unknown}"
else
    echo "Virtualization: detection utility unavailable"
fi

echo "[PROXMOX] Installing QEMU Guest Agent & Time Sync (chrony)..."
apk add --no-cache qemu-guest-agent chrony ethtool

echo "[PROXMOX] Enabling QEMU Guest Agent service..."
rc-update add qemu-guest-agent default || true
service qemu-guest-agent start || true

echo "[PROXMOX] Configuring NTP host time synchronization (preventing RTC drift)..."
cat << 'EOF' > /etc/chrony/chrony.conf
# Proxmox VE Chrony Time Synchronization
pool pool.ntp.org iburst
driftfile /var/lib/chrony/drift
makestep 1.0 3
rtcsync
EOF

rc-update add chronyd default || true
service chronyd restart || true

echo "[PROXMOX] Applying VirtIO bridge & kernel sysctl optimizations..."
cat << 'EOF' > /etc/sysctl.d/99-proxmox-virtio.conf
# Proxmox VirtIO bridge compatibility
net.ipv4.conf.all.rp_filter = 1
net.ipv4.conf.default.rp_filter = 1
net.core.rmem_max = 16777216
net.core.wmem_max = 16777216
EOF

sysctl -p /etc/sysctl.d/99-proxmox-virtio.conf || true

echo "[PROXMOX] Proxmox VE guest optimizations applied successfully!"
