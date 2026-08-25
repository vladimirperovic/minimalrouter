#!/bin/sh
# Shared library for the torture-lab scenario runner.
# All functions run on the mac and drive the Proxmox host.
# Sourced by lab-run.sh and scenarios/*.sh.

HOST="${LAB_HOST:-root@proxmox.example}"
SSHOPTS="-o BatchMode=yes -o ConnectTimeout=10"
# ponytail: absolute path — $(dirname "$0") breaks when scenarios are invoked
# as `sh scenarios/xx.sh` ($0 = scenarios/xx.sh -> wrong relative base).
KEY="${LAB_SSH_KEY:-${HOME:-/root}/.ssh/lab_id_ed25519}"
KNOWN_HOSTS="${LAB_KNOWN_HOSTS:-${HOME:-/root}/.ssh/known_hosts}"
H() { ssh $SSHOPTS -i "$KEY" -o UserKnownHostsFile="$KNOWN_HOSTS" "$HOST" "$@"; }

MR_API="https://192.168.1.1:8443"
MR_LAN_IP="192.168.1.1"
MR_WAN_PPP="10.250.0.50"
SIM_INET="11.255.0.2"
ISP_DNS="10.250.0.1"
PROD_GW="192.168.1.1"
LAN_CLIENT_IF="${LAB_LAN_CLIENT_IF:-eth0}"
ADMIN_PW="${LAB_ADMIN_PW:-MinimalRouter-Lab-Test!2026}"

# --- result bookkeeping -----------------------------------------------------
# Normalize the results dir regardless of whether lib.sh is sourced from
# lab-run.sh (scripts/lab) or from a scenario (scripts/lab/scenarios):
# a scenario always resolves its parent's ../results.
case "$(basename "$(dirname "$0")")" in
  scenarios) RESULTS_DIR="${LAB_RESULTS:-$(dirname "$0")/../results}" ;;
  *) RESULTS_DIR="${LAB_RESULTS:-$(dirname "$0")/results}" ;;
esac
mkdir -p "$RESULTS_DIR"
CURRENT_SCENARIO=""
CURRENT_PHASE=""
FAILED=0

begin() { CURRENT_SCENARIO="$1"; FAILED=0; mkdir -p "$RESULTS_DIR/$1"; CUR_FILE=/tmp/lab-current.json; }
phase() { CURRENT_PHASE="$1"; log "--- phase $1"; temp_guard; echo "{\"scenario\":\"$CURRENT_SCENARIO\",\"phase\":\"$1\",\"ts\":\"$(TZ=Europe/Podgorica date +%H:%M:%S)\"}" > "${CUR_FILE:-/tmp/lab-current.json}" 2>/dev/null; }
log() { echo "[$(TZ=Europe/Podgorica date +%H:%M:%S)] $*"; }
note() { echo "[note] $*"; }

# temp_guard — abort the whole suite if the host CPU exceeds 85C
TEMP_LIMIT="${LAB_TEMP_LIMIT:-95}"
temp_guard() {
  t=$(H "cat /sys/class/thermal/thermal_zone1/temp 2>/dev/null" 2>/dev/null)
  t=${t:-0}
  if [ "$t" -gt $((TEMP_LIMIT * 1000)) ] 2>/dev/null; then
    echo "[ABORT] host CPU temp $((t / 1000))C exceeds ${TEMP_LIMIT}C — stopping suite (no permanent damage risk)"
    finish_scenario 1
  fi
}
check() {  # check <name> <cmd...> — 0 = PASS, else FAIL (recorded, non-fatal)
  name="$1"; shift
  if "$@" >/dev/null 2>&1; then
    echo "[PASS] $CURRENT_SCENARIO/$CURRENT_PHASE: $name"
  else
    echo "[FAIL] $CURRENT_SCENARIO/$CURRENT_PHASE: $name"
    FAILED=$((FAILED+1))
  fi
}
require() {  # like check but aborts the scenario on failure
  name="$1"; shift
  if "$@" >/dev/null 2>&1; then
    echo "[PASS] $CURRENT_SCENARIO/$CURRENT_PHASE: $name"
  else
    echo "[FAIL] $CURRENT_SCENARIO/$CURRENT_PHASE: $name (aborting scenario)"
    FAILED=$((FAILED+1)); finish_scenario 1
  fi
}
finish_scenario() {
  rc="${1:-$([ "$FAILED" -eq 0 ] && echo 0 || echo 1)}"
  if [ "$rc" -eq 0 ]; then
    echo "[RESULT] $CURRENT_SCENARIO: PASS"
  else
    echo "[RESULT] $CURRENT_SCENARIO: FAIL ($FAILED failed checks)"
  fi
  echo "$(TZ=Europe/Podgorica date +%Y-%m-%dT%H:%M:%S%z) rc=$rc" > "$RESULTS_DIR/$CURRENT_SCENARIO/result.txt"
  exit "$rc"
}

