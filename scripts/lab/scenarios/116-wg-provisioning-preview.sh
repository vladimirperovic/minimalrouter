#!/bin/sh
# 116 — WireGuard provisioning preview returns the next /32 without mutating config.
. "$(dirname "$0")/../lib.sh"
begin "116-wg-provisioning-preview"
phase "3-fault"
require "fault: none (wg preview)" ispfault status
phase "4.5-operator"
api_login
before="$(api GET /api/v1/config | python3 -c 'import json,sys; print(json.load(sys.stdin)["revision"])' 2>/dev/null)"
resp="$(api GET /api/v1/wireguard/provisioning-preview)"
client="$(echo "$resp" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("client_ip", ""))' 2>/dev/null)"
after="$(api GET /api/v1/config | python3 -c 'import json,sys; print(json.load(sys.stdin)["revision"])' 2>/dev/null)"
# The preview returns the address; the provisioning endpoint adds /32 when it
# persists the peer's AllowedIPs. Keep the contract check at the API boundary.
check "preview returns client IPv4" python3 -c 'import ipaddress,sys; ipaddress.IPv4Address(sys.argv[1])' "$client"
check "preview is read-only" test "$before" = "$after"
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
