#!/bin/sh
# 61 — Disabled peers are excluded from the runtime: a disabled peer must not
# appear in wg0 while enabled peers stay.
. "$(dirname "$0")/../lib.sh"
begin "61-wg-peer-disable"
phase "3-fault"
require "fault: none (wg disable)" ispfault status
phase "4.5-operator"
api_login
cleanup_disabled_peer() {
  current="$(api GET /api/v1/config 2>/dev/null || true)"
  [ -n "$current" ] || return 0
  clean="$(echo "$current" | python3 -c 'import json,sys; c=json.load(sys.stdin); c["wireguard"]["peers"]=[p for p in c["wireguard"].get("peers",[]) if p.get("id")!="lab-dis"]; print(json.dumps(c))')"
  api PUT /api/v1/config "$clean" >/dev/null 2>&1 && confirm_pending >/dev/null 2>&1 || true
}
trap cleanup_disabled_peer EXIT HUP INT TERM
PUB="$(wg genkey | wg pubkey)"
cfg="$(api GET /api/v1/config)"
new="$(echo "$cfg" | python3 -c '
import json,sys
c=json.load(sys.stdin)
c.setdefault("wireguard",{}).setdefault("peers",[]).append({"id":"lab-dis","public_key":"'$PUB'","allowed_ips":["10.6.0.99/32"],"name":"lab-disabled","enabled":False})
print(json.dumps(c))')"
require "disabled peer saved" api PUT /api/v1/config "$new"
require "disabled peer save confirmed" confirm_pending
sleep 3
check "disabled peer retained in canonical config" mr "grep -q lab-dis /var/lib/minimalrouter-applyd/last-good.json"
check "disabled peer not in wg0" check_not mr "wg show wg0 peers | grep -Fq $PUB"
check "existing peer still works" retry 60 mr "wg show wg0 | grep -q 'latest handshake'"
phase "4.5-cleanup"
cfg="$(api GET /api/v1/config)"
new="$(echo "$cfg" | python3 -c '
import json,sys
c=json.load(sys.stdin)
c["wireguard"]["peers"]=[p for p in c["wireguard"].get("peers",[]) if p.get("id")!="lab-dis"]
print(json.dumps(c))')"
require "disabled peer removal saved" api PUT /api/v1/config "$new"
require "disabled peer removal confirmed" confirm_pending
trap - EXIT HUP INT TERM
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
