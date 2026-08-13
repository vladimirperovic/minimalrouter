#!/bin/sh
# 147 — Outbound WireGuard cannot capture the default route.
. "$(dirname "$0")/../lib.sh"
begin "147-wireguard-client-full-tunnel"
phase "3-fault"
require "fault: none (WireGuard full tunnel)" ispfault status
phase "4.5-operator"
api_login
cfg="$(api GET /api/v1/config)"
bad="$(echo "$cfg" | python3 -c '
import json,sys
c=json.load(sys.stdin)
c["wg_client"]={"enabled":True,"interface":"wg1","private_key":"kCSYV3RvNOwDNvNVGrB4u463Njj+I//Edf63KtP3D20=","address":"10.9.0.2/32","public_key":"kCSYV3RvNOwDNvNVGrB4u463Njj+I//Edf63KtP3D20=","preshared_key":"","endpoint":"203.0.113.5:51820","allowed_ips":["0.0.0.0/0"],"persistent_keepalive":25}
print(json.dumps(c))')"
require "outbound full-tunnel route rejected" save_expects_error "$bad"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
