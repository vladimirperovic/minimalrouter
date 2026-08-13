#!/bin/sh
# 94 — applyd restart is idempotent: reconcile does not churn the canonical
# revision or drop the tunnel.
. "$(dirname "$0")/../lib.sh"
begin "94-applyd-restart-idempotent"
phase "3-fault"
require "fault: none (applyd restart)" ispfault status
phase "4.5-operator"
rev1="$(mr 'cat /var/lib/minimalrouter-applyd/last-good.json' | python3 -c 'import json,sys; print(json.load(sys.stdin)["revision"])' 2>/dev/null)"
require "pre-restart revision is present" test -n "$rev1"
require "router-applyd restart succeeds" mr "rc-service router-applyd restart"
sleep 8
rev2="$(mr 'cat /var/lib/minimalrouter-applyd/last-good.json' | python3 -c 'import json,sys; print(json.load(sys.stdin)["revision"])' 2>/dev/null)"
check "revision unchanged by reconcile" test -n "$rev2" -a "$rev1" = "$rev2"
check "wg0 handshake survives" retry 90 mr "wg show wg0 | grep -q 'latest handshake'"
check "internet still works" check_lan_internet
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
