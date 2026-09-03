#!/bin/sh
# 153 — Per-device traffic accounting: opt-in, counted in the forward chain,
# and reported per LAN address without storing anything else about the device.
. "$(dirname "$0")/../lib.sh"
begin "153-accounting-per-device"
phase "3-fault"
require "fault: none (accounting)" ispfault status
phase "4.5-operator"
api_login
original="$(api GET /api/v1/config)"

restore_accounting() {
  current="$(api GET /api/v1/config 2>/dev/null)" || return 1
  restore="$(printf '%s' "$current" | ORIGINAL_JSON="$original" python3 -c '
import json,os,sys
current=json.load(sys.stdin)
original=json.loads(os.environ["ORIGINAL_JSON"])
current["accounting"] = original.get("accounting", {"enabled": False, "retention_months": 13})
print(json.dumps(current))
')" || return 1
  api PUT /api/v1/config "$restore" >/dev/null 2>&1 || return 1
  confirm_pending >/dev/null 2>&1
}
trap restore_accounting EXIT HUP INT TERM

check "accounting sets absent while disabled" check_not mr "nft list table inet minimalrouter 2>/dev/null | grep -q acct_rx"

enabled="$(echo "$original" | python3 -c '
import json,sys
c=json.load(sys.stdin)
c["accounting"] = {"enabled": True, "retention_months": 13}
print(json.dumps(c))')"
require "accounting enabled" api PUT /api/v1/config "$enabled"
require "accounting change confirmed" confirm_pending
sleep 3

check "download counter set installed" mr "nft list table inet minimalrouter 2>/dev/null | grep -q 'set acct_rx'"
check "upload counter set installed" mr "nft list table inet minimalrouter 2>/dev/null | grep -q 'set acct_tx'"
check "counting rules carry no verdict" check_not mr "nft list table inet minimalrouter 2>/dev/null | grep 'update @acct_' | grep -qE ' (accept|drop|reject)'"

# Generate traffic from the LAN client so at least one host is counted.
require "LAN client generates traffic" lan "wget -q -O /dev/null http://10.250.0.1/ 2>/dev/null || ping -c 20 -s 1400 10.250.0.1 >/dev/null 2>&1 || true"
require "counters observed in the kernel" retry 60 mr "nft -j list set inet minimalrouter acct_tx | grep -q counter"

# The collector runs every five minutes; allow one complete collection cycle.
accounting_has_device_usage() {
  api GET "/api/v1/accounting?months=1" | python3 -c '
import json,sys
d=json.load(sys.stdin)
sys.exit(not any(month.get("devices") for month in d.get("months", [])))
'
}
# Collection is intentionally every five minutes to minimize appliance load.
require "API reports per-device usage" retry 360 accounting_has_device_usage
check "usage response marks the feature available" api GET "/api/v1/accounting?months=1" | grep -q '"available":true'

phase "4.5-cleanup"
restore_accounting
trap - EXIT HUP INT TERM
sleep 3
check "accounting sets removed after disable" check_not mr "nft list table inet minimalrouter 2>/dev/null | grep -q acct_rx"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
