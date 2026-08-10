#!/bin/sh
# 36 — Corrupted helper metadata: corrupt the router-applyd last-good file on
# the live router. The privileged helper must never apply a wrong
# configuration from corrupt metadata: a live save uses the in-memory truth
# and repairs the file, and a helper restart quarantines the corruption and
# recovers through canonical reconcile — the router must never be bricked or
# fail open.
. "$(dirname "$0")/../lib.sh"

begin "36-corrupt-metadata"
phase "3-fault"
require "fault: none (metadata corruption)" ispfault status

phase "4-mr-runtime"
check "MR up before corruption" mr "uptime -s | grep -q ."

phase "4.5-operator"
require "corrupt last-good.json" mr "echo 'garbage' > /var/lib/minimalrouter-applyd/last-good.json"

phase "4-mr-runtime-2"
# a live save must not be poisoned by the corrupt file. The helper refuses to
# apply while last-good is unreadable (recovery-required by design); a live
# save either repairs from the in-memory canonical state, or is blocked and
# recovery (reconcile) restores canonical + last-good from SQLite.
save_config
# A corrupt last-good puts the helper into recovery mode: neither the live save
# nor the API reconcile can rewrite it (both verify against last-good). The
# designed repair path is the applyd restart: startup reconciles quarantine the
# corrupt file and restore canonical + last-good from SQLite.
if ! mr "grep -q '\"revision\"' /var/lib/minimalrouter-applyd/last-good.json"; then
  echo "[note] live save left last-good corrupt (recovery required) — restarting applyd"
  require "restart applyd recovers from canonical" mr "rc-service router-applyd restart; sleep 8; rc-service router-applyd status | grep -q started"
fi
check "last-good is valid JSON again" retry 60 mr "grep -q '\"revision\"' /var/lib/minimalrouter-applyd/last-good.json"
check "canonical + last-good converge" retry 60 check_converge
check "firewall still policy-drop" check_fw_not_fail_open
check "internet still works" check_lan_internet

phase "4.5-cleanup"
# restart: the helper must quarantine the corrupt evidence and come back up
# (fail-closed would brick the router), then reconcile restores the canonical
# runtime from SQLite
require "corrupt last-good again" mr "echo 'garbage' > /var/lib/minimalrouter-applyd/last-good.json"
require "restart applyd self-heals (quarantine)" mr "rc-service router-applyd restart; sleep 5; rc-service router-applyd status | grep -q started"
check "quarantine evidence preserved" mr "ls /var/lib/minimalrouter-applyd/ | grep -qE 'last-good.*corrupt'"

phase "4-mr-runtime-3"
require "reconcile restores canonical state" api_reconcile
check "canonical + last-good converge after reconcile" retry 90 check_converge
check "wg0 back after reconcile" retry 60 mr "wg show wg0 | grep -q 'interface: wg0'"
check "internet works after recovery" retry 90 check_lan_internet
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"

capture_state "evidence"
finish_scenario
