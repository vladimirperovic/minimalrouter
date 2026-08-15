#!/bin/sh
# 97 — Invalid WireGuard keys are rejected by config validation.
. "$(dirname "$0")/../lib.sh"
begin "97-config-validation-wg-key"
phase "3-fault"
require "fault: none (wg key validation)" ispfault status
phase "4.5-operator"
api_login
cfg="$(api GET /api/v1/config)"
revision="$(echo "$cfg" | python3 -c 'import json,sys; print(json.load(sys.stdin)["revision"])')"
bad="$(echo "$cfg" | python3 -c '
import json,sys
c=json.load(sys.stdin)
peers=c.get("wireguard",{}).get("peers") or []
peers.append({"id":"lab-bad","public_key":"not-a-valid-key","allowed_ips":["10.6.0.97/32"],"name":"bad","enabled":True})
c["wireguard"]["peers"]=peers
print(json.dumps(c))')"
code="$(api_status PUT /api/v1/config "$bad")"
check "invalid WG key rejected with 422" test "$code" = "422"
after="$(api GET /api/v1/config | python3 -c 'import json,sys; print(json.load(sys.stdin)["revision"])')"
check "rejected key did not mutate config" test "$after" = "$revision"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
