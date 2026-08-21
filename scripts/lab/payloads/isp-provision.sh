#!/bin/sh
# ISP-LAB provisioning: PPPoE access concentrator + DNS + NAT + fault tool.
# Payload runs as root inside ISP-LAB (Debian 13). Idempotent.
# $1 = labadmin public key (optional)

set -eu
export DEBIAN_FRONTEND=noninteractive

echo "== packages =="
apt-get update -qq
apt-get install -y -qq pppoe dnsmasq nftables iproute2 qemu-guest-agent curl openssl kmod >/dev/null 2>&1 || \
  apt-get install -y -qq pppoe dnsmasq nftables iproute2 qemu-guest-agent curl kmod >/dev/null
systemctl enable --now qemu-guest-agent >/dev/null 2>&1 || true

echo "== interface detection =="
IFACE=$(ip -4 -o addr show | awk '$4 ~ /^10\.250\.0\.1\// {sub(/:.*/,"",$2); print $2}' | head -1)
[ -n "$IFACE" ] || IFACE="$(ip -o link show | awk -F': ' '$2 ~ /^ens/ {print $2; exit}')"
echo "$IFACE" > /etc/lab-iface
echo "lab-wan iface: $IFACE"

echo "== PPP runtime =="
# The disposable Debian ISP runs pppd for every PPPoE session. Ensure the
# kernel endpoint exists before starting rp-pppoe; otherwise the daemon can
# appear healthy while every incoming session fails with a missing /dev/ppp.
for module in ppp_generic pppox pppoe; do
  modprobe "$module" 2>/dev/null || true
done
if [ ! -d /sys/module/ppp_generic ]; then
  echo "ERROR: ISP-LAB kernel did not load ppp_generic" >&2
  uname -a >&2 || true
  ls -la /lib/modules 2>&1 >&2 || true
  exit 1
fi
if [ ! -c /dev/ppp ]; then
  rm -f /dev/ppp
  mknod -m 0600 /dev/ppp c 108 0 2>/dev/null || true
fi
[ -c /dev/ppp ] || {
  echo "ERROR: ISP-LAB PPP runtime is missing /dev/ppp" >&2
  lsmod 2>/dev/null || true
  exit 1
}

