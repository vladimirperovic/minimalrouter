#!/bin/sh
# 50 — Saturate every logical CPU while performing a confirmed config save,
# then stop only the lab load and restore the exact original configuration.
. "$(dirname "$0")/../lib.sh"
begin "50-cpu-saturation-save"
phase "3-fault"
require "fault: none (CPU stress)" ispfault status
api_login
original="$(api GET /api/v1/config)"
original_lease="$(echo "$original" | python3 -c 'import json,sys; print(json.load(sys.stdin)["dhcp"]["lease_time"])')"
cleanup_cpu() {
  mr "if [ -f /run/lab-cpu-load.pids ]; then while read p; do kill \"\$p\" 2>/dev/null || true; done </run/lab-cpu-load.pids; rm -f /run/lab-cpu-load.pids; fi" >/dev/null 2>&1 || true
  current="$(api GET /api/v1/config 2>/dev/null || true)"
  if [ -n "$current" ]; then
    restored="$(echo "$current" | python3 -c "import json,sys; c=json.load(sys.stdin); c['dhcp']['lease_time']='$original_lease'; print(json.dumps(c))")"
    api PUT /api/v1/config "$restored" >/dev/null 2>&1 && confirm_pending >/dev/null 2>&1 || true
  fi
}
trap cleanup_cpu EXIT HUP INT TERM
phase "4.5-operator"
require "start one CPU load per logical core" mr "rm -f /run/lab-cpu-load.pids; cores=\$(getconf _NPROCESSORS_ONLN); i=0; while [ \$i -lt \$cores ]; do nohup yes >/dev/null 2>&1 & echo \$! >>/run/lab-cpu-load.pids; i=\$((i+1)); done; sleep 2; [ \$(wc -l </run/lab-cpu-load.pids) -eq \$cores ] && while read p; do kill -0 \$p || exit 1; done </run/lab-cpu-load.pids"
phase "4-mr-runtime-2"
check "routerd remains responsive" retry 60 mr "rc-service routerd status | grep -q started"
new="$(echo "$original" | python3 -c '
import json,sys
c=json.load(sys.stdin)
c["dhcp"]["lease_time"]="3h" if c["dhcp"].get("lease_time")!="3h" else "4h"
print(json.dumps(c))')"
require "config save succeeds under CPU load" api PUT /api/v1/config "$new"
require "loaded save confirmed" confirm_pending
check "firewall still policy-drop" check_fw_not_fail_open
phase "4.5-cleanup"
require "stop only lab CPU workers" mr "while read p; do kill \$p 2>/dev/null || true; done </run/lab-cpu-load.pids; rm -f /run/lab-cpu-load.pids; sleep 2; ! pgrep -x yes >/dev/null"
current="$(api GET /api/v1/config)"
restored="$(echo "$current" | python3 -c "import json,sys; c=json.load(sys.stdin); c['dhcp']['lease_time']='$original_lease'; print(json.dumps(c))")"
require "original lease duration restored" api PUT /api/v1/config "$restored"
require "restore confirmed" confirm_pending
trap - EXIT HUP INT TERM
phase "7-recovery"
check "canonical + last-good converge" retry 60 check_converge
check "internet works after stress" check_lan_internet
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
