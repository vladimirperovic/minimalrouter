#!/bin/sh
# MR-TEST full lab profile configuration.
# Runs on the mac; drives the Proxmox host (curl via host to MR-TEST API) and
# guest exec into VMs. Requires: isp, sim, mr deploy steps done.
# Configures: PPPoE WAN (wizard), LAN/DHCP/DNS, ExtraLAN, wg0 peer, wg1 office,
# then pushes MR public keys into SIM-LAB wg peers.

set -eu

HOST="${LAB_HOST:-root@192.168.1.2}"
SSHOPTS="-o BatchMode=yes -o ConnectTimeout=10"
KEY="${LAB_SSH_KEY:-$(dirname "$0")/../../private/secrets/proxmox_codex_ed25519}"
KNOWN_HOSTS="${LAB_KNOWN_HOSTS:-$(dirname "$0")/../../private/secrets/proxmox_known_hosts}"
LABDIR="$(cd "$(dirname "$0")" && pwd)"
SEC="$(cd "$LABDIR/../../private/secrets" && pwd)"
H() { ssh $SSHOPTS -i "$KEY" -o UserKnownHostsFile="$KNOWN_HOSTS" "$HOST" "$@"; }
gx() {
  H "qm guest exec $1 -- sh -c \"$2\"" | python3 -c '
import json,sys,base64
try:
    d=json.load(sys.stdin).get("out-data","")
    sys.stdout.write(base64.b64decode(d).decode("utf-8","replace"))
except Exception: pass'
}

MR_API="https://10.77.0.1:8443"
PPPOE_PW="minimalrouter-lab-pppoe"
ADMIN_PW="${LAB_ADMIN_PW:-MinimalRouter-Lab-Test!2026}"

echo "== wait for MR-TEST pristine boot =="
i=0
while [ $i -lt 40 ]; do
  st="$(H "curl -sk --max-time 5 $MR_API/api/v1/setup/status 2>/dev/null" || true)"
  if echo "$st" | grep -q '"is_configured": false'; then
    echo "  pristine first boot confirmed"
    break
  fi
  sleep 8; i=$((i+1))
done
[ $i -lt 40 ] || { echo "ERROR: MR-TEST did not reach pristine first boot"; exit 1; }

echo "== pull MR + SIM wireguard keys =="
MR_WG0_KEY="$(gx 151 'cat /root/lab-wg-keys/mr_wg0.key')"
MR_WG1_KEY="$(gx 151 'cat /root/lab-wg-keys/mr_wg1.key')"
SIM_WG0_PUB="$(gx 153 'cat /root/lab-wg-keys/sim_wg0.pub')"
SIM_WG1_PUB="$(gx 153 'cat /root/lab-wg-keys/sim_wg1.pub')"
MR_WG0_PUB="$(gx 151 'cat /root/lab-wg-keys/mr_wg0.pub')"
MR_WG1_PUB="$(gx 151 'cat /root/lab-wg-keys/mr_wg1.pub')"
echo "  mr_wg0.pub=$MR_WG0_PUB"
echo "  sim_wg0.pub=$SIM_WG0_PUB"
echo "  mr_wg1.pub=$MR_WG1_PUB"
echo "  sim_wg1.pub=$SIM_WG1_PUB"

echo "== wizard setup (PPPoE WAN + LAN) =="
H "curl -sk --max-time 30 -X POST $MR_API/api/v1/setup/apply -H 'Content-Type: application/json' -d '{
  \"wan_interface\": \"eth0\",
  \"pppoe_username\": \"mr-test\",
  \"pppoe_password\": \"$PPPOE_PW\",
  \"admin_password\": \"$ADMIN_PW\",
  \"lan_interface\": \"eth1\",
  \"lan_ip_address\": \"10.77.0.1\"
}'" | head -c 600
echo

echo "== wait for PPPoE session =="
i=0
while [ $i -lt 40 ]; do
  if gx 151 'ip -4 -o addr show ppp0 2>/dev/null' | grep -q '10.250.0.50'; then
    echo "  ppp0 up: $(gx 151 'ip -4 -o addr show ppp0' | tr -s ' ')"
    break
  fi
  sleep 6; i=$((i+1))
done
[ $i -lt 40 ] || { echo "ERROR: PPPoE session did not come up"; gx 151 'tail -20 /var/log/pppd.log 2>/dev/null; logread | tail -20 2>/dev/null'; exit 1; }

echo "== login =="
COOKIE="/tmp/lab-cookie.txt"
H "curl -sk --max-time 10 -c $COOKIE -X POST $MR_API/api/v1/auth/login -H 'Content-Type: application/json' -d '{\"password\": \"$ADMIN_PW\"}'" >/dev/null
CFG="$(H "curl -sk --max-time 10 -b $COOKIE $MR_API/api/v1/config")"
REV="$(echo "$CFG" | python3 -c 'import json,sys; print(json.load(sys.stdin)["revision"])')"
echo "  current revision: $REV"

