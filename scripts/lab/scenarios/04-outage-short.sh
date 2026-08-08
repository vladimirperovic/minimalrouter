#!/bin/sh
# 04 — short ISP outage: server stops 30 s, then returns. One auto-reconnect.
. "$(dirname "$0")/../lib.sh"

begin "04-outage-short"
phase "3-fault"
require "fault: short outage started" ispfault outage short
require "symptom: session dropped" wait_pppoe_down 60

phase "4-mr-runtime"
check "LAN still up during outage" check_lan_up
check "local DNS still serves" check_local_dns

phase "6-7-recovery"
require "PPPoE reconnects after short outage" wait_pppoe 120
check "LAN client internet back" check_lan_internet
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"

capture_state "evidence"
finish_scenario