# --- transports -------------------------------------------------------------
# gx <vmid> <sh-command> — decoded guest exec (raw or base64 out-data) that
# propagates the guest command's real exit code (qm guest exec itself always
# exits 0 when the agent answered, so the JSON "exitcode" is the only truth).
# The guest command is base64-encoded on the host so no layer of the local
# shell chain can expand `$var`, `$(...)` or `$((...))` meant for the guest;
# host-side expansion (variables the scenario wants resolved locally) already
# happened before this function was called. A generous qm timeout keeps
# long-running fault injections (disk/inode fills) from being killed at 30s.
gx() {
  b64="$(printf '%s' "$2" | base64 -w0 2>/dev/null || printf '%s' "$2" | base64)"
  out="$(H "qm guest exec --timeout 900 $1 -- sh -c \"echo $b64 | base64 -d | sh\"" 2>/dev/null)"
  printf '%s' "$out" | python3 -c '
import json,sys,base64
try:
    d=json.load(sys.stdin)
    ret=d.get("return",d)  # new qemu-agent wraps in "return", old returns flat
    od=ret.get("out-data","")
    if od:
        try:
            sys.stdout.write(base64.b64decode(od, validate=True).decode("utf-8","replace"))
        except Exception:
            sys.stdout.write(od)
    ec=ret.get("exitcode")
    sys.exit(ec if isinstance(ec,int) else 1)
except SystemExit:
    raise
except Exception:
    sys.exit(1)'
}
# ispfault <args...> — run fault tool on ISP-LAB (guest exec runs as root)
ispfault() { gx 150 "lab-fault $* 2>&1"; }
# ispfaultroot — same but via root user (payload installed tool at /usr/local/sbin)
isp() { gx 150 "$* 2>&1"; }
sim() { gx 153 "$* 2>&1"; }
lan() { gx 154 "$* 2>&1"; }
mr() { gx 151 "$* 2>&1"; }

# api <method> <path> [data] — MR-TEST API via host curl
API_COOKIE="/tmp/lab-runner-cookie.txt"
API_CSRF="/tmp/lab-runner-csrf.txt"
api() {
  method="$1"; path="$2"; data="${3:-}"
  csrf=""
  [ -f "$API_CSRF" ] && csrf="$(cat "$API_CSRF" 2>/dev/null)"
  hdr=""
  [ -n "$csrf" ] && hdr="-H 'X-CSRF-Token: $csrf'"
  if [ -n "$data" ]; then
    H "curl -sk --max-time 120 -b $API_COOKIE -X $method $hdr -H 'Content-Type: application/json' -d '$data' $MR_API$path" 2>/dev/null
  else
    H "curl -sk --max-time 60 -b $API_COOKIE -X $method $hdr $MR_API$path" 2>/dev/null
  fi
}
api_login() {
  # Reuse the existing session while the cookie still works: the API rate
  # limits logins to 5/min per source IP, and every check calls api_login.
  # The cookie lives on the remote host (api/H run there), so probe remotely.
  if H "curl -sk --max-time 10 -b $API_COOKIE -o /dev/null -w '%{http_code}' $MR_API/api/v1/config" 2>/dev/null | grep -q '^200$'; then
    return 0
  fi
  H "curl -sk --max-time 10 -c $API_COOKIE -X POST $MR_API/api/v1/auth/login -H 'Content-Type: application/json' -d '{\"password\": \"$ADMIN_PW\"}'" 2>/dev/null | python3 -c 'import json,sys; print(json.load(sys.stdin).get("csrf_token",""))' > "$API_CSRF" 2>/dev/null || true
}
# config_py_assert <python-snippet> — pass when the snippet (evaluated with
# `c` bound to the current config JSON, json module imported) exits 0. Runs in
# this shell so api/config helpers stay visible.
config_py_assert() {
  api_login
  api GET /api/v1/config | python3 -c 'import json,sys
c=json.load(sys.stdin)
'"$1"'
' 2>/dev/null
}
# api_reconcile — trigger recovery reconcile (POST /api/v1/recovery/reconcile);
# pass only when the API confirms success (curl's own exit code ignores HTTP).
api_reconcile() {
  api_login
  body="$(api POST /api/v1/recovery/reconcile)"
  echo "$body" | grep -q '"success"' && echo "$body" | grep -q 'true'
}