echo "== full lab profile (DHCP/DNS/ExtraLAN/wg0/wg1) =="
H "curl -sk --max-time 60 -b $COOKIE -X PUT $MR_API/api/v1/config -H 'Content-Type: application/json' -d '{
  \"revision\": $REV,
  \"system\": { \"hostname\": \"mr-test\", \"domain\": \"lab.test\", \"management_access\": \"lan_and_wireguard\" },
  \"lan\": { \"interface\": \"eth1\", \"ip_address\": \"10.77.0.1\", \"cidr\": \"/24\" },
  \"dhcp\": { \"enabled\": true, \"dns_enabled\": true, \"range_start\": \"10.77.0.100\", \"range_end\": \"10.77.0.200\", \"lease_time\": \"12h\" },
  \"dns\": { \"records\": [ { \"name\": \"router.home.arpa\", \"ip\": \"10.77.0.1\" }, { \"name\": \"client.home.arpa\", \"ip\": \"10.77.0.100\" } ] },
  \"firewall\": { \"extra_lans\": [ { \"id\": \"elab1\", \"name\": \"lab-extra\", \"interface\": \"eth2\", \"cidr\": \"10.78.0.0/24\", \"router_address\": \"10.78.0.1/24\", \"dst_ip\": \"10.78.0.10\", \"dst_port\": 8080, \"allow_from\": [\"10.77.0.0/24\"], \"enabled\": true } ] },
  \"wireguard\": { \"enabled\": true, \"interface\": \"wg0\", \"private_key\": \"$MR_WG0_KEY\", \"listen_port\": 51820, \"address\": \"10.6.0.1/24\", \"peers\": [ { \"id\": \"sim-peer\", \"name\": \"sim-lab\", \"public_key\": \"$SIM_WG0_PUB\", \"allowed_ips\": [\"10.6.0.10/32\"], \"endpoint\": \"10.250.0.10:51820\", \"enabled\": true } ] },
  \"wg_client\": { \"enabled\": true, \"interface\": \"wg1\", \"private_key\": \"$MR_WG1_KEY\", \"address\": \"10.79.0.1/32\", \"public_key\": \"$SIM_WG1_PUB\", \"endpoint\": \"10.79.0.2:51821\", \"allowed_ips\": [\"10.79.1.0/24\", \"10.79.0.2/32\"], \"persistent_keepalive\": 25 }
}'" | head -c 900
echo
sleep 5

echo "== confirmation window handling =="
PEND="$(H "curl -sk --max-time 10 -b $COOKIE $MR_API/api/v1/transactions/pending")"
if echo "$PEND" | grep -q '"state": "AwaitingConfirmation"'; then
  TXID="$(echo "$PEND" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')"
  echo "  confirming transaction $TXID"
  H "curl -sk --max-time 120 -b $COOKIE -X POST $MR_API/api/v1/transactions/$TXID/confirm" | head -c 400
  echo
else
  echo "  no pending confirmation"
fi

echo "== push MR keys into SIM-LAB wg peers =="
gx 153 "cat > /etc/wireguard/wg0.conf <<EOF
[Interface]
Address = 10.6.0.10/32
ListenPort = 51820
PrivateKey = \$(cat /root/lab-wg-keys/sim_wg0.key)

[Peer]
PublicKey = $MR_WG0_PUB
AllowedIPs = 10.6.0.1/32
EOF
systemctl restart wg-quick@wg0 || true"
gx 153 "cat > /etc/wireguard/wg1.conf <<EOF
[Interface]
Address = 10.79.0.2/24
ListenPort = 51821
PrivateKey = \$(cat /root/lab-wg-keys/sim_wg1.key)
PostUp = ip addr add 10.79.1.1/24 dev wg1 2>/dev/null || true
PostDown = ip addr del 10.79.1.1/24 dev wg1 2>/dev/null || true

[Peer]
PublicKey = $MR_WG1_PUB
AllowedIPs = 10.79.0.1/32
EOF
systemctl restart wg-quick@wg1 || true"

echo "== verify handshakes =="
sleep 8
gx 151 'wg show wg0 | grep -E "peer|latest handshake" ; wg show wg1 | grep -E "peer|latest handshake"'

echo "== verify end-to-end =="
echo "  wg0 tunnel ping:  $(gx 151 'ping -c1 -W2 10.6.0.10 2>&1 | tail -1')"
echo "  wg1 office ping:  $(gx 151 'ping -c1 -W2 10.79.1.1 2>&1 | tail -1')"
echo "  LAN client lease: $(gx 152 'ip -4 -o addr show | grep 10.77.0 || echo none')"
echo "  LAN client -> sim internet: $(gx 152 'curl -s --max-time 5 http://10.250.0.10/marker.txt 2>&1 || echo FAIL')"
echo "  LAN client DNS:  $(gx 152 'dig +short router.home.arpa @10.77.0.1 2>/dev/null || nslookup router.home.arpa 10.77.0.1 2>/dev/null | tail -2')"
echo "== MR-TEST lab profile configured =="
