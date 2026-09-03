#!/bin/sh
# 36 — Corrupted helper metadata: corrupt the router-applyd last-good file on
# the live router. The privileged helper must never apply a wrong
# configuration from corrupt metadata. A malformed root-owned recovery record
# must fail closed; recovery requires an explicit restore of a trusted backup,
# never silently accepting unverified canonical state.
. "$(dirname "$0")/../lib.sh"

begin "36-corrupt-metadata"
backup=/tmp/minimalrouter-last-good.lab-backup
recover_metadata() {
  mr "if test -f '$backup'; then install -m 600 '$backup' /var/lib/minimalrouter-applyd/last-good.json; fi; rc-service router-applyd restart" >/dev/null 2>&1 || true
  mr "rm -f '$backup'" >/dev/null 2>&1 || true
}
trap recover_metadata EXIT HUP INT TERM
phase "3-fault"
require "fault: none (metadata corruption)" ispfault status

phase "4-mr-runtime"
check "MR up before corruption" mr "uptime -s | grep -q ."

phase "4.5-operator"
require "save trusted last-good backup" mr "install -m 600 /var/lib/minimalrouter-applyd/last-good.json '$backup'"
require "corrupt last-good.json" mr "echo 'garbage' > /var/lib/minimalrouter-applyd/last-good.json"

phase "4-mr-runtime-2"
# A corrupt recovery record blocks both mutation and helper startup. That
# conservative behavior prevents the privileged helper from guessing at state.
check "live save is rejected with corrupt last-good" check_not save_config
check "restart applyd fails closed on corrupt metadata" check_not mr "rc-service router-applyd restart; sleep 5; rc-service router-applyd status | grep -q started"

phase "4.5-recovery"
require "trusted backup restores applyd" mr "install -m 600 '$backup' /var/lib/minimalrouter-applyd/last-good.json; rc-service router-applyd restart; sleep 8; rc-service router-applyd status | grep -q started"
require "canonical reconcile after trusted restore" api_reconcile
trap - EXIT HUP INT TERM
mr "rm -f '$backup'"
check "canonical + last-good converge after recovery" retry 90 check_converge
check "wg0 back after recovery" retry 60 mr "wg show wg0 | grep -q 'interface: wg0'"
check "internet works after recovery" retry 90 check_lan_internet
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"

capture_state "evidence"
finish_scenario
