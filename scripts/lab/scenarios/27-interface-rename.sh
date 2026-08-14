#!/bin/sh
# 27 — Interface collision protection: the LAN cannot be moved onto eth2
# while that interface belongs to the configured isolated ExtraLAN.
. "$(dirname "$0")/../lib.sh"

begin "27-interface-rename"
phase "3-fault"
require "fault: none (config change)" ispfault status

phase "4.5-operator"
api_login
cfg="$(api GET /api/v1/config)"
bad="$(echo "$cfg" | python3 -c 'import json,sys; c=json.load(sys.stdin); c["lan"]["interface"]="eth2"; print(json.dumps(c))')"
require "LAN/ExtraLAN interface collision rejected" save_expects_error "$bad"

phase "7-recovery"
check "LAN remains on $MR_LAN_IF" mr "ip -4 addr show '$MR_LAN_IF' 2>/dev/null | grep -q '$MR_LAN_IP/'"
check "LAN client remains connected" check_lan_up
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"

capture_state "evidence"
finish_scenario
