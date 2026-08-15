#!/bin/sh
# 76 — A long-lived TCP transfer survives the nftables regeneration caused by
# a real configuration save; no tautological assertion is allowed.
. "$(dirname "$0")/../lib.sh"
begin "76-firewall-stateful-flows"
phase "3-fault"
require "fault: none (stateful)" ispfault status
phase "4.5-operator"
api_login
original="$(api GET /api/v1/config)"
original_lease="$(echo "$original" | python3 -c 'import json,sys; print(json.load(sys.stdin)["dhcp"]["lease_time"])')"
FLOW_PID=""
cleanup_stateful() {
  [ -z "$FLOW_PID" ] || kill "$FLOW_PID" 2>/dev/null || true
  sim "rm -f /var/www/extralan/lab-stateful.bin" >/dev/null 2>&1 || true
  current="$(api GET /api/v1/config 2>/dev/null || true)"
  [ -n "$current" ] || return 0
  restored="$(echo "$current" | python3 -c "import json,sys; c=json.load(sys.stdin); c['dhcp']['lease_time']='$original_lease'; print(json.dumps(c))")"
  api PUT /api/v1/config "$restored" >/dev/null 2>&1 && confirm_pending >/dev/null 2>&1 || true
}
trap cleanup_stateful EXIT HUP INT TERM
require "prepare slow HTTP stream on ExtraLAN simulator" sim "dd if=/dev/zero of=/var/www/extralan/lab-stateful.bin bs=1024 count=1280 status=none"
require "stream endpoint listening" retry 20 sim "ss -tln | grep -q ':8080'"
lan "rm -f /tmp/lab-stateful.bin; curl -fsS --limit-rate 64k --max-time 30 http://10.78.0.10:8080/lab-stateful.bin -o /tmp/lab-stateful.bin; test \$(wc -c < /tmp/lab-stateful.bin) -eq 1310720" &
FLOW_PID=$!
sleep 3
require "flow established before reload" lan "ss -tn | grep -q '10.78.0.10:8080'"
check "config save reloads policy during flow" mr_save_lease
wait "$FLOW_PID"
FLOW_RC=$?
check "established flow completed through reload" test "$FLOW_RC" -eq 0

phase "4.5-cleanup"
kill "$FLOW_PID" 2>/dev/null || true
sim "rm -f /var/www/extralan/lab-stateful.bin" >/dev/null 2>&1 || true
current="$(api GET /api/v1/config)"
restore="$(echo "$current" | python3 -c "import json,sys; c=json.load(sys.stdin); c['dhcp']['lease_time']='$original_lease'; print(json.dumps(c))")"
require "original lease setting restored" api PUT /api/v1/config "$restore"
require "restoration confirmed" confirm_pending
trap - EXIT HUP INT TERM
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