echo "== pppoe-server =="
cat > /etc/ppp/pppoe-server-options <<'EOF'
require-chap
lcp-echo-interval 3
lcp-echo-failure 4
logfile /var/log/pppoe-server.log
mtu 1492
mru 1492
EOF
# Debian's ppp package creates /etc/ppp/chap-secrets during installation, so
# testing only for file existence silently leaves the lab with no mr-test
# credential. Seed the lab credential unconditionally and make its permissions
# explicit; this file belongs solely to the disposable ISP VM.
cat > /etc/ppp/chap-secrets <<'EOF'
# client  server  secret                  fixed-ip
mr-test   *       minimalrouter-lab-pppoe 10.250.0.50
EOF
chmod 0600 /etc/ppp/chap-secrets
grep -Eq '^mr-test[[:space:]]+\*[[:space:]]+minimalrouter-lab-pppoe[[:space:]]+10\.250\.0\.50([[:space:]]|$)' /etc/ppp/chap-secrets || {
  echo "ERROR: ISP-LAB CHAP credential was not seeded" >&2
  exit 1
}
cat > /etc/systemd/system/pppoe-server.service <<EOF
[Unit]
Description=Lab PPPoE access concentrator (rp-pppoe)
After=network-online.target
[Service]
# rp-pppoe daemonizes by default; -F keeps the process attached so systemd owns
# the real server instead of repeatedly restarting after the parent exits.
# Keep address allocation in pppoe-server itself; pppd's options file is only
# for PPP options and must not contain pseudo-options such as local-ip/remote-ip.
ExecStart=/usr/sbin/pppoe-server -F -I $(cat /etc/lab-iface) -L 10.250.0.1 -R 10.250.0.50 -N 1 -T 60 -C lab-isp -S lab-isp
Restart=always
RestartSec=2
[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload
systemctl enable pppoe-server >/dev/null 2>&1
systemctl restart pppoe-server
systemctl is-active --quiet pppoe-server

echo "== dnsmasq =="
cat > /etc/dnsmasq.d/lab.conf <<EOF
port=53
bind-interfaces
listen-address=10.250.0.1,127.0.0.1
interface=$IFACE
local=/lab.test/
address=/router.lab.test/10.77.0.1
address=/isp.lab.test/10.250.0.1
address=/sim.lab.test/10.250.0.10
no-resolv
EOF
cat > /etc/dnsmasq.d/lab-mode.conf <<EOF
server=192.168.1.4
server=1.1.1.1
EOF
systemctl enable dnsmasq >/dev/null 2>&1
systemctl restart dnsmasq

echo "== routing + NAT + modes =="
sysctl -w net.ipv4.ip_forward=1 >/dev/null
[ -f /etc/sysctl.conf ] || : > /etc/sysctl.conf
grep -q 'net.ipv4.ip_forward=1' /etc/sysctl.conf || echo 'net.ipv4.ip_forward=1' >> /etc/sysctl.conf
mkdir -p /etc/nftables.d
echo real > /etc/lab-mode
cat > /usr/local/sbin/lab-nat <<'EOF'
#!/bin/sh
# Lab NAT/mode policy. mode real: ppp clients NAT to upstream (prod LAN).
# mode sim: no upstream egress; only the lab segment (10.250.0.0/24) is reachable.
IFACE=$(cat /etc/lab-iface)
MODE=$(cat /etc/lab-mode)
# Shell redirections are not valid nft batch syntax. Delete old tables from the
# shell first, then feed only nft grammar to nft -f -.
nft delete table inet labnat 2>/dev/null || true
nft -f - <<NATEOF
table inet labnat {
  chain postrouting {
    type nat hook postrouting priority srcnat; policy accept;
    oifname "eth0" ip saddr 10.250.0.0/24 masquerade
  }
}
NATEOF
nft delete table inet labfw 2>/dev/null || true
nft -f - <<FORWEOF
table inet labfw {
  chain blackhole {
  }
  chain forward {
    type filter hook forward priority filter; policy accept;
    jump blackhole
    iifname "ppp+" oifname "$IFACE" accept
    iifname "$IFACE" oifname "ppp+" ct state established,related accept
  }
}
FORWEOF
if [ "$MODE" = sim ]; then
  nft -f - <<SIMEOF
table inet labfw {
  chain forward {
    policy drop;
    jump blackhole
    iifname "ppp+" oifname "$IFACE" accept
    iifname "$IFACE" oifname "ppp+" ct state established,related accept
    oifname "eth0" ip saddr 10.250.0.0/24 drop
  }
}
SIMEOF
fi
EOF
chmod 755 /usr/local/sbin/lab-nat
cat > /etc/systemd/system/lab-net.service <<'EOF'
[Unit]
Description=Lab NAT/mode policy
After=network-online.target
Before=pppoe-server.service
[Service]
Type=oneshot
ExecStart=/usr/local/sbin/lab-nat
[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload
systemctl enable lab-net >/dev/null 2>&1
/usr/local/sbin/lab-nat

echo "== fault injection tool =="
cat > /usr/local/sbin/lab-fault <<'FAULTEOF'
#!/bin/sh
# Lab fault-injection CLI (ISP-LAB side). See docs/TEST-LAB.md.
IFACE=$(cat /etc/lab-iface 2>/dev/null || echo ens18)
tc_nic="$IFACE"; tc_ppp="ppp0"
qdisc() {  # qdisc <iface> <on|off> <args...>
  iface="$1"; op="$2"; shift 2
  if [ "$op" = off ]; then
    tc qdisc del dev "$iface" root 2>/dev/null || true
    return
  fi
  tc qdisc add dev "$iface" root handle 1: netem "$@" 2>/dev/null || \
    tc qdisc change dev "$iface" root handle 1: netem "$@" 2>/dev/null || true
}
mode() {
  echo "$1" > /etc/lab-mode
  /usr/local/sbin/lab-nat
  if [ "$1" = sim ]; then
    cat > /etc/dnsmasq.d/lab-mode.conf <<'EOF'
address=/#/10.250.0.10
EOF
  else
    cat > /etc/dnsmasq.d/lab-mode.conf <<'EOF'
server=192.168.1.4
server=1.1.1.1
EOF
  fi
  systemctl restart dnsmasq
  echo "mode=$1"
}
pppoe() { systemctl "$1" pppoe-server; }
auth() {  # good|bad
  if [ "$1" = good ]; then
    cat > /etc/ppp/chap-secrets <<EOF
mr-test   *       minimalrouter-lab-pppoe 10.250.0.50
EOF
  else
    cat > /etc/ppp/chap-secrets <<EOF
mr-test   *       wrong-password-123      10.250.0.50
EOF
  fi
  chmod 0600 /etc/ppp/chap-secrets
  echo "auth=$1"
}
carrier() { ip link set "$IFACE" "$1"; echo "carrier=$1"; }
loss() {  # 0|1|5|20|100
  if [ "$1" = 0 ]; then qdisc "$tc_nic" off; qdisc "$tc_ppp" off
  else qdisc "$tc_nic" on loss "$1"; qdisc "$tc_ppp" on loss "$1"; fi
  echo "loss=$1%"
}
latency() {  # <ms> <jitter_ms> [loss%]
  qdisc "$tc_nic" on delay "$1"ms "$2"ms distribution normal ${3:+loss "$3"}
  qdisc "$tc_ppp" on delay "$1"ms "$2"ms distribution normal ${3:+loss "$3"}
  echo "latency=$1ms jitter=$2ms loss=${3:-0}%"
}
reorder() {  # 0|25|50  (percentage, requires base delay)
  if [ "$1" = 0 ]; then qdisc "$tc_nic" off; qdisc "$tc_ppp" off
  else
    qdisc "$tc_nic" on delay 20ms reorder "$1" gap 3
    qdisc "$tc_ppp" on delay 20ms reorder "$1" gap 3
  fi
  echo "reorder=$1%"
}
rate() {  # mbps
  qdisc "$tc_nic" on rate "${1}mbit"
  qdisc "$tc_ppp" on rate "${1}mbit"
  echo "rate=${1}mbit"
}
mtu() {  # 1400|1492|1500
  sed -i "s/^mtu .*/mtu $1/;s/^mru .*/mru $1/" /etc/ppp/pppoe-server-options
  ip link set "$IFACE" mtu "$1"
  systemctl restart pppoe-server
  echo "mtu=$1 (server restarted)"
}
dns() {  # on|out
  case "$1" in
    on)
      nft flush chain inet labfw blackhole 2>/dev/null || true
      systemctl start dnsmasq
      ;;
    out)
      systemctl stop dnsmasq
      nft flush chain inet labfw blackhole 2>/dev/null || true
      nft add rule inet labfw blackhole iifname ppp0 udp dport 53 drop
      nft add rule inet labfw blackhole iifname ppp0 tcp dport 53 drop
      ;;
  esac
  echo "dns=$1"
}
blackhole() {  # on [dest[:port]] | off
  nft flush chain inet labfw blackhole 2>/dev/null || true
  if [ "$1" = on ]; then
    if [ -n "${2:-}" ]; then
      case "$2" in
        *:*) dst="${2%:*}"; port="${2##*:}"
             nft add rule inet labfw blackhole iifname ppp0 ip daddr "$dst" tcp dport "$port" drop
             nft add rule inet labfw blackhole iifname ppp0 ip daddr "$dst" udp dport "$port" drop ;;
        *)   nft add rule inet labfw blackhole iifname ppp0 ip daddr "$2" drop ;;
      esac
    else
      nft add rule inet labfw blackhole iifname ppp0 drop
    fi
  fi
  echo "blackhole=$1${2:+:$2}"
}
outage() {  # short|long|stop — composite ISP outage (pppoe stop + restart)
  case "$1" in
    short) systemctl stop pppoe-server; sleep 30; systemctl start pppoe-server; echo "outage=short(30s)";;
    long)  systemctl stop pppoe-server; echo "outage=long(started)";;
    stop)  systemctl start pppoe-server; echo "outage=long(ended)";;
    *) echo "usage: outage short|long|stop";;
  esac
}
reset() {
  qdisc "$tc_nic" off; qdisc "$tc_ppp" off
  nft flush chain inet labfw blackhole 2>/dev/null || true
  ip link set "$IFACE" mtu 1500
  sed -i 's/^mtu .*/mtu 1492/;s/^mru .*/mru 1492/' /etc/ppp/pppoe-server-options
  auth good
  systemctl start dnsmasq >/dev/null 2>&1 || true
  systemctl start pppoe-server >/dev/null 2>&1 || true
  /usr/local/sbin/lab-nat
  echo "reset done"
}
status() {
  echo "mode=$(cat /etc/lab-mode)"
  echo "iface=$IFACE carrier=$(cat /sys/class/net/$IFACE/carrier 2>/dev/null || echo down)"
  echo "pppoe=$(systemctl is-active pppoe-server 2>/dev/null || echo inactive)"
  echo "dns=$(systemctl is-active dnsmasq 2>/dev/null || echo inactive)"
  if grep -Eq '^mr-test[[:space:]]+\*[[:space:]]+minimalrouter-lab-pppoe[[:space:]]+10\.250\.0\.50([[:space:]]|$)' /etc/ppp/chap-secrets; then
    echo "auth=good"
  elif grep -Eq '^mr-test[[:space:]]+\*[[:space:]]+wrong-password-123([[:space:]]|$)' /etc/ppp/chap-secrets; then
    echo "auth=bad"
  else
    echo "auth=missing"
  fi
  echo "mtu=$(grep '^mtu' /etc/ppp/pppoe-server-options | awk '{print $2}')"
  for i in "$tc_nic" "$tc_ppp"; do
    s=$(tc qdisc show dev "$i" 2>/dev/null | grep -o 'netem [^ ]*' | head -1 || true)
    [ -n "$s" ] && echo "netem[$i]=$s"
  done
  echo "blackhole_rules=$(nft list chain inet labfw blackhole 2>/dev/null | grep -c drop || echo 0)"
  echo "sessions=$(ls /var/run/ppp-* 2>/dev/null | wc -l) / ppp active: $(ip -o link show ppp0 2>/dev/null | wc -l)"
}
cmd="$1"; shift 2>/dev/null || true
case "$cmd" in
  mode|pppoe|auth|carrier|loss|latency|reorder|rate|mtu|dns|blackhole|outage|reset|status) "$cmd" "$@";;
  *) echo "usage: lab-fault <mode|pppoe|auth|carrier|loss|latency|reorder|rate|mtu|dns|blackhole|outage|reset|status>"; exit 1;;
esac
FAULTEOF
chmod 755 /usr/local/sbin/lab-fault

echo "== ISP-LAB ready =="
status_output="$(/usr/local/sbin/lab-fault status)"
printf '%s\n' "$status_output"
printf '%s\n' "$status_output" | grep -qx 'auth=good' || {
  echo "ERROR: ISP-LAB authentication state is not good" >&2
  exit 1
}
