#!/bin/sh
# 149 — Enabling Squid requires a credential of at least twelve characters.
. "$(dirname "$0")/../lib.sh"
begin "149-squid-short-password"
phase "3-fault"
require "fault: none (Squid credentials)" ispfault status
phase "4.5-operator"
api_login
cfg="$(api GET /api/v1/config)"
bad="$(echo "$cfg" | python3 -c 'import json,sys; c=json.load(sys.stdin); c["squid_proxy"].update({"enabled":True,"port":3128,"username":"lab","password":"short"}); print(json.dumps(c))')"
require "short Squid password rejected" save_expects_error "$bad"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
