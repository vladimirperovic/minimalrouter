#!/bin/sh
# 59 — Anti-lockout continuity: a LAN administrator cannot save a trust list
# that removes the caller's own source network.
. "$(dirname "$0")/../lib.sh"
begin "59-firewall-trusted-networks"
phase "3-fault"
require "fault: none (trusted nets)" ispfault status
phase "4.5-operator"
api_login
cfg="$(api GET /api/v1/config)"
before="$(echo "$cfg" | python3 -c 'import json,sys; print(json.dumps(json.load(sys.stdin)["trusted_networks"],sort_keys=True))')"
new="$(echo "$cfg" | python3 -c 'import json,sys; c=json.load(sys.stdin); c["trusted_networks"]=["10.6.0.0/24"]; print(json.dumps(c))')"
code="$(api_status PUT /api/v1/config "$new")"
require "self-lockout change rejected" test "$code" = "403"
after="$(api GET /api/v1/config | python3 -c 'import json,sys; print(json.dumps(json.load(sys.stdin)["trusted_networks"],sort_keys=True))')"
check "trusted networks remain unchanged" test "$before" = "$after"
check "LAN API remains reachable" api GET /api/v1/config
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