# --- invariant checks -------------------------------------------------------
# Firewall must never be fail-open: input/forward/output policy drop.
check_fw_not_fail_open() {
  n=$(mr 'nft list ruleset 2>/dev/null' | grep -cE "type filter hook (input|forward|output).*policy drop")
  [ "$n" -ge 3 ]
}
# LAN reachable from host and from LAN client; DHCP lease present.
check_lan_up() {
  H "ping -c1 -W2 192.168.1.1 >/dev/null 2>&1" && \
  lan 'ip -4 -o addr show' | grep -q "192.168.1."
}
lan_client_ipv4() {
  lan "ip -4 -o addr show '$LAN_CLIENT_IF' | awk '\$4 ~ /^192\\.168\\.1\\./ {sub(/\\/.*/, \"\", \$4); print \$4; exit}'" | tr -d '\r\n'
}
lan_dhcp_renew() {
  lan "ip -4 addr flush dev '$LAN_CLIENT_IF'; dhclient '$LAN_CLIENT_IF' >/dev/null 2>&1" || return 1
  retry 30 lan "ip -4 -o addr show '$LAN_CLIENT_IF' | grep -q '192.168.1.'"
}
# Local DNS records resolve through MR dnsmasq even with WAN down.
check_local_dns() {
  lan 'host router.home.arpa 192.168.1.1 2>/dev/null' | grep -q "192.168.1.1"
}
# PPPoE session up with the fixed lab address.
check_pppoe() {
  mr "ip -4 -o addr show ppp0 2>/dev/null" | grep -q "$MR_WAN_PPP"
}
# At least one configured peer must have completed a recent real handshake.
check_wg_recent() {
  interface="$1"
  max_age="${2:-90}"
  latest="$(mr "wg show '$interface' latest-handshakes 2>/dev/null" | awk '$2 ~ /^[0-9]+$/ && $2 > latest {latest = $2} END {if (latest > 0) print latest}')"
  [ -n "$latest" ] || return 1
  now="$(date +%s)"
  [ "$latest" -le "$now" ] && [ $((now - latest)) -le "$max_age" ]
}
# Health API reports the DNS/DHCP check (loopback is firewalled off, so query
# the authenticated LAN endpoint like the dashboard does).
check_health_reports_dns() {
  api_login
  api GET /api/v1/health 2>/dev/null | grep -q dns_dhcp
}
# LAN client reaches the simulated internet through NAT.
check_lan_internet() {
  lan "curl -s --max-time 6 http://$SIM_INET/marker.txt 2>/dev/null" | grep -q torture-lab
}
# Canonical SQLite and helper last-good converge on the same revision.
check_converge() {
  api_login
  canon="$(api GET /api/v1/config | python3 -c 'import json,sys; print(json.load(sys.stdin)["revision"])' 2>/dev/null)"
  lastgood="$(mr 'cat /var/lib/minimalrouter-applyd/last-good.json 2>/dev/null' | python3 -c 'import json,sys; print(json.load(sys.stdin)["revision"])' 2>/dev/null)"
  [ -n "$canon" ] && [ "$canon" = "$lastgood" ]
}
# Runtime after recovery must not be a hybrid: LAN addr + PPP user + dnsmasq
# range all match the canonical config.
check_runtime_not_hybrid() {
  mr 'ip -4 -o addr show eth1 2>/dev/null' | grep -q "192.168.1.1/" && \
  mr 'grep mr-test /etc/ppp/chap-secrets 2>/dev/null' | grep -q mr-test && \
  mr 'ip -4 -o addr show ppp0 2>/dev/null' | grep -q "10.250.0.50" && \
  mr 'grep -r "192.168.1.100" /etc/dnsmasq* 2>/dev/null' | grep -q 192.168.1.100
}
# No MR-TEST fault may affect production: pfSense reachable + bridge ports unchanged.
check_prod_untouched() {
  PROD_PORTS_BEFORE="$1"
  H "ping -c1 -W2 $PROD_GW >/dev/null 2>&1" && \
  H "bridge link show vmbr0 2>/dev/null | grep -oE 'ifindex [0-9]+' | md5sum" | grep -q "^$PROD_PORTS_BEFORE"
}
prod_ports_md5() { H "bridge link show vmbr0 2>/dev/null | grep -oE 'ifindex [0-9]+' | md5sum" | awk '{print $1}'; }

