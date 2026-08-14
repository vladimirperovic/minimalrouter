#!/bin/sh
# 117 — Outbound WireGuard key generation returns valid, unique key pairs.
. "$(dirname "$0")/../lib.sh"
begin "117-wg-client-key-uniqueness"
phase "3-fault"
require "fault: none (wg key generation)" ispfault status
phase "4.5-operator"
api_login
one="$(api POST /api/v1/wireguard/client/keys)"
two="$(api POST /api/v1/wireguard/client/keys)"
pub1="$(echo "$one" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("public_key", ""))' 2>/dev/null)"
pub2="$(echo "$two" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("public_key", ""))' 2>/dev/null)"
priv1="$(echo "$one" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("private_key", ""))' 2>/dev/null)"
check "public key is WireGuard length" test "${#pub1}" -eq 44
check "private key is WireGuard length" test "${#priv1}" -eq 44
check "generated keys are unique" test "$pub1" != "$pub2"
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
