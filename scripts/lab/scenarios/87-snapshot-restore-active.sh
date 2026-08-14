#!/bin/sh
# 87 — Restore a snapshot while a LAN client continuously sends traffic.
. "$(dirname "$0")/../lib.sh"
begin "87-snapshot-restore-active"
phase "3-fault"
require "fault: none (snapshot)" ispfault status
phase "4.5-operator"
api_login
before="$(api GET /api/v1/config)"
before_revision="$(echo "$before" | python3 -c 'import json,sys; print(json.load(sys.stdin)["revision"])')"
snap="$(api POST /api/v1/snapshots)"
id="$(echo "$snap" | python3 -c 'import json,sys; print(json.load(sys.stdin)["snapshot"]["id"])' 2>/dev/null)"
require "snapshot created" test -n "$id"

traffic_pid="$(lan "rm -f /tmp/snapshot-traffic.log; nohup sh -c 'i=0; while [ \$i -lt 40 ]; do curl -s --max-time 3 http://$SIM_INET/marker.txt >>/tmp/snapshot-traffic.log || echo FAIL >>/tmp/snapshot-traffic.log; i=\$((i+1)); sleep 1; done' >/dev/null 2>&1 & echo \$!" | tr -d '\r\n')"
require "background traffic started" sh -c "case '$traffic_pid' in (*[!0-9]*|'') exit 1;; esac"
require "snapshot restore accepted during traffic" api POST "/api/v1/snapshots/$id/restore"
require "snapshot restore confirmed" confirm_pending
require "traffic loop completed" retry 60 lan "! kill -0 $traffic_pid 2>/dev/null"

phase "7-recovery"
check "all active-flow requests succeeded" lan "test \"\$(grep -c torture-lab /tmp/snapshot-traffic.log)\" -eq 40 && ! grep -q FAIL /tmp/snapshot-traffic.log"
check "routerd alive after restore" mr "rc-service routerd status | grep -q started"
after_revision="$(api GET /api/v1/config | python3 -c 'import json,sys; print(json.load(sys.stdin)["revision"])')"
check "restore produced a newer canonical revision" test "$after_revision" -gt "$before_revision"
check "internet still works" check_lan_internet
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
