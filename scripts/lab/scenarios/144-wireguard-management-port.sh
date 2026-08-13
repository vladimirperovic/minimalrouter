#!/bin/sh
# 144 — WireGuard cannot bind the HTTPS management port.
. "$(dirname "$0")/../lib.sh"
begin "144-wireguard-management-port"
phase "3-fault"
require "fault: none (WireGuard port collision)" ispfault status
phase "4.5-operator"
api_login
cfg="$(api GET /api/v1/config)"
bad="$(echo "$cfg" | python3 -c 'import json,sys; c=json.load(sys.stdin); c["wireguard"]["listen_port"]=c["system"]["https_port"]; print(json.dumps(c))')"
require "WireGuard management-port collision rejected" save_expects_error "$bad"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
