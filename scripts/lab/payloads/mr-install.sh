#!/bin/sh
# MR-TEST pristine install: stops services, wipes all application state,
# installs the current dist archive with fault hooks, regenerates lab WG keys,
# strips stale network state, then reboots for a pristine first boot.
# $1 = path to dist tarball. Runs as root inside MR-TEST (Alpine clone).

set -eu

echo "== stopping services =="
for s in routerd router-applyd pppoe-wan dnsmasq inadyn squid wg-quick@wg0 wg-quick@wg1; do
  rc-service "$s" stop >/dev/null 2>&1 || true
done
sleep 2

echo "== pre-wipe evidence =="
cat /etc/alpine-release 2>/dev/null || true
uname -r
cat /etc/network/interfaces 2>/dev/null || echo "(no interfaces file)"

echo "== wiping application state =="
rm -rf /var/lib/minimalrouter /var/lib/minimalrouter-applyd /etc/minimalrouter 2>/dev/null || true
rm -rf /etc/ppp /etc/dnsmasq.d /etc/wireguard 2>/dev/null || true
rm -f /etc/squid/minimalrouter.conf /etc/inadyn.conf /etc/nftables.d/minimalrouter.nft 2>/dev/null || true
rm -f /run/minimalrouter/apply.sock /run/minimalrouter-applyd/* 2>/dev/null || true
pkill -9 -f 'routerd|router-applyd|pppd|dnsmasq' 2>/dev/null || true

echo "== clean network state (lo-only; MR owns eth0/eth1/eth2) =="
rc-service cloud-init stop >/dev/null 2>&1 || true
rc-update del cloud-init default >/dev/null 2>&1 || true
cat > /etc/network/interfaces <<'EOF'
# MR-TEST lab: interfaces are owned by MinimalRouter services.
auto lo
iface lo inet loopback

iface eth0 inet manual
iface eth1 inet manual
iface eth2 inet manual
EOF

echo "== kernel PPPoE capability =="
modprobe pppoe && echo "pppoe module OK"

echo "== install dist =="
rm -rf /tmp/dx && mkdir -p /tmp/dx
tar xzf "$1" -C /tmp/dx
D="/tmp/dx/minimalrouter-linux-amd64"
cd "$D" && sh install.sh --offline

echo "== fault hook env (lab only) =="
mkdir -p /etc/conf.d /run/minimalrouter-fault
chmod 0755 /run/minimalrouter-fault
echo 'MINIMALROUTER_FAULT_HOOK_DIR=/run/minimalrouter-fault' > /etc/conf.d/routerd
echo 'MINIMALROUTER_FAULT_HOOK_DIR=/run/minimalrouter-fault' > /etc/conf.d/router-applyd
# OpenRC does not propagate conf.d variables through supervise-daemon unless
# the init script also exports them (router-applyd already does; add for routerd).
grep -q 'export MINIMALROUTER_FAULT_HOOK_DIR' /etc/init.d/routerd || \
  sed -i '/^export MINIMALROUTER_WEB_DIR=/a export MINIMALROUTER_FAULT_HOOK_DIR="${MINIMALROUTER_FAULT_HOOK_DIR:-}"' /etc/init.d/routerd
# Fault hooks run as the unprivileged routerd user; power-loss scenarios need
# root poweroff through doas.
grep -q 'permit nopass routerd as root cmd /sbin/poweroff' /etc/doas.conf || \
  echo 'permit nopass routerd as root cmd /sbin/poweroff' >> /etc/doas.conf

echo "== lab WireGuard keys =="
mkdir -p /root/lab-wg-keys
[ -f /root/lab-wg-keys/mr_wg0.key ] || { wg genkey > /root/lab-wg-keys/mr_wg0.key; wg pubkey < /root/lab-wg-keys/mr_wg0.key > /root/lab-wg-keys/mr_wg0.pub; }
[ -f /root/lab-wg-keys/mr_wg1.key ] || { wg genkey > /root/lab-wg-keys/mr_wg1.key; wg pubkey < /root/lab-wg-keys/mr_wg1.key > /root/lab-wg-keys/mr_wg1.pub; }
chmod 600 /root/lab-wg-keys/*.key
echo "mr_wg0.pub: $(cat /root/lab-wg-keys/mr_wg0.pub)"
echo "mr_wg1.pub: $(cat /root/lab-wg-keys/mr_wg1.pub)"

echo "== host identity =="
echo "mr-test" > /etc/hostname
hostname mr-test 2>/dev/null || true
echo "mr-test.lab.test" > /etc/hostname
echo "127.0.0.1 mr-test.lab.test mr-test" >> /etc/hosts

echo "== verify =="
ls -la /usr/bin/routerd /usr/sbin/router-applyd /usr/bin/router-recovery /usr/bin/router-update
rc-update show | grep -E 'routerd|router-applyd|pppoe-wan|dnsmasq|squid|inadyn|chronyd' || true

echo "== reboot for pristine first boot =="
reboot >/dev/null 2>&1 &
exit 0
