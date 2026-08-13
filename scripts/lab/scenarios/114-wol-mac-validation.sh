#!/bin/sh
# 114 — Wake-on-LAN rejects malformed MAC addresses before sending a packet.
. "$(dirname "$0")/../lib.sh"
begin "114-wol-mac-validation"
phase "3-fault"
require "fault: none (wol validation)" ispfault status
phase "4.5-operator"
api_login
code="$(api_status POST /api/v1/network/wol '{"mac":"not-a-mac"}')"
check "invalid MAC rejected" test "$code" = "400"
check "LAN remains available" check_lan_up
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
