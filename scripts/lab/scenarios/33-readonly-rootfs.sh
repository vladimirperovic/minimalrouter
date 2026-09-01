#!/bin/sh
# 33 — Read-only root rejects persistence while in-memory routing keeps
# serving; the root filesystem is remounted read-write on every exit path.
. "$(dirname "$0")/../lib.sh"
begin "33-readonly-rootfs"
phase "3-fault"
require "fault: none (filesystem stress)" ispfault status
restore_rw() { mr "mount -o remount,rw /" >/dev/null 2>&1 || true; }
root_remount() {
  mode="$1"
  mr "mount -o remount,$mode /; mount_rc=\$?; root_line=\$(awk '\$2==\"/\" {print; exit}' /proc/mounts); printf 'remount=$mode mount_rc=%s root_mount=%s\\n' \"\$mount_rc\" \"\$root_line\"; [ \"\$mount_rc\" -eq 0 ] && printf '%s\\n' \"\$root_line\" | awk -v want='$mode' '\$4 ~ \"(^|,)\" want \"(,|$)\" {ok=1} END {exit !ok}'"
}
trap restore_rw EXIT HUP INT TERM
phase "4-mr-runtime"
check "MR up before readonly" mr "uptime -s | grep -q ."
phase "4.5-operator"
api_login
cfg="$(api GET /api/v1/config)"
revision="$(echo "$cfg" | python3 -c 'import json,sys; print(json.load(sys.stdin)["revision"])')"
if remount_detail="$(root_remount ro)"; then
  note "root remount detail: $remount_detail"
else
  note "root remount detail: $remount_detail"
  require "remount root read-only" false
fi
phase "4-mr-runtime-2"
code="$(api_status PUT /api/v1/config "$cfg")"
check "config write fails closed on read-only root" sh -c "case '$code' in 500|503|507) exit 0;; *) exit 1;; esac"
after="$(api GET /api/v1/config | python3 -c 'import json,sys; print(json.load(sys.stdin)["revision"])' 2>/dev/null)"
check "failed write leaves revision unchanged" test "$after" = "$revision"
check "runtime traffic survives read-only root" check_lan_internet
check "firewall still policy-drop" check_fw_not_fail_open
check "routerd still alive" mr "rc-service routerd status | grep -q started"
phase "4.5-cleanup"
if remount_detail="$(root_remount rw)"; then
  note "root remount detail: $remount_detail"
else
  note "root remount detail: $remount_detail"
  require "remount root read-write" false
fi
trap - EXIT HUP INT TERM
require "save succeeds after remount" save_config
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
