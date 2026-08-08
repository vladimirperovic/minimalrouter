#!/bin/sh
# MR-TEST full lab profile configuration.
# Runs on the mac; drives the Proxmox host (curl via host to MR-TEST API) and
# guest exec into VMs. Requires: isp, sim, mr deploy steps done.
# Configures: PPPoE WAN (wizard), LAN/DHCP/DNS, ExtraLAN, wg0 peer, wg1 office,
# then pushes MR public keys into SIM-LAB wg peers.

set -eu

HOST="${LAB_HOST:-root@192.168.1.2}"
SSHOPTS="-o BatchMode=yes -o ConnectTimeout=10"
KEY="${LAB_SSH_KEY:-${HOME:-/root}/.ssh/lab_id_ed25519}"
KNOWN_HOSTS="${LAB_KNOWN_HOSTS:-${HOME:-/root}/.ssh/known_hosts}"
LABDIR="$(cd "$(dirname "$0")" && pwd)"
SEC="$(cd "$LABDIR/../../private/secrets" && pwd)"
H() { ssh $SSHOPTS -i "$KEY" -o UserKnownHostsFile="$KNOWN_HOSTS" "$HOST" "$@"; }
gx() {
  H "qm guest exec $1 -- sh -c \"$2\"" | python3 -c '
import json,sys,base64
try:
    d=json.load(sys.stdin)
    ret=d.get("return",d)
    od=ret.get("out-data","")
    if od:
        try:
            sys.stdout.write(base64.b64decode(od, validate=True).decode("utf-8","replace"))
        except Exception:
            sys.stdout.write(od)
    ec=ret.get("exitcode")
    sys.exit(ec if isinstance(ec,int) else 1)
except SystemExit:
    raise
except Exception as e:
    sys.stderr.write("gx decode: %s\n" % e); sys.exit(1)'
}

MR_API="https://192.168.1.1:8443"
PPPOE_PW="minimalrouter-lab-pppoe"
ADMIN_PW="${LAB_ADMIN_PW:-MinimalRouter-Lab-Test!2026}"

echo "== wait for MR-TEST API =="
i=0
while [ $i -lt 40 ]; do
  gx 151 'ip route add 192.168.1.254/32 dev eth1 2>/dev/null || true'
  st="$(H "curl -sk --max-time 5 $MR_API/api/v1/setup/status 2>/dev/null" || true)"
  if echo "$st" | grep -qE '"is_configured": ?(false|true)'; then
    echo "  MR-TEST API up (is_configured=$(echo "$st" | grep -oE '"(is_configured)": ?(false|true)' | grep -oE 'false|true'))"
    break
  fi
  sleep 8; i=$((i+1))
done
[ $i -lt 40 ] || { echo "ERROR: MR-TEST API did not come up"; exit 1; }

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

if ! echo "$st" | grep -qE '"is_configured": ?true'; then
echo "== wizard setup (PPPoE WAN + LAN) =="
H "curl -sk --max-time 30 -X POST $MR_API/api/v1/setup/apply -H 'Content-Type: application/json' -d '{
  \"wan_interface\": \"eth0\",
  \"pppoe_username\": \"mr-test\",
  \"pppoe_password\": \"$PPPOE_PW\",
  \"admin_password\": \"$ADMIN_PW\",
  \"lan_interface\": \"eth1\",
  \"lan_ip_address\": \"192.168.1.1\"
}'" | head -c 600
echo
fi

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
CSRF="$(H "curl -sk --max-time 10 -c $COOKIE -X POST $MR_API/api/v1/auth/login -H 'Content-Type: application/json' -d '{\"password\": \"$ADMIN_PW\"}'" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("csrf_token",""))' 2>/dev/null)"
CFG="$(H "curl -sk --max-time 10 -b $COOKIE $MR_API/api/v1/config")"
REV="$(echo "$CFG" | python3 -c 'import json,sys; print(json.load(sys.stdin)["revision"])')"
echo "  current revision: $REV"

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


echo "== full lab profile (DHCP/DNS/ExtraLAN/wg0/wg1) =="
# Merge lab fields into the CURRENT config (PUT replaces the whole object; the
# WAN/PPPoE section must survive). LAN subnet stays 192.168.1.1 — the product
# rejects live LAN subnet changes (use the recovery console for that), and the
# scenarios' lib.sh expects MR_LAN_IP=192.168.1.1 anyway.
BODY="$(echo "$CFG" | python3 -c '
import json,sys
c=json.load(sys.stdin)
c["system"]["hostname"]="mr-test"; c["system"]["domain"]="lab.test"
c["system"]["management_access"]="lan_and_wireguard"
c["lan"]["ip_address"]="192.168.1.1"; c["lan"]["cidr"]="192.168.1.1/24"
c["dhcp"]={"enabled":True,"dns_enabled":False,"range_start":"192.168.1.100","range_end":"192.168.1.200","lease_time":"12h","dns_servers":["1.1.1.1","1.0.0.1"]}
c["dns"]={"records":[{"name":"router.home.arpa","ip":"192.168.1.1"},{"name":"client.home.arpa","ip":"192.168.1.100"}]}
c["firewall"]["extra_lans"]=[{"id":"elab1","name":"lab-extra","interface":"eth2","cidr":"10.78.0.0/24","router_address":"10.78.0.1/24","dst_ip":"10.78.0.10","dst_port":8080,"allow_from":["192.168.1.0/24"],"enabled":True}]
c["wireguard"]={"enabled":True,"interface":"wg0","private_key":"'"$MR_WG0_KEY"'","listen_port":51820,"address":"10.6.0.1/24","peers":[{"id":"sim-peer","name":"sim-lab","public_key":"'"$SIM_WG0_PUB"'","allowed_ips":["10.6.0.10/32"],"endpoint":"10.250.0.10:51820","enabled":True}]}
c["wg_client"]={"enabled":False,"interface":"wg1","private_key":"'$MR_WG1_KEY'","address":"10.79.0.1/32","public_key":"'$SIM_WG1_PUB'","endpoint":"10.79.0.2:51821","allowed_ips":["10.79.1.0/24"],"persistent_keepalive":25}
c["trusted_networks"]=["192.168.1.0/24","10.6.0.0/24"]
print(json.dumps(c))
')"
H "curl -sk --max-time 60 -b $COOKIE -X PUT $MR_API/api/v1/config -H 'Content-Type: application/json' -H 'X-CSRF-Token: $CSRF' -d '$BODY'" | head -c 900
echo
sleep 5

echo "== confirmation window handling =="
# The apply's network reconfiguration wipes the host route to MR-TEST; restore
# it from inside the VM (no host access needed), then confirm the pending tx.
# Window is 90s — retry a few times in case of a race.
sleep 3
for attempt in 1 2 3 4 5; do
  gx 151 'ip route add 192.168.1.254/32 dev eth1 2>/dev/null || true'
  PEND="$(H "curl -sk --max-time 10 -b $COOKIE $MR_API/api/v1/transactions/pending")"
  if echo "$PEND" | grep -qE '"pending": ?true'; then
    TXID="$(echo "$PEND" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')"
    echo "  confirming transaction $TXID (attempt $attempt)"
    R="$(H "curl -sk --max-time 120 -b $COOKIE -X POST $MR_API/api/v1/transactions/$TXID/confirm -H 'Content-Type: application/json' -H 'X-CSRF-Token: $CSRF'")"
    echo "$R" | head -c 400
    echo
    if echo "$R" | grep -q '"success": true\|"state": "Confirmed"'; then
      echo "  CONFIRMED"
      break
    fi
  fi
  sleep 5
done

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
