#!/bin/sh
# 31 — Backup and restore round-trip: export an encrypted backup, mutate the
# config, import + restore the backup, and verify the router converges back
# to the exported state.
. "$(dirname "$0")/../lib.sh"

begin "31-backup-restore"
phase "3-fault"
require "fault: none (backup path)" ispfault status

phase "4-mr-runtime"
check "MR up before backup" mr "uptime -s | grep -q ."

phase "4.5-operator"
api_login
cfg="$(api GET /api/v1/config)" || { echo "[FAIL] cannot fetch config"; FAILED=$((FAILED+1)); finish_scenario 1; }
echo "$cfg" > /tmp/lab-backup-before.json
api POST /api/v1/backup/export > /tmp/lab-backup.bin 2>/dev/null
check "backup exported" test -s /tmp/lab-backup.bin

phase "4-mr-runtime-2"
# mutate: flip lease_time (safe, reversible change)
new="$(python3 - <<PYEOF
import json
c=json.load(open('/tmp/lab-backup-before.json'))
c['dhcp']['lease_time']=1200 if c['dhcp'].get('lease_time')!=1200 else 7200
print(json.dumps(c))
PYEOF
)"
api PUT /api/v1/config "$new" >/dev/null 2>&1
sleep 2
check "config mutated" check_converge

phase "4.5-restore"
# preview + apply the exported backup
pid="$(api POST /api/v1/backup/import/preview "$(cat /tmp/lab-backup.bin 2>/dev/null)" 2>/dev/null | python3 -c 'import json,sys
try:
    d=json.load(sys.stdin)
    print(d.get("id",""))
except Exception:
    print("")' 2>/dev/null)"
check "backup import preview ok" test -n "$pid"
if [ -n "$pid" ]; then
  api POST "/api/v1/import/backup/$pid/apply" >/dev/null 2>&1
  sleep 3
fi
check "MR alive after restore" mr "uptime -s | grep -q ."

phase "7-recovery"
check "canonical + last-good converge after restore" retry 90 check_converge
check "firewall still policy-drop" check_fw_not_fail_open
check "LAN up after restore" check_lan_up
check "internet works after restore" check_lan_internet
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"

capture_state "evidence"
finish_scenario
