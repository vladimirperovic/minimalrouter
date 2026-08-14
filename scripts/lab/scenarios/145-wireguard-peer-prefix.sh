#!/bin/sh
# 145 — A WireGuard server peer must own exactly one /32 inside wg0.
. "$(dirname "$0")/../lib.sh"
begin "145-wireguard-peer-prefix"
phase "3-fault"
require "fault: none (WireGuard peer prefix)" ispfault status
phase "4.5-operator"
api_login
cfg="$(api GET /api/v1/config)"
bad="$(echo "$cfg" | python3 -c '
import json,sys
c=json.load(sys.stdin)
c["wireguard"]["peers"].append({"id":"lab-peer","name":"lab-peer","public_key":"kCSYV3RvNOwDNvNVGrB4u463Njj+I//Edf63KtP3D20=","preshared_key":"","allowed_ips":["10.8.0.0/24"],"endpoint":"","enabled":True})
print(json.dumps(c))')"
require "non-/32 WireGuard peer rejected" save_expects_error "$bad"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
