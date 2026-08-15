#!/bin/sh
# 151 — QoS must survive a change that restarts PPPoE.
#
# Scenario 85 enables QoS on its own, which leaves cfg.WAN untouched: applyd's
# wanChanged branch never fires, pppd is never restarted, and the shaper is
# never at risk. The ordering bug lived precisely in the combination -- shaping
# was applied and then destroyed by the PPPoE restart later in the same apply --
# so this scenario changes QoS and WAN together.
. "$(dirname "$0")/../lib.sh"
begin "151-qos-survives-wan-change"
phase "3-fault"
require "fault: none (qos + wan)" ispfault status
phase "4.5-operator"
api_login
original="$(api GET /api/v1/config)"

restore_all() {
  current="$(api GET /api/v1/config 2>/dev/null)" || return 1
  restore="$(printf '%s' "$current" | ORIGINAL_JSON="$original" python3 -c '
import json,os,sys
current=json.load(sys.stdin)
original=json.loads(os.environ["ORIGINAL_JSON"])
current["qos"] = original["qos"]
current["wan"]["mtu"] = original["wan"]["mtu"]
print(json.dumps(current))
')" || return 1
  api PUT /api/v1/config "$restore" >/dev/null 2>&1 || return 1
  confirm_pending >/dev/null 2>&1
}
trap restore_all EXIT HUP INT TERM

# Enable shaping first so the second apply is the one that also touches WAN.
enabled="$(echo "$original" | python3 -c '
import json,sys
c=json.load(sys.stdin)
c["qos"].update({"enabled":True,"algorithm":"cake","download_limit_mbps":80,"upload_limit_mbps":16})
print(json.dumps(c))')"
require "QoS enabled" api PUT /api/v1/config "$enabled"
require "QoS change confirmed" confirm_pending
require "CAKE attached before the WAN change" retry 60 mr "tc qdisc show dev ppp0 | grep -q cake"

# Now change a WAN field. applyd restarts pppd, which destroys ppp0 and every
# qdisc on it.
cfg="$(api GET /api/v1/config)"
mtu_changed="$(echo "$cfg" | python3 -c '
import json,sys
c=json.load(sys.stdin)
c["wan"]["mtu"] = 1480 if c["wan"].get("mtu") != 1480 else 1492
print(json.dumps(c))')"
require "WAN MTU change accepted" api PUT /api/v1/config "$mtu_changed"
require "WAN MTU change confirmed" confirm_pending

require "PPPoE reconnects after the MTU change" wait_pppoe 180
check "CAKE still attached after PPPoE restart" retry 90 mr "tc qdisc show dev ppp0 | grep -q cake"
check "IFB download path still shaped" retry 90 mr "tc qdisc show dev ifb0 | grep -q cake"

phase "4.5-cleanup"
restore_all
trap - EXIT HUP INT TERM
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
