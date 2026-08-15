#!/bin/sh
# 85 — CAKE is actually attached to the PPPoE and IFB interfaces, then the
# original QoS configuration is restored.
. "$(dirname "$0")/../lib.sh"
begin "85-qos-shaping-apply"
phase "3-fault"
require "fault: none (qos)" ispfault status
phase "4.5-operator"
api_login
original="$(api GET /api/v1/config)"
original_enabled="$(echo "$original" | python3 -c 'import json,sys; print(str(json.load(sys.stdin)["qos"]["enabled"]).lower())')"
require "scenario starts with QoS disabled" test "$original_enabled" = "false"
restore_qos() {
  current="$(api GET /api/v1/config 2>/dev/null)" || return 1
  restore="$(printf '%s' "$current" | ORIGINAL_JSON="$original" python3 -c '
import json,os,sys
current=json.load(sys.stdin)
original=json.loads(os.environ["ORIGINAL_JSON"])
current["qos"] = original["qos"]
print(json.dumps(current))
')" || return 1
  api PUT /api/v1/config "$restore" >/dev/null 2>&1 || return 1
  confirm_pending >/dev/null 2>&1
}
trap restore_qos EXIT HUP INT TERM

enabled="$(echo "$original" | python3 -c '
import json,sys
c=json.load(sys.stdin)
c["qos"].update({"enabled":True,"algorithm":"cake","download_limit_mbps":80,"upload_limit_mbps":16})
print(json.dumps(c))')"
require "CAKE configuration saved" api PUT /api/v1/config "$enabled"
require "CAKE change confirmed" confirm_pending
require "QoS plan contains exact limits" retry 45 mr "grep -q 'ppp0 root cake bandwidth 16000kbit' /etc/minimalrouter/qos.plan && grep -q 'ifb0 root cake bandwidth 80000kbit' /etc/minimalrouter/qos.plan"
check "CAKE attached to PPPoE egress" mr "tc qdisc show dev ppp0 | grep -q cake"
check "CAKE attached to IFB download path" mr "tc qdisc show dev ifb0 | grep -q cake"

phase "4.5-cleanup"
restore_qos
trap - EXIT HUP INT TERM
check "QoS qdiscs removed after restore" retry 45 mr "! tc qdisc show dev ppp0 | grep -q cake; ! tc qdisc show dev ifb0 2>/dev/null | grep -q cake"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
