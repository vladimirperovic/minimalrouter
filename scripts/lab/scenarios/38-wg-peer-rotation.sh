#!/bin/sh
# 38 — Control-plane peer rotation adds a fresh key to wg0 runtime, preserves
# existing peers, then removes only the temporary peer.
. "$(dirname "$0")/../lib.sh"
begin "38-wg-peer-rotation"
phase "3-fault"
require "fault: none (peer rotation)" ispfault status
api_login
PKEY="$(wg genkey 2>/dev/null)"
PUB="$(echo "$PKEY" | wg pubkey 2>/dev/null)"
require "fresh WireGuard keypair generated" test -n "$PKEY" -a -n "$PUB"
cleanup_peer() {
  cfg="$(api GET /api/v1/config 2>/dev/null || true)"
  [ -n "$cfg" ] || return 0
  clean="$(echo "$cfg" | python3 -c 'import json,sys; c=json.load(sys.stdin); c["wireguard"]["peers"]=[p for p in c["wireguard"].get("peers",[]) if p.get("id")!="lab-rot-peer"]; print(json.dumps(c))')"
  api PUT /api/v1/config "$clean" >/dev/null 2>&1 && confirm_pending >/dev/null 2>&1 || true
}
trap cleanup_peer EXIT HUP INT TERM
phase "4.5-operator"
cfg="$(api GET /api/v1/config)"
newpeer="$(echo "$cfg" | python3 -c "
import ipaddress,json,sys
c=json.load(sys.stdin)
used={str(ipaddress.ip_network(route,strict=False)) for p in c['wireguard'].get('peers',[]) for route in p.get('allowed_ips',[])}
route=next(str(ipaddress.ip_network(f'10.6.0.{i}/32')) for i in range(20,250) if str(ipaddress.ip_network(f'10.6.0.{i}/32')) not in used)
c['wireguard']['peers'].append({'id':'lab-rot-peer','public_key':'$PUB','allowed_ips':[route],'name':'lab-rotation','enabled':True})
print(json.dumps(c))")"
require "temporary peer saved" api PUT /api/v1/config "$newpeer"
require "peer addition confirmed" confirm_pending
check "peer added to canonical config" mr "grep -q lab-rot-peer /var/lib/minimalrouter-applyd/last-good.json"
require "peer active in wg0 runtime" retry 60 mr "wg show wg0 peers | grep -Fq '$PUB'"
check "existing peer handshake survives" retry 90 mr "wg show wg0 | grep -q 'latest handshake'"
check "firewall still policy-drop" check_fw_not_fail_open
phase "4.5-remove"
cleanup_peer
trap - EXIT HUP INT TERM
check "peer removed from canonical config" check_not mr "grep -q lab-rot-peer /var/lib/minimalrouter-applyd/last-good.json"
check "peer gone from wg0 runtime" retry 60 check_not mr "wg show wg0 peers | grep -Fq '$PUB'"
check "canonical + last-good converge" check_converge
check "existing peer still works after removal" retry 90 mr "wg show wg0 | grep -q 'latest handshake'"
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
