#!/bin/sh
# 65 — Enable the packaged DNS filter, verify its artifact and resolution,
# then restore the exact original configuration.
. "$(dirname "$0")/../lib.sh"
begin "65-adblock-enable"
phase "3-fault"
require "fault: none (adblock)" ispfault status
phase "4.5-operator"
api_login
original="$(api GET /api/v1/config)"
original_enabled="$(echo "$original" | python3 -c 'import json,sys; print(str(json.load(sys.stdin)["adguard"]["enabled"]).lower())')"
require "scenario starts with DNS filter disabled" test "$original_enabled" = "false"
restore_filter() { api PUT /api/v1/config "$original" >/dev/null 2>&1 && confirm_pending >/dev/null 2>&1 || true; }
trap restore_filter EXIT HUP INT TERM
new="$(echo "$original" | python3 -c 'import json,sys; c=json.load(sys.stdin); c["adguard"]["enabled"]=True; print(json.dumps(c))')"
require "DNS filter enable saved" api PUT /api/v1/config "$new"
require "DNS filter enable confirmed" confirm_pending
require "adblock artifact created" retry 45 mr "test -f /etc/dnsmasq.d/adblock_hosts.conf"
check "DNS still resolves" check_local_dns
phase "4.5-cleanup"
restore_filter
trap - EXIT HUP INT TERM
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
