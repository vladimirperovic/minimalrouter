#!/bin/sh
# 118 — Snapshot listing returns checksummed, revisioned metadata.
. "$(dirname "$0")/../lib.sh"
begin "118-snapshot-list-contract"
phase "3-fault"
require "fault: none (snapshot list)" ispfault status
phase "4.5-operator"
api_login
created="$(api POST /api/v1/snapshots)"
id="$(echo "$created" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("snapshot",{}).get("id", ""))' 2>/dev/null)"
list="$(api GET /api/v1/snapshots)"
found="$(echo "$list" | python3 -c 'import json,sys; d=json.load(sys.stdin); print(sum(1 for s in d.get("snapshots",[]) if s.get("id")=="'$id'" and s.get("checksum") and s.get("revision") is not None))' 2>/dev/null)"
check "created snapshot listed with metadata" test "$found" -eq 1
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
