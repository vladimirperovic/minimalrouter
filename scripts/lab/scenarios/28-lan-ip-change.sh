#!/bin/sh
# 28 — A live cross-subnet LAN change must be rejected atomically and direct
# the operator to the local recovery console.
. "$(dirname "$0")/../lib.sh"
begin "28-lan-ip-change"
phase "3-fault"
require "fault: none (LAN transition validation)" ispfault status
phase "4.5-operator"
api_login
cfg="$(api GET /api/v1/config)"
revision="$(echo "$cfg" | python3 -c 'import json,sys; print(json.load(sys.stdin)["revision"])')"
candidate="$(echo "$cfg" | python3 -c '
import json,sys
c=json.load(sys.stdin)
c["lan"].update({"ip_address":"192.168.2.1","cidr":"192.168.2.1/24","netmask":"255.255.255.0"})
c["dhcp"].update({"range_start":"192.168.2.100","range_end":"192.168.2.200"})
c["trusted_networks"]=["192.168.1.0/24","192.168.2.0/24"]
print(json.dumps(c))')"
body_file="/tmp/lab28-response.txt"
csrf="$(cat "$API_CSRF")"
candidate_b64="$(printf '%s' "$candidate" | base64 | tr -d '\n')"
code="$(lan "echo '$candidate_b64' | base64 -d | curl -sk --max-time 30 -o $body_file -w '%{http_code}' -b $API_COOKIE -X PUT -H 'X-CSRF-Token: $csrf' -H 'Content-Type: application/json' --data-binary @- $MR_API/api/v1/config")"
check "cross-subnet live change rejected with 422" test "$code" = "422"
check "response directs operator to recovery console" lan "grep -qi 'recovery console' $body_file"
after="$(api GET /api/v1/config)"
after_revision="$(echo "$after" | python3 -c 'import json,sys; print(json.load(sys.stdin)["revision"])')"
check "rejected transition leaves canonical revision unchanged" test "$after_revision" = "$revision"
check "original LAN address remains active" mr "ip -4 addr show '$MR_LAN_IF' | grep -q '$MR_LAN_IP/'"
check "candidate LAN address was never applied" check_not mr "ip -4 addr show '$MR_LAN_IF' | grep -q '192.168.2.1/'"
check "LAN client keeps its original lease" lan "ip -4 -o addr show '$LAN_CLIENT_IF' | grep -q '192.168.1.'"
check "internet remains available" check_lan_internet
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
