#!/bin/sh
# 45 — A validation failure must leave canonical state, last-good state and
# runtime unchanged while the router continues serving traffic.
. "$(dirname "$0")/../lib.sh"
begin "45-config-rollback-failed-apply"
phase "3-fault"
require "fault: none (invalid config)" ispfault status
phase "4-mr-runtime"
require "converged before rejection" check_converge
phase "4.5-operator"
api_login
cfg="$(api GET /api/v1/config)"
revision="$(echo "$cfg" | python3 -c 'import json,sys; print(json.load(sys.stdin)["revision"])')"
original_ip="$(echo "$cfg" | python3 -c 'import json,sys; print(json.load(sys.stdin)["lan"]["ip_address"])')"
bad="$(echo "$cfg" | python3 -c 'import json,sys; c=json.load(sys.stdin); c["lan"]["ip_address"]="999.999.999.999"; print(json.dumps(c))')"
code="$(api_status PUT /api/v1/config "$bad")"
check "invalid address rejected with 422" test "$code" = "422"
after="$(api GET /api/v1/config)"
after_revision="$(echo "$after" | python3 -c 'import json,sys; print(json.load(sys.stdin)["revision"])')"
after_ip="$(echo "$after" | python3 -c 'import json,sys; print(json.load(sys.stdin)["lan"]["ip_address"])')"
check "canonical revision unchanged" test "$after_revision" = "$revision"
check "canonical LAN address unchanged" test "$after_ip" = "$original_ip"
phase "4-mr-runtime-2"
check "runtime LAN address unchanged" mr "ip -4 addr show '$MR_LAN_IF' | grep -q '$MR_LAN_IP/'"
check "routerd still alive" mr "rc-service routerd status | grep -q started"
check "firewall still policy-drop" check_fw_not_fail_open
check "internet still works" check_lan_internet
phase "7-recovery"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
