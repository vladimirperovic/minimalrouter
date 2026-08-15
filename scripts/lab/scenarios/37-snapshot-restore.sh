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
    print(d.get("snapshot",{}).get("id",""))
except Exception:
    print("")' 2>/dev/null)"
check "snapshot created" test -n "$snap"

phase "4-mr-runtime-2"
cfg="$(api GET /api/v1/config)"
original_lease="$(echo "$cfg" | python3 -c 'import json,sys; print(json.load(sys.stdin)["dhcp"]["lease_time"])')"
cleanup_snapshot_test() {
  current="$(api GET /api/v1/config 2>/dev/null || true)"
  [ -n "$current" ] || return 0
  restored_cfg="$(echo "$current" | python3 -c "import json,sys; c=json.load(sys.stdin); c['dhcp']['lease_time']='$original_lease'; print(json.dumps(c))")"
  api PUT /api/v1/config "$restored_cfg" >/dev/null 2>&1 && confirm_pending >/dev/null 2>&1 || true
}
trap cleanup_snapshot_test EXIT HUP INT TERM
new="$(echo "$cfg" | python3 -c 'import json,sys
c=json.load(sys.stdin)
c["dhcp"]["lease_time"]="30m" if c["dhcp"].get("lease_time")!="30m" else "1h"
print(json.dumps(c))')"
require "snapshot mutation accepted" api PUT /api/v1/config "$new"
confirm_pending
sleep 2
mutated="$(api GET /api/v1/config | python3 -c 'import json,sys; print(json.load(sys.stdin)["dhcp"]["lease_time"])' 2>/dev/null)"
check "snapshot test value became canonical" test "$mutated" != "$(echo "$cfg" | python3 -c 'import json,sys; print(json.load(sys.stdin)["dhcp"]["lease_time"])')"

phase "4.5-restore"
require "restore snapshot" api POST "/api/v1/snapshots/$snap/restore"
confirm_pending
sleep 3
check "MR alive after snapshot restore" mr "uptime -s | grep -q ."

phase "7-recovery"
check "canonical + last-good converge after restore" retry 90 check_converge
restored="$(api GET /api/v1/config | python3 -c 'import json,sys; print(json.load(sys.stdin)["dhcp"]["lease_time"])' 2>/dev/null)"
check "snapshot restored original value" test "$restored" = "$(echo "$cfg" | python3 -c 'import json,sys; print(json.load(sys.stdin)["dhcp"]["lease_time"])')"
trap - EXIT HUP INT TERM
check "firewall still policy-drop" check_fw_not_fail_open
check "internet works after restore" check_lan_internet
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"

capture_state "evidence"
finish_scenario
