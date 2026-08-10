#!/bin/sh
# 38 — WireGuard peer rotation: provision a new wg0 peer, verify it can
# connect, then remove it. The router must re-read peer state and keep the
# existing peer working.
. "$(dirname "$0")/../lib.sh"

begin "38-wg-peer-rotation"
phase "3-fault"
require "fault: none (peer rotation)" ispfault status

phase "4-mr-runtime"
check "MR up before peer rotation" mr "uptime -s | grep -q ."
check "wg0 up with existing peer" mr "wg show wg0 | grep -q 'latest handshake'"

phase "4.5-operator"
api_login
# generate a fresh keypair for the new peer
PKEY="$(wg genkey 2>/dev/null)"
PUB="$(echo "$PKEY" | wg pubkey 2>/dev/null)"
newpeer="$(api GET /api/v1/config | python3 -c "
import json,sys
c=json.load(sys.stdin)
p={'id':'lab-rot-peer','public_key':'$PUB','allowed_ips':['10.6.0.99/32'],'name':'lab-rotation','enabled':True}
c.setdefault('wireguard',{}).setdefault('peers',[]).append(p)
print(json.dumps(c))")"
api PUT /api/v1/config "$newpeer" >/dev/null 2>&1
confirm_pending
sleep 2
check "peer added to canonical config" mr "grep -q lab-rot-peer /var/lib/minimalrouter-applyd/last-good.json"
check "peer active in wg0 runtime" retry 60 mr "wg show wg0 peers | grep -Fq $PUB"

phase "4-mr-runtime-2"
check "existing peer still has handshake" retry 90 mr "wg show wg0 | grep -q 'latest handshake'"
check "firewall still policy-drop" check_fw_not_fail_open

phase "4.5-remove"
removecfg="$(api GET /api/v1/config | python3 -c "
import json,sys
c=json.load(sys.stdin)
c['wireguard']['peers']=[p for p in c['wireguard'].get('peers',[]) if p.get('id')!='lab-rot-peer']
print(json.dumps(c))")"
api PUT /api/v1/config "$removecfg" >/dev/null 2>&1
confirm_pending
sleep 2
check "peer removed from canonical config" check_not mr "grep -q lab-rot-peer /var/lib/minimalrouter-applyd/last-good.json"
check "peer gone from wg0 runtime" retry 60 check_not mr "wg show wg0 peers | grep -Fq $PUB"

phase "7-recovery"
check "canonical + last-good converge" check_converge
check "existing peer still works after removal" retry 90 mr "wg show wg0 | grep -q 'latest handshake'"
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"

capture_state "evidence"
finish_scenario
