#!/bin/sh
# 92 — Render fifty temporary static leases, keep DNS responsive under the
# larger configuration, then restore the original lease set.
. "$(dirname "$0")/../lib.sh"
begin "92-dnsmasq-lease-load"
phase "3-fault"
require "fault: none (lease load)" ispfault status
phase "4.5-operator"
api_login
original="$(api GET /api/v1/config)"
original_count="$(echo "$original" | python3 -c 'import json,sys; print(len(json.load(sys.stdin)["dhcp"].get("static_leases") or []))')"
restore_leases() {
  current="$(api GET /api/v1/config 2>/dev/null)" || return 1
  restore="$(printf '%s' "$current" | ORIGINAL_JSON="$original" python3 -c '
import json,os,sys
current=json.load(sys.stdin)
original=json.loads(os.environ["ORIGINAL_JSON"])
current["dhcp"]["static_leases"] = original["dhcp"].get("static_leases") or []
print(json.dumps(current))
')" || return 1
  api PUT /api/v1/config "$restore" >/dev/null 2>&1 || return 1
  confirm_pending >/dev/null 2>&1
}
trap restore_leases EXIT HUP INT TERM
loaded="$(echo "$original" | python3 -c '
import ipaddress,json,sys
c=json.load(sys.stdin)
leases=list(c["dhcp"].get("static_leases") or [])
used={x.get("ip_address") for x in leases}
pool_start=ipaddress.ip_address(c["dhcp"]["range_start"])
pool_end=ipaddress.ip_address(c["dhcp"]["range_end"])
lan=ipaddress.ip_interface(c["lan"]["cidr"])
added=0
for ip in lan.network.hosts():
    if ip==lan.ip or pool_start <= ip <= pool_end or str(ip) in used:
        continue
    added += 1
    leases.append({"id":f"lab-load-{added:02d}","hostname":f"lab-load-{added:02d}","mac":f"02:92:00:00:00:{added:02x}","ip_address":str(ip)})
    if added==50: break
if added != 50: raise SystemExit("not enough addresses outside DHCP pool")
c["dhcp"]["static_leases"]=leases
print(json.dumps(c))')"
require "fifty temporary static leases saved" api PUT /api/v1/config "$loaded"
require "loaded lease configuration confirmed" confirm_pending
require "dnsmasq renders all temporary leases" retry 45 mr "test \"\$(grep -c 'lab-load-' /etc/dnsmasq.d/minimalrouter.conf)\" -eq 50"
check "local DNS works with loaded configuration" check_local_dns
check "dnsmasq stays active" mr "rc-service dnsmasq status | grep -q started"
phase "4.5-cleanup"
restore_leases
trap - EXIT HUP INT TERM
restored_count="$(api GET /api/v1/config | python3 -c 'import json,sys; print(len(json.load(sys.stdin)["dhcp"].get("static_leases") or []))')"
check "original static lease count restored" test "$restored_count" = "$original_count"
check "temporary leases removed from runtime" check_not mr "grep -q 'lab-load-' /etc/dnsmasq.d/minimalrouter.conf"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