# --- helpers ----------------------------------------------------------------
# mr_save <json-fragment-pairs...> — save a trivial local change via API
mr_save_lease() {  # toggles lease_time; returns canonical revision
  api_login
  confirm_pending
  cfg="$(api GET /api/v1/config)"
  rev="$(echo "$cfg" | python3 -c 'import json,sys; print(json.load(sys.stdin)["revision"])' 2>/dev/null)" || rev=""
  [ -n "$rev" ] || return 1
  cur="$(echo "$cfg" | python3 -c 'import json,sys; print(json.load(sys.stdin)["dhcp"]["lease_time"])')"
  new="$(echo "$cur" | grep -q 12h && echo 2h || echo 12h)"
  api PUT /api/v1/config "$(echo "$cfg" | python3 -c "
import json,sys
c=json.load(sys.stdin)
c['dhcp']['lease_time']='$new'
print(json.dumps(c))")" >/dev/null 2>&1
  confirm_pending
  mr_env_restore
}

# wait_pppoe <seconds> — poll until ppp0 has the lab address
wait_pppoe() { t="${1:-90}"; i=0; while [ $i -lt "$t" ]; do check_pppoe && return 0; sleep 3; i=$((i+3)); done; return 1; }
wait_pppoe_down() { t="${1:-30}"; i=0; while [ $i -lt "$t" ]; do check_pppoe || return 0; sleep 3; i=$((i+3)); done; return 1; }
# wait_pppoe_ip <ip> [timeout] — poll until ppp0 carries the given address
# (used when a scenario deliberately renumbers the WAN away from MR_WAN_PPP).
wait_pppoe_ip() { want="$1"; t="${2:-90}"; i=0; while [ $i -lt "$t" ]; do mr "ip -4 -o addr show ppp0 2>/dev/null" | grep -q "$want" && return 0; sleep 3; i=$((i+3)); done; return 1; }

# snapshots of runtime state for post-mortem
capture_state() {  # capture_state <label>
  lbl="$1"
  {
    echo "--- mr runtime ($(date)) ---"
    mr 'ip -brief addr; echo; ip -brief link; echo; ip route; echo; nft list ruleset 2>/dev/null | head -60'
    mr 'rc-service routerd status; rc-service router-applyd status; rc-service pppoe-wan status 2>/dev/null; wg show 2>/dev/null'
    mr 'tail -30 /var/log/routerd.log 2>/dev/null; tail -30 /var/log/router-applyd.log 2>/dev/null'
    echo "--- isp fault state ---"
    ispfault status
    isp 'ip -4 -brief addr show ppp0 2>/dev/null; ip -4 route show; nft list ruleset 2>/dev/null; tail -40 /var/log/pppoe-server.log 2>/dev/null'
    echo "--- simulator return path ---"
    sim 'ip -4 route show; ip neigh show dev eth1; ping -c1 -W2 10.250.0.1 2>&1; ping -c1 -W2 10.250.0.50 2>&1'
    echo "--- lan client ---"
    lan 'ip -4 -o addr show; ip route; curl -v --max-time 6 http://11.255.0.2/marker.txt 2>&1; tail -5 /var/log/syslog 2>/dev/null'
  } > "$RESULTS_DIR/$CURRENT_SCENARIO/$lbl.txt" 2>/dev/null || true
}

