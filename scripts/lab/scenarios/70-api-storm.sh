#!/bin/sh
# 70 — A burst of revision-correct configuration saves must not wedge the API
# or leave a pending transaction behind.
. "$(dirname "$0")/../lib.sh"
begin "70-api-storm"
phase "3-fault"
require "fault: none (api storm)" ispfault status
phase "4.5-operator"
api_login
initial_cfg="$(api GET /api/v1/config)"
initial_lease="$(echo "$initial_cfg" | python3 -c 'import json,sys; print(json.load(sys.stdin)["dhcp"]["lease_time"])')"
restore_storm() {
  current="$(api GET /api/v1/config 2>/dev/null || true)"
  [ -n "$current" ] || return 0
  restored="$(echo "$current" | python3 -c "import json,sys; c=json.load(sys.stdin); c['dhcp']['lease_time']='$initial_lease'; print(json.dumps(c))")"
  api PUT /api/v1/config "$restored" >/dev/null 2>&1 && confirm_pending >/dev/null 2>&1 || true
}
trap restore_storm EXIT HUP INT TERM
for i in 1 2 3 4 5 6 7 8; do
  cfg="$(api GET /api/v1/config)"
  new="$(echo "$cfg" | python3 -c "import json,sys; c=json.load(sys.stdin); c['dhcp']['lease_time']='4h' if $i%2 else '5h'; print(json.dumps(c))")"
  require "save $i accepted" api PUT /api/v1/config "$new"
  require "save $i confirmed" confirm_pending
done
phase "4-mr-runtime-2"
check "routerd responsive after storm" retry 60 mr "rc-service routerd status | grep -q started"
check "fresh save still works after storm" save_config
pending="$(api GET /api/v1/transactions/pending | python3 -c 'import json,sys; print(str(json.load(sys.stdin).get("pending",True)).lower())')"
check "no transaction left pending" test "$pending" = "false"
current="$(api GET /api/v1/config)"
restore="$(echo "$current" | python3 -c "import json,sys; c=json.load(sys.stdin); c['dhcp']['lease_time']='$initial_lease'; print(json.dumps(c))")"
require "original lease setting restored" api PUT /api/v1/config "$restore"
require "lease restoration confirmed" confirm_pending
trap - EXIT HUP INT TERM
check "firewall still policy-drop" check_fw_not_fail_open
check "internet still works" check_lan_internet
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
