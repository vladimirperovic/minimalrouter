#!/bin/sh
# 110 — Canonical GET redacts every secret-bearing configuration field.
. "$(dirname "$0")/../lib.sh"
begin "110-config-secret-redaction"
phase "3-fault"
require "fault: none (config redaction)" ispfault status
phase "4.5-operator"
api_login
cfg="$(api GET /api/v1/config)"
redacted="$(echo "$cfg" | python3 -c 'import json,sys; d=json.load(sys.stdin); vals=[d["wan"].get("password"),d["wireguard"].get("private_key"),d["cloudflare"].get("api_token"),d["squid_proxy"].get("password"),d["wifi"].get("passphrase")]; print(sum(v=="[REDACTED]" for v in vals))' 2>/dev/null)"
check "all top-level secrets redacted" test "$redacted" -eq 5
check "admin password absent" test "$(printf '%s' "$cfg" | grep -Fc "$ADMIN_PW")" -eq 0
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