# arm_hook <phase> <command>  — arm a fault hook on MR-TEST
# routerd reads the hook dir as an unprivileged user, so the dir must be
# world-readable and hook files 0644 (root-created 0700 files are invisible).
# The command is stored verbatim: $ and backticks are escaped so nothing is
# evaluated when the hook file is written, only when the hook runs.
arm_hook() {
  cmd="$(printf '%s' "$2" | sed 's/[$`]/\\&/g')"
  mr "mkdir -p /run/minimalrouter-fault && chmod 0755 /run/minimalrouter-fault && printf '%s' '$cmd' > /run/minimalrouter-fault/$1 && chmod 0644 /run/minimalrouter-fault/$1 && cat /run/minimalrouter-fault/$1" >/dev/null
}
disarm_hooks() { mr 'rm -rf /run/minimalrouter-fault 2>/dev/null; true' >/dev/null; }

# mr_env_restore — restore lab-only routing quirks on MR-TEST that the
# pristine image / applyd wipes: the host route for the Proxmox host
# (192.168.1.254) must leave via eth1, and the WAN interface (eth0) must not
# carry the stale 192.168.1.1/24 LAN address (else LAN client replies egress
# the wrong NIC). Call after every API-affecting step (apply/confirm/save).
mr_env_restore() {
  mr "ip route add 192.168.1.254/32 dev eth1 2>/dev/null || true; ip -4 addr del 192.168.1.1/24 dev eth0 2>/dev/null || true; true" >/dev/null 2>&1
}

# --- retry / wait helpers ----------------------------------------------------
# retry <secs> <cmd...> — run the command (functions visible) until success
retry() { t="$1"; shift; i=0; while [ $i -lt "$t" ]; do "$@" && return 0; sleep 5; i=$((i+5)); done; return 1; }
# mr_wait <secs> — poll until the MR-TEST guest agent answers
mr_wait() { t="${1:-120}"; i=0; while [ $i -lt "$t" ]; do [ "$(mr 'echo ok' 2>/dev/null)" = "ok" ] && return 0; sleep 5; i=$((i+5)); done; return 1; }
# wait_vm_stopped <vmid> <secs> / wait_vm_running
wait_vm_stopped() { t="${2:-120}"; i=0; while [ $i -lt "$t" ]; do H "qm status $1" 2>/dev/null | grep -qi stopped && return 0; sleep 5; i=$((i+5)); done; return 1; }
wait_vm_running() { t="${2:-120}"; i=0; while [ $i -lt "$t" ]; do H "qm status $1" 2>/dev/null | grep -qi running && return 0; sleep 5; i=$((i+5)); done; return 1; }
# mr_put <local-file> <remote-path> [mode] — chunked base64 push via the guest agent
mr_put() {
  src="$1"; dst="$2"; mode="${3:-0755}"
  size=$(wc -c < "$src") || return 1
  mr "rm -f '$dst'" >/dev/null 2>&1
  pos=0
  while [ $pos -lt "$size" ]; do
    chunk=$(dd if="$src" bs=1 skip=$pos count=60000 2>/dev/null | base64 | tr -d '\n')
    mr "echo '$chunk' | base64 -d >> '$dst'" >/dev/null || return 1
    pos=$((pos+60000))
  done
  mr "test -s '$dst' && chmod $mode '$dst'"
}

