#!/bin/sh
# 93 — Sustained authenticated API reads do not balloon routerd memory.
. "$(dirname "$0")/../lib.sh"
begin "93-routerd-memory-stability"
phase "3-fault"
require "fault: none (memory)" ispfault status
phase "4.5-operator"
api_login
readmem() {
  attempt=0
  while [ "$attempt" -lt 3 ]; do
    pid="$(mr "pgrep -o routerd" | tr -d ' \r\n')"
    value="$(mr "grep VmRSS /proc/$pid/status 2>/dev/null" | awk '{print $2}')"
    case "$value" in (''|*[!0-9]*) sleep 3 ;; (*) echo "$value"; return 0 ;; esac
    attempt=$((attempt+1))
  done
  echo 0
}
mem1="$(readmem)"
success=0
i=0
while [ "$i" -lt 60 ]; do
  if api GET /api/v1/config >/dev/null 2>&1; then success=$((success+1)); fi
  i=$((i+1))
done
mem2="$(readmem)"
check "all sustained API reads succeeded" test "$success" -eq 60
check "routerd RSS remains below baseline plus 20 MiB" sh -c "[ '$mem1' -gt 0 ] && [ '$mem2' -gt 0 ] && [ '$mem2' -le $((mem1+20480)) ]"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
