#!/bin/sh
# 52 — Session lifecycle: logout invalidates the cookie and stale sessions
# receive an explicit 401 while a new administrator session still works.
. "$(dirname "$0")/../lib.sh"
begin "52-auth-session-logout"
phase "3-fault"
require "fault: none (session)" ispfault status
phase "4.5-operator"
api_login
check "authenticated config read works" api GET /api/v1/config
require "logout succeeds" api POST /api/v1/auth/logout
code="$(api_status GET /api/v1/config)"
require "config read rejected after logout" test "$code" = "401"
lan "rm -f $API_COOKIE" >/dev/null 2>&1
rm -f "$API_CSRF"
require "new session works after logout" api_login
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
