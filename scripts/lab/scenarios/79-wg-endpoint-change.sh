#!/bin/sh
# 79 — An unreachable WireGuard endpoint stops fresh handshakes without
# harming the router; restoring the exact original endpoint recovers it.
. "$(dirname "$0")/../lib.sh"
begin "79-wg-endpoint-change"
phase "3-fault"
require "fault: none (wg endpoint)" ispfault status
phase "4.5-operator"
api_login
original="$(api GET /api/v1/config)"
peer_id="$(echo "$original" | python3 -c 'import json,sys; print(next(p["id"] for p in json.load(sys.stdin)["wireguard"]["peers"] if p.get("enabled")))')"
peer_pub="$(echo "$original" | python3 -c 'import json,sys; print(next(p["public_key"] for p in json.load(sys.stdin)["wireguard"]["peers"] if p.get("enabled")))')"
original_endpoint="$(echo "$original" | python3 -c 'import json,sys; print(next(p.get("endpoint","") for p in json.load(sys.stdin)["wireguard"]["peers"] if p.get("enabled")))')"
restore_endpoint() {
  current="$(api GET /api/v1/config 2>/dev/null || true)"
  [ -n "$current" ] || return 0
  restored="$(echo "$current" | python3 -c "import json,sys; c=json.load(sys.stdin); p=next(x for x in c['wireguard']['peers'] if x.get('id')=='$peer_id'); p['endpoint']='$original_endpoint'; print(json.dumps(c))")"
  api PUT /api/v1/config "$restored" >/dev/null 2>&1 && confirm_pending >/dev/null 2>&1 || true
}
trap restore_endpoint EXIT HUP INT TERM
bad="$(echo "$original" | python3 -c "import json,sys; c=json.load(sys.stdin); p=next(x for x in c['wireguard']['peers'] if x.get('id')=='$peer_id'); p['endpoint']='10.250.0.254:51820'; print(json.dumps(c))")"
require "unreachable endpoint saved" api PUT /api/v1/config "$bad"
require "endpoint change confirmed" confirm_pending
sleep 15
stale="$(mr "now=\$(date +%s); hs=\$(wg show wg0 latest-handshakes | awk '\$1==\"$peer_pub\"{print \$2}'); echo \$((now-hs))" | tr -d ' \n')"
check "endpoint change stops fresh handshakes" test "${stale:-0}" -ge 15
check "internet unaffected by peer outage" check_lan_internet

phase "4.5-cleanup"
current="$(api GET /api/v1/config)"
restored="$(echo "$current" | python3 -c "import json,sys; c=json.load(sys.stdin); p=next(x for x in c['wireguard']['peers'] if x.get('id')=='$peer_id'); p['endpoint']='$original_endpoint'; print(json.dumps(c))")"
require "original endpoint restored" api PUT /api/v1/config "$restored"
require "endpoint restoration confirmed" confirm_pending
trap - EXIT HUP INT TERM
check "tunnel traffic recovers on original endpoint" retry 90 mr "ping -c1 -W3 10.6.0.10 >/dev/null 2>&1"
check "handshake recovers on original endpoint" retry 30 check_wg_recent wg0 90
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