# --- config save helpers -----------------------------------------------------
# confirm_pending — confirm any pending (awaiting-confirmation) transaction so
# the canonical revision and helper last-good converge. No-op when clean.
confirm_pending() {
  api_login
  id="$(api GET /api/v1/transactions/pending | python3 -c 'import json,sys
try:
    d=json.load(sys.stdin)
    print(d.get("id","") if d.get("pending") else "")
except Exception:
    print("")' 2>/dev/null)"
  if [ -n "$id" ]; then
    api POST "/api/v1/transactions/$id/confirm" >/dev/null 2>&1
  fi
}
# save_config — GET current config and PUT it back (exercises the full save
# path; pending transactions are confirmed)
save_config() {
  api_login
  cfg="$(api GET /api/v1/config)" || return 1
  confirm_pending
  api PUT /api/v1/config "$cfg" >/dev/null 2>&1
  confirm_pending
  mr_env_restore
}
# patch_config <python-snippet> — load config, eval snippet against `c`, PUT
patch_config() {
  api_login
  cfg="$(api GET /api/v1/config)" || return 1
  new="$(echo "$cfg" | python3 -c "
import json,sys
c=json.load(sys.stdin)
$1
print(json.dumps(c))")" || return 1
  api PUT /api/v1/config "$new" >/dev/null 2>&1
  mr_env_restore
}
# patch_config_reject <python-snippet> <expected-error-substring> — like
# patch_config but asserts the API REJECTS the change (HTTP 422) with the
# given error text. Used where the product deliberately blocks self-lockout
# changes (live LAN interface/subnet swaps) as a safety guard.
patch_config_reject() {
  api_login
  cfg="$(api GET /api/v1/config)" || return 1
  new="$(echo "$cfg" | python3 -c "
import json,sys
c=json.load(sys.stdin)
$1
print(json.dumps(c))")" || return 1
  csrf=""
  [ -f "$API_CSRF" ] && csrf="$(cat "$API_CSRF" 2>/dev/null)"
  hdr=""
  [ -n "$csrf" ] && hdr="-H 'X-CSRF-Token: $csrf'"
  resp="$(H "curl -sk --max-time 60 -b $API_COOKIE -w '|%{http_code}' -X PUT $hdr -H 'Content-Type: application/json' -d '$new' $MR_API/api/v1/config" 2>/dev/null)"
  mr_env_restore
  code="${resp##*|}"
  body="${resp%|*}"
  [ "$code" = "422" ] || { echo "  expected HTTP 422, got $code: $(echo "$body" | head -c 200)"; return 1; }
  case "$body" in
    *"$2"*) return 0 ;;
    *) echo "  expected rejection text '$2' not in: $(echo "$body" | head -c 200)"; return 1 ;;
  esac
}
# check_not <name> <cmd...> — passes when the command fails
check_not() {
  name="$1"; shift
  if "$@" >/dev/null 2>&1; then
    echo "[FAIL] $CURRENT_SCENARIO/$CURRENT_PHASE: $name (unexpected success)"
    FAILED=$((FAILED+1))
  else
    echo "[PASS] $CURRENT_SCENARIO/$CURRENT_PHASE: $name"
  fi
}
# save_expects_error <json> — PUT the given config; passes only when the full
# save path is rejected: either the PUT is rejected outright (JSON error body
# or HTTP 422), or the PUT entered the two-phase confirmation path and the
# confirm itself failed (e.g. ENOSPC on the helper's last-good write).
save_expects_error() {
  api_login
  body="$(api PUT /api/v1/config "$1")"
  # a broken/unauthorized session is a harness failure, not the expected
  # product rejection: never false-pass on it
  if echo "$body" | grep -qi 'unauthorized'; then
    return 1
  fi
  if echo "$body" | grep -qE '"error"|"status"[[:space:]]*:[[:space:]]*"(error|failed|rejected)"|422'; then
    return 0
  fi
  state="$(echo "$body" | python3 -c 'import json,sys
try: print(json.load(sys.stdin).get("state",""))
except Exception: print("")' 2>/dev/null)"
  [ "$state" = "AwaitingConfirmation" ] || return 1
  id="$(echo "$body" | python3 -c 'import json,sys
try: print(json.load(sys.stdin).get("id",""))
except Exception: print("")' 2>/dev/null)"
  [ -n "$id" ] || return 1
  csrf=""
  [ -f "$API_CSRF" ] && csrf="$(cat "$API_CSRF" 2>/dev/null)"
  hdr=""
  [ -n "$csrf" ] && hdr="-H 'X-CSRF-Token: $csrf'"
  resp="$(H "curl -sk --max-time 60 -b $API_COOKIE -w '|%{http_code}' -X POST $hdr $MR_API/api/v1/transactions/$id/confirm" 2>/dev/null)"
  code="${resp##*|}"
  [ "$code" != "200" ] && [ "$code" != "202" ]
}
