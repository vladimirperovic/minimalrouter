#!/bin/sh
# 58 — Custom firewall rules: a bounded LAN allow rule is generated into the
# ruleset and disappears again when removed.
. "$(dirname "$0")/../lib.sh"
begin "58-firewall-custom-rule"
phase "3-fault"
require "fault: none (custom rule)" ispfault status
phase "4.5-operator"
api_login
cleanup_custom_rule() {
  current="$(api GET /api/v1/config 2>/dev/null || true)"
  [ -n "$current" ] || return 0
  clean="$(echo "$current" | python3 -c 'import json,sys; c=json.load(sys.stdin); c["firewall"]["custom_rules"]=[r for r in c["firewall"].get("custom_rules",[]) if r.get("id")!="lab-cr"]; print(json.dumps(c))')"
  api PUT /api/v1/config "$clean" >/dev/null 2>&1 && confirm_pending >/dev/null 2>&1 || true
}
trap cleanup_custom_rule EXIT HUP INT TERM
cfg="$(api GET /api/v1/config)"
new="$(echo "$cfg" | python3 -c '
import json,sys
c=json.load(sys.stdin)
c.setdefault("firewall",{}).setdefault("custom_rules",[]).append({"id":"lab-cr","name":"lab-custom","action":"allow","direction":"input","protocol":"tcp","src_ip":"192.168.1.187","dst_port":18443,"enabled":True})
print(json.dumps(c))')"
require "custom rule accepted" api PUT /api/v1/config "$new"
require "custom rule transaction confirmed" confirm_pending
sleep 3
check "custom rule in ruleset" mr "nft list chain inet minimalrouter input 2>/dev/null | grep -q 18443"
phase "4.5-cleanup"
cfg="$(api GET /api/v1/config)"
new="$(echo "$cfg" | python3 -c '
import json,sys
c=json.load(sys.stdin)
c["firewall"]["custom_rules"]=[r for r in c["firewall"].get("custom_rules",[]) if r.get("id")!="lab-cr"]
print(json.dumps(c))')"
require "custom rule removal accepted" api PUT /api/v1/config "$new"
require "custom rule removal confirmed" confirm_pending
trap - EXIT HUP INT TERM
check "custom rule removed" check_not mr "nft list chain inet minimalrouter input 2>/dev/null | grep -q 18443"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
