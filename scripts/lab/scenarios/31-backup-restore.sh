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
original_lease="$(echo "$cfg" | python3 -c 'import json,sys; print(json.load(sys.stdin)["dhcp"]["lease_time"])')"
cleanup_backup_test() {
  lan "rm -f /tmp/lab-backup-cookie.txt /tmp/lab-backup.mrbak" >/dev/null 2>&1 || true
  current="$(api GET /api/v1/config 2>/dev/null || true)"
  [ -n "$current" ] || return 0
  restored="$(echo "$current" | python3 -c "import json,sys; c=json.load(sys.stdin); c['dhcp']['lease_time']='$original_lease'; print(json.dumps(c))")"
  api PUT /api/v1/config "$restored" >/dev/null 2>&1 && confirm_pending >/dev/null 2>&1 || true
}
trap cleanup_backup_test EXIT HUP INT TERM
BACKUP_PASSPHRASE="MinimalRouter-Lab-Backup!2026"
backup="$(api POST /api/v1/backup/export "{\"current_password\":\"$ADMIN_PW\",\"backup_passphrase\":\"$BACKUP_PASSPHRASE\"}")"
printf '%s' "$backup" > /tmp/lab-backup.bin
require "encrypted backup is non-empty" test -s /tmp/lab-backup.bin
require "copy encrypted backup to LAN client" lan_put /tmp/lab-backup.bin /tmp/lab-backup.mrbak
lan_login="$(lan "rm -f /tmp/lab-backup-cookie.txt; curl -sk --max-time 10 -c /tmp/lab-backup-cookie.txt -X POST $MR_API/api/v1/auth/login -H 'Content-Type: application/json' -d '{\"password\":\"$ADMIN_PW\"}'")"
lan_csrf="$(echo "$lan_login" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("csrf_token",""))' 2>/dev/null)"
require "LAN backup session issued" test -n "$lan_csrf"

phase "4-mr-runtime-2"
# mutate: flip lease_time (safe, reversible change)
new="$(python3 - <<PYEOF
import json
c=json.load(open('/tmp/lab-backup-before.json'))
c['dhcp']['lease_time']='20m' if c['dhcp'].get('lease_time')!='20m' else '2h'
print(json.dumps(c))
PYEOF
)"
require "mutated config accepted" api PUT /api/v1/config "$new"
confirm_pending
sleep 2
mutated_lease="$(api GET /api/v1/config | python3 -c 'import json,sys; print(json.load(sys.stdin)["dhcp"]["lease_time"])' 2>/dev/null)"
check "config mutation became canonical" test "$mutated_lease" != "$(echo "$cfg" | python3 -c 'import json,sys; print(json.load(sys.stdin)["dhcp"]["lease_time"])')"
check "config mutated without drift" check_converge

phase "4.5-restore"
# Preview and apply use the same LAN session because import IDs are bound to
# the authenticated session that created them.
preview="$(lan "curl -sk --fail-with-body --max-time 60 -b /tmp/lab-backup-cookie.txt -H 'X-CSRF-Token: $lan_csrf' -F 'current_password=$ADMIN_PW' -F 'backup_passphrase=$BACKUP_PASSPHRASE' -F 'backup=@/tmp/lab-backup.mrbak;type=application/vnd.minimalrouter.backup+json' $MR_API/api/v1/backup/import/preview")"
pid="$(echo "$preview" | python3 -c 'import json,sys
try:
    d=json.load(sys.stdin)
    print(d.get("import_id",""))
except Exception:
    print("")' 2>/dev/null)"
require "backup import preview ok" test -n "$pid"
require "backup restore accepted" lan "curl -sk --fail-with-body --max-time 120 -b /tmp/lab-backup-cookie.txt -X POST -H 'X-CSRF-Token: $lan_csrf' $MR_API/api/v1/import/backup/$pid/apply"
confirm_pending
sleep 3
check "MR alive after restore" mr "uptime -s | grep -q ."

phase "7-recovery"
check "canonical + last-good converge after restore" retry 90 check_converge
restored_lease="$(api GET /api/v1/config | python3 -c 'import json,sys; print(json.load(sys.stdin)["dhcp"]["lease_time"])' 2>/dev/null)"
check "backup restored exported lease value" test "$restored_lease" = "$(echo "$cfg" | python3 -c 'import json,sys; print(json.load(sys.stdin)["dhcp"]["lease_time"])')"
trap - EXIT HUP INT TERM
check "firewall still policy-drop" check_fw_not_fail_open
check "LAN up after restore" check_lan_up
check "internet works after restore" check_lan_internet
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"

capture_state "evidence"
finish_scenario
