#!/bin/sh
# LAN-CLIENT provisioning: verification tooling. Runs as root inside
# LAN-CLIENT (Debian 13). Idempotent.

set -eu
export DEBIAN_FRONTEND=noninteractive

echo "== packages =="
apt-get update -qq
apt-get install -y -qq qemu-guest-agent curl dnsutils iputils-ping netcat-openbsd >/dev/null
systemctl enable --now qemu-guest-agent >/dev/null 2>&1 || true

echo "== lease probe (wait for MR-TEST DHCP) =="
i=0
while [ $i -lt 30 ]; do
  if ip -4 -o addr show | grep -q "10.77.0."; then
    echo "DHCP lease acquired: $(ip -4 -o addr show | grep '10.77.0.' | awk '{print $4}')"
    break
  fi
  sleep 5; i=$((i+1))
done
ip -4 route
echo "== LAN-CLIENT ready =="
