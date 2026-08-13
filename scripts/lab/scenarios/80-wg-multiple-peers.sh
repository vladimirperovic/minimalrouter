#!/bin/sh
# 80 — Multiple enabled peers coexist: both appear in the runtime and the
# live peer keeps its handshake.
. "$(dirname "$0")/../lib.sh"
begin "80-wg-multiple-peers"
phase "3-fault"
require "fault: none (wg multi)" ispfault status
phase "4.5-operator"
api_login
cleanup_extra_peer() {
  current="$(api GET /api/v1/config 2>/dev/null || true)"
  [ -n "$current" ] || return 0
  clean="$(echo "$current" | python3 -c 'import json,sys; c=json.load(sys.stdin); c["wireguard"]["peers"]=[p for p in c["wireguard"].get("peers",[]) if p.get("id")!="lab-extra"]; print(json.dumps(c))')"
  api PUT /api/v1/config "$clean" >/dev/null 2>&1 && confirm_pending >/dev/null 2>&1 || true
}
trap cleanup_extra_peer EXIT HUP INT TERM
PUB="$(wg genkey | wg pubkey)"
cfg="$(api GET /api/v1/config)"
new="$(echo "$cfg" | python3 -c '
import json,sys
c=json.load(sys.stdin)
peers=c.get("wireguard",{}).get("peers") or []
peers.append({"id":"lab-extra","public_key":"'$PUB'","allowed_ips":["10.6.0.98/32"],"name":"lab-extra","enabled":True})
c["wireguard"]["peers"]=peers
print(json.dumps(c))')"
require "second peer saved" api PUT /api/v1/config "$new"
require "second peer save confirmed" confirm_pending
sleep 3
check "second peer in runtime" mr "wg show wg0 peers | grep -Fq $PUB"
check "live peer handshake intact" retry 60 mr "wg show wg0 | grep -q 'latest handshake'"
phase "4.5-cleanup"
cfg="$(api GET /api/v1/config)"
new="$(echo "$cfg" | python3 -c '
import json,sys
c=json.load(sys.stdin)
c["wireguard"]["peers"]=[p for p in c["wireguard"].get("peers",[]) if p.get("id")!="lab-extra"]
print(json.dumps(c))')"
require "second peer removal saved" api PUT /api/v1/config "$new"
require "second peer removal confirmed" confirm_pending
trap - EXIT HUP INT TERM
check "peer removed" check_not mr "wg show wg0 peers | grep -Fq $PUB"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
