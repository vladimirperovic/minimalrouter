#!/bin/sh
# 60 — wireguard_only is provisional until confirmed through the tunnel. A
# LAN/loopback confirmation is denied and expiry restores LAN management.
. "$(dirname "$0")/../lib.sh"
begin "60-mgmt-access-wireguard-only"
phase "3-fault"
require "fault: none (mgmt access)" ispfault status
phase "4.5-operator"
api_login
cfg="$(api GET /api/v1/config)"
new="$(echo "$cfg" | python3 -c 'import json,sys; c=json.load(sys.stdin); c["system"]["management_access"]="wireguard_only"; print(json.dumps(c))')"
code="$(api_status PUT /api/v1/config "$new")"
require "wireguard-only change enters confirmation window" test "$code" = "202"
sleep 3
lan_code="$(api_unauth_status GET /api/v1/config)"
check "LAN management is blocked provisionally" test "$lan_code" = "403"

local_login="$(mr "curl -sk -m 10 -c /tmp/lab-local-cookie.txt -X POST https://127.0.0.1:8443/api/v1/auth/login -H 'Content-Type: application/json' -d '{\"password\":\"$ADMIN_PW\"}'")"
local_csrf="$(echo "$local_login" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("csrf_token",""))' 2>/dev/null)"
pending="$(mr "curl -sk -m 10 -b /tmp/lab-local-cookie.txt https://127.0.0.1:8443/api/v1/transactions/pending")"
txid="$(echo "$pending" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("id",""))' 2>/dev/null)"
require "pending transaction visible locally" test -n "$txid"
local_confirm="$(mr "curl -sk -m 10 -o /dev/null -w '%{http_code}' -b /tmp/lab-local-cookie.txt -X POST https://127.0.0.1:8443/api/v1/transactions/$txid/confirm -H 'X-CSRF-Token: $local_csrf'")"
check "non-WireGuard confirmation rejected" test "$local_confirm" = "403"

phase "7-recovery"
lan_api_reachable() { test "$(api_unauth_status GET /api/v1/config)" = "401"; }
require "confirmation expiry restores LAN management" retry 120 lan_api_reachable
lan "rm -f $API_COOKIE" >/dev/null 2>&1
rm -f "$API_CSRF"
api_login
mode="$(api GET /api/v1/config | python3 -c 'import json,sys; print(json.load(sys.stdin)["system"].get("management_access",""))')"
check "canonical mode rolled back" test "$mode" = "lan_and_wireguard"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
