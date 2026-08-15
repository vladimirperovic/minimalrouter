#!/bin/sh
# 115 — Wake-on-LAN accepts a valid MAC and sends the magic packet on the LAN.
. "$(dirname "$0")/../lib.sh"
begin "115-wol-broadcast-send"
phase "3-fault"
require "fault: none (wol send)" ispfault status
phase "4.5-operator"
api_login
code="$(api_status POST /api/v1/network/wol '{"mac":"02:00:00:00:00:99"}')"
check "valid WoL request accepted" test "$code" = "204"
check "firewall remains fail-closed" check_fw_not_fail_open
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
