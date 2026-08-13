#!/bin/sh
# 98 — Overlapping WireGuard peer routes are rejected.
. "$(dirname "$0")/../lib.sh"
begin "98-config-validation-wg-route"
phase "3-fault"
require "fault: none (wg route validation)" ispfault status
phase "4.5-operator"
api_login
cfg="$(api GET /api/v1/config)"
revision="$(echo "$cfg" | python3 -c 'import json,sys; print(json.load(sys.stdin)["revision"])')"
peer_count="$(echo "$cfg" | python3 -c 'import json,sys; print(len(json.load(sys.stdin).get("wireguard",{}).get("peers") or []))')"
require "baseline has a WireGuard peer" test "$peer_count" -gt 0
bad="$(echo "$cfg" | python3 -c '
import json,sys
c=json.load(sys.stdin)
peers=c.get("wireguard",{}).get("peers") or []
duplicate_route=peers[0]["allowed_ips"][0]
peers.append({"id":"lab-dup","public_key":"kCSYV3RvNOwDNvNVGrB4u463Njj+I//Edf63KtP3D20=","allowed_ips":[duplicate_route],"name":"dup","enabled":True})
c["wireguard"]["peers"]=peers
print(json.dumps(c))')"
code="$(api_status PUT /api/v1/config "$bad")"
check "duplicate WG route rejected with 422" test "$code" = "422"
after="$(api GET /api/v1/config | python3 -c 'import json,sys; print(json.load(sys.stdin)["revision"])')"
check "rejected route did not mutate config" test "$after" = "$revision"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
