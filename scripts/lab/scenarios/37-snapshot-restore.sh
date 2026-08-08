#!/bin/sh
# 37 — Snapshot round-trip: create a config snapshot, mutate the config,
# restore the snapshot, and verify the router converges back to the
# snapshotted state.
. "$(dirname "$0")/../lib.sh"

begin "37-snapshot-restore"
phase "3-fault"
require "fault: none (snapshot path)" ispfault status

phase "4-mr-runtime"
check "MR up before snapshot" mr "uptime -s | grep -q ."

phase "4.5-operator"
api_login
snap="$(api POST /api/v1/snapshots | python3 -c 'import json,sys
try:
    d=json.load(sys.stdin)
    print(d.get("id",""))
except Exception:
    print("")' 2>/dev/null)"
check "snapshot created" test -n "$snap"

phase "4-mr-runtime-2"
cfg="$(api GET /api/v1/config)"
new="$(echo "$cfg" | python3 -c 'import json,sys
c=json.load(sys.stdin)
c["dhcp"]["lease_time"]=1800 if c["dhcp"].get("lease_time")!=1800 else 3600
print(json.dumps(c))')"
api PUT /api/v1/config "$new" >/dev/null 2>&1
sleep 2
check "config mutated" check_converge

phase "4.5-restore"
require "restore snapshot" api POST "/api/v1/snapshots/$snap/restore"
sleep 3
check "MR alive after snapshot restore" mr "uptime -s | grep -q ."

phase "7-recovery"
check "canonical + last-good converge after restore" retry 90 check_converge
check "firewall still policy-drop" check_fw_not_fail_open
check "internet works after restore" check_lan_internet
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"

capture_state "evidence"
finish_scenario
