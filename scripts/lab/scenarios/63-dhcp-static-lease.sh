#!/bin/sh
# 63 — Static DHCP lease: a reserved address is handed to the client and the
# pool stays healthy.
. "$(dirname "$0")/../lib.sh"
begin "63-dhcp-static-lease"
phase "3-fault"
require "fault: none (static lease)" ispfault status
phase "4.5-operator"
api_login
cleanup_static_lease() {
  current="$(api GET /api/v1/config 2>/dev/null || true)"
  [ -n "$current" ] || return 0
  clean="$(echo "$current" | python3 -c 'import json,sys; c=json.load(sys.stdin); c["dhcp"]["static_leases"]=[l for l in c["dhcp"].get("static_leases",[]) if l.get("id")!="lab-lease"]; print(json.dumps(c))')"
  api PUT /api/v1/config "$clean" >/dev/null 2>&1 && confirm_pending >/dev/null 2>&1 || true
}
trap cleanup_static_lease EXIT HUP INT TERM
cfg="$(api GET /api/v1/config)"
new="$(echo "$cfg" | python3 -c '
import json,sys
c=json.load(sys.stdin)
c.setdefault("dhcp",{})
sl=c["dhcp"].get("static_leases") or []
sl.append({"id":"lab-lease","hostname":"lab-static","mac":"02:bb:cc:dd:ee:01","ip_address":"192.168.1.50"})
c["dhcp"]["static_leases"]=sl
print(json.dumps(c))')"
require "static lease saved" api PUT /api/v1/config "$new"
require "static lease save confirmed" confirm_pending
sleep 3
check "lease in dnsmasq config" mr "grep -q 02:bb:cc:dd:ee:01 /etc/dnsmasq.d/minimalrouter.conf"
check "firewall still policy-drop" check_fw_not_fail_open
phase "4.5-cleanup"
cfg="$(api GET /api/v1/config)"
new="$(echo "$cfg" | python3 -c '
import json,sys
c=json.load(sys.stdin)
c["dhcp"]["static_leases"]=[l for l in c["dhcp"].get("static_leases",[]) if l.get("id")!="lab-lease"]
print(json.dumps(c))')"
require "static lease removal saved" api PUT /api/v1/config "$new"
require "static lease removal confirmed" confirm_pending
trap - EXIT HUP INT TERM
check "lease removed" check_not mr "grep -q 02:bb:cc:dd:ee:01 /etc/dnsmasq.d/minimalrouter.conf"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
