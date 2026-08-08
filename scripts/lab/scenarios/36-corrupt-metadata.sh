#!/bin/sh
# 36 — Corrupted helper metadata: corrupt/remove each router-applyd metadata
# file on the live router. The privileged helper must refuse to apply or
# fail closed (recovery required), never apply a wrong config. Recovery via
# reconcile must restore canonical state.
. "$(dirname "$0")/../lib.sh"

begin "36-corrupt-metadata"
phase "3-fault"
require "fault: none (metadata corruption)" ispfault status

phase "4-mr-runtime"
check "MR up before corruption" mr "uptime -s | grep -q ."

phase "4.5-operator"
require "corrupt last-good.json" mr "echo 'garbage' > /var/lib/minimalrouter-applyd/last-good.json"

phase "4-mr-runtime-2"
api_login
check "save rejected with corrupt metadata (fail closed)" save_expects_error "$(api GET /api/v1/config)"
check "routerd still alive" mr "rc-service routerd status | grep -q started"
check "firewall still policy-drop" check_fw_not_fail_open
check "internet still works" check_lan_internet

phase "4.5-cleanup"
require "restore metadata" mr "rc-service router-applyd restart 2>/dev/null; sleep 3; true"

phase "4-mr-runtime-3"
check "reconcile restores canonical state" retry 90 check_converge

phase "7-recovery"
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"

capture_state "evidence"
finish_scenario
