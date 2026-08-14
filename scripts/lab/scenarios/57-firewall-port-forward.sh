#!/bin/sh
# 57 — Port forwards are tunnel-scoped. A forward is accepted only when
# WireGuard is enabled, the resulting DNAT rule is bound to wg0, and no DNAT is
# ever reachable from the WAN or ppp interface.
#
# This scenario previously asserted that every enabled forward was rejected with
# 422 and that no DNAT rule existed at all. Forwards now work over the tunnel,
# which is what the dashboard has always described; the invariant that survived
# unchanged is that nothing is exposed to WAN.
. "$(dirname "$0")/../lib.sh"
begin "57-firewall-port-forward"
phase "3-fault"
require "fault: none (port forward)" ispfault status
phase "4.5-operator"
api_login

cleanup_port_forward() {
  current="$(api GET /api/v1/config 2>/dev/null || true)"
  [ -n "$current" ] || return 0
  clean="$(echo "$current" | python3 -c 'import json,sys; c=json.load(sys.stdin); c["firewall"]["port_forwards"]=[r for r in c["firewall"].get("port_forwards",[]) if r.get("id")!="lab-pf"]; print(json.dumps(c))')"
  api PUT /api/v1/config "$clean" >/dev/null 2>&1 && confirm_pending >/dev/null 2>&1 || true
}
trap cleanup_port_forward EXIT HUP INT TERM

cfg="$(api GET /api/v1/config)"
wg_enabled="$(echo "$cfg" | python3 -c 'import json,sys; print(str(json.load(sys.stdin)["wireguard"]["enabled"]).lower())')"
require "lab router has the WireGuard entry point enabled" test "$wg_enabled" = "true"

new="$(echo "$cfg" | python3 -c '
import json,sys
c=json.load(sys.stdin)
c.setdefault("firewall",{}).setdefault("port_forwards",[]).append({"id":"lab-pf","name":"lab-fwd","protocol":"tcp","external_port":18080,"internal_ip":"192.168.1.187","internal_port":8080,"enabled":True})
print(json.dumps(c))')"
require "tunnel port forward accepted" api PUT /api/v1/config "$new"
require "tunnel port forward confirmed" confirm_pending
sleep 3

check "DNAT rule installed" mr "nft list table inet minimalrouter 2>/dev/null | grep -q 'dport 18080'"
check "DNAT rule is bound to the tunnel" mr "nft list table inet minimalrouter 2>/dev/null | grep 'dport 18080' | grep -q 'iifname \"wg0\"'"
check "DNAT rule is not reachable from WAN" check_not mr "nft list table inet minimalrouter 2>/dev/null | grep 'dport 18080' | grep -qE 'ppp0|eth0'"
check "WAN remains fail-closed" check_fw_not_fail_open

phase "4.5-cleanup"
cfg="$(api GET /api/v1/config)"
new="$(echo "$cfg" | python3 -c '
import json,sys
c=json.load(sys.stdin)
c["firewall"]["port_forwards"]=[r for r in c["firewall"].get("port_forwards",[]) if r.get("id")!="lab-pf"]
print(json.dumps(c))')"
require "port forward removal accepted" api PUT /api/v1/config "$new"
require "port forward removal confirmed" confirm_pending
trap - EXIT HUP INT TERM
check "DNAT rule removed" check_not mr "nft list table inet minimalrouter 2>/dev/null | grep -q 'dport 18080'"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
