#!/bin/sh
# Shared library for the torture-lab scenario runner.
# All functions run on the mac and drive the Proxmox host.
# Sourced by lab-run.sh and scenarios/*.sh.

HOST="${LAB_HOST:-root@192.168.1.2}"
SSHOPTS="-o BatchMode=yes -o ConnectTimeout=10"
MR_VMID="${LAB_MR_VMID:-151}"
ISP_VMID="${LAB_ISP_VMID:-150}"
SIM_VMID="${LAB_SIM_VMID:-153}"
LAN_VMID="${LAB_LAN_VMID:-154}"
# ponytail: absolute path — $(dirname "$0") breaks when scenarios are invoked
# as `sh scenarios/xx.sh` ($0 = scenarios/xx.sh -> wrong relative base).
KEY="${LAB_SSH_KEY:-${HOME:-/root}/.ssh/lab_id_ed25519}"
[ -f "$KEY" ] || KEY="${LAB_SSH_KEY:-$HOME/Documents/minimalrouter/private/secrets/proxmox_codex_ed25519}"
KNOWN_HOSTS="${LAB_KNOWN_HOSTS:-${HOME:-/root}/.ssh/known_hosts}"
[ -f "$KNOWN_HOSTS" ] || KNOWN_HOSTS="${LAB_KNOWN_HOSTS:-$HOME/Documents/minimalrouter/private/secrets/proxmox_known_hosts}"
H() { ssh $SSHOPTS -i "$KEY" -o UserKnownHostsFile="$KNOWN_HOSTS" "$HOST" "$@"; }

MR_API="https://192.168.1.1:8443"
MR_LAN_IP="192.168.1.1"
MR_WAN_PPP="${LAB_MR_WAN_PPP:-10.250.0.50}"
MR_WAN_IF="${LAB_MR_WAN_IF:-eth0}"
MR_LAN_IF="${LAB_MR_LAN_IF:-eth1}"
LAN_CLIENT_IF="${LAB_LAN_IF:-eth0}"
SIM_INET="11.255.0.2"
ISP_DNS="10.250.0.1"
PROD_GW="192.168.1.1"
ADMIN_PW="${LAB_ADMIN_PW:-MinimalRouter-Lab-Test!2026}"

# --- result bookkeeping -----------------------------------------------------
if [ -n "${LAB_RESULTS:-}" ]; then
  RESULTS_DIR="$LAB_RESULTS"
else
  caller_dir="$(cd "$(dirname "$0")" && pwd)"
  [ "$(basename "$caller_dir")" != "scenarios" ] || caller_dir="$(dirname "$caller_dir")"
  RESULTS_DIR="$caller_dir/results"
fi
CURRENT_SCENARIO=""
CURRENT_PHASE=""
FAILED=0
mkdir -p "$RESULTS_DIR"

begin() { CURRENT_SCENARIO="$1"; FAILED=0; mkdir -p "$RESULTS_DIR/$1"; }
phase() { CURRENT_PHASE="$1"; log "--- phase $1"; }
log() { echo "[$(date +%H:%M:%S)] $*"; }
note() { echo "[note] $*"; }
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
  echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) rc=$rc" > "$RESULTS_DIR/$CURRENT_SCENARIO/result.txt"
  exit "$rc"
}

# --- transports -------------------------------------------------------------
# gx <vmid> <sh-command> — decoded guest exec (raw or base64 out-data) that
# propagates the guest command's real exit code (qm guest exec itself always
# exits 0 when the agent answered, so the JSON "exitcode" is the only truth).
gx() {
  vmid="$1"
  encoded="$(printf '%s' "$2" | base64 | tr -d '\n')"
  out="$(H "qm guest exec $vmid -- sh -c \"echo '$encoded' | base64 -d | sh\"" 2>/dev/null)"
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
ispfault() { gx "$ISP_VMID" "lab-fault $* 2>&1"; }
# ispfaultroot — same but via root user (payload installed tool at /usr/local/sbin)
isp() { gx "$ISP_VMID" "$* 2>&1"; }
sim() { gx "$SIM_VMID" "$* 2>&1"; }
lan() { gx "$LAN_VMID" "$* 2>&1"; }
mr() { gx "$MR_VMID" "$* 2>&1"; }

# api <method> <path> [data] — MR-TEST API from the isolated LAN client.
# Never curl MR_LAN_IP from the Proxmox host: 192.168.1.1 on vmbr0 is the
# production router, while the identical lab address exists only on
# vmbr-lab-lan and is reachable from LAN_VMID.
API_COOKIE="/tmp/lab-runner-cookie.txt"
API_CSRF="/tmp/lab-runner-csrf.txt"
api() {
  method="$1"; path="$2"; data="${3:-}"
  csrf=""
  [ -f "$API_CSRF" ] && csrf="$(cat "$API_CSRF" 2>/dev/null)"
  hdr=""
  [ -n "$csrf" ] && hdr="-H 'X-CSRF-Token: $csrf'"
  if [ -n "$data" ]; then
    body_b64="$(printf '%s' "$data" | base64 | tr -d '\n')"
    lan "echo '$body_b64' | base64 -d | curl -sk --fail-with-body --max-time 120 -b $API_COOKIE -X $method $hdr -H 'Content-Type: application/json' --data-binary @- $MR_API$path" 2>/dev/null
  else
    lan "curl -sk --fail-with-body --max-time 60 -b $API_COOKIE -X $method $hdr $MR_API$path" 2>/dev/null
  fi
}
api_login() {
  # Reuse a still-valid lab session so large scenario batches do not consume
  # the five-login-per-minute security budget between every script.
  existing="$(lan "curl -sk --max-time 10 -b $API_COOKIE $MR_API/api/v1/auth/session" 2>/dev/null || true)"
  existing_csrf="$(echo "$existing" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("csrf_token", ""))' 2>/dev/null || true)"
  existing_read_only="$(echo "$existing" | python3 -c 'import json,sys; print(str(json.load(sys.stdin).get("read_only", True)).lower())' 2>/dev/null || true)"
  if [ -n "$existing_csrf" ] && [ "$existing_read_only" = "false" ]; then
    printf '%s\n' "$existing_csrf" > "$API_CSRF"
    return 0
  fi
  lan "curl -sk --max-time 10 -c $API_COOKIE -X POST $MR_API/api/v1/auth/login -H 'Content-Type: application/json' -d '{\"password\": \"$ADMIN_PW\"}'" 2>/dev/null | python3 -c 'import json,sys; print(json.load(sys.stdin).get("csrf_token",""))' > "$API_CSRF" 2>/dev/null || true
  # The API caps logins at 5/min per source IP. When the quota is exhausted
  # the login response carries no csrf_token; wait out the window and retry
  # once so a scenario batch is never wedged by a 429.
  if [ ! -s "$API_CSRF" ]; then
    waited=0
    while [ "$waited" -lt 65 ]; do
      sleep 5
      waited=$((waited+5))
    done
    lan "curl -sk --max-time 10 -c $API_COOKIE -X POST $MR_API/api/v1/auth/login -H 'Content-Type: application/json' -d '{\"password\": \"$ADMIN_PW\"}'" 2>/dev/null | python3 -c 'import json,sys; print(json.load(sys.stdin).get("csrf_token",""))' > "$API_CSRF" 2>/dev/null || true
  fi
  [ -s "$API_CSRF" ]
}

# api_status <method> <path> [data] [content-type] — authenticated request
# that prints only the HTTP status code. Useful for negative-path scenarios
# where the response body is intentionally not JSON.
api_status() {
  method="$1"; path="$2"; data="${3:-}"; content_type="${4:-application/json}"
  csrf=""
  [ -f "$API_CSRF" ] && csrf="$(cat "$API_CSRF" 2>/dev/null)"
  hdr=""
  [ -n "$csrf" ] && hdr="-H 'X-CSRF-Token: $csrf'"
  if [ -n "$data" ]; then
    body_b64="$(printf '%s' "$data" | base64 | tr -d '\n')"
    lan "echo '$body_b64' | base64 -d | curl -sk --max-time 30 -o /dev/null -w '%{http_code}' -b $API_COOKIE -X $method $hdr -H 'Content-Type: $content_type' --data-binary @- $MR_API$path" 2>/dev/null
  else
    lan "curl -sk --max-time 30 -o /dev/null -w '%{http_code}' -b $API_COOKIE -X $method $hdr $MR_API$path" 2>/dev/null
  fi
}

# api_unauth_status <method> <path> [data] — request without the lab cookie.
api_unauth_status() {
  method="$1"; path="$2"; data="${3:-}"
  if [ -n "$data" ]; then
    body_b64="$(printf '%s' "$data" | base64 | tr -d '\n')"
    lan "echo '$body_b64' | base64 -d | curl -sk --max-time 30 -o /dev/null -w '%{http_code}' -X $method -H 'Content-Type: application/json' --data-binary @- $MR_API$path" 2>/dev/null
  else
    lan "curl -sk --max-time 30 -o /dev/null -w '%{http_code}' -X $method $MR_API$path" 2>/dev/null
  fi
}

# --- invariant checks -------------------------------------------------------
# Firewall must never be fail-open: input/forward/output policy drop.
check_fw_not_fail_open() {
  n=$(mr 'nft list ruleset 2>/dev/null' | grep -cE "type filter hook (input|forward|output).*policy drop")
  [ "$n" -ge 3 ]
}
# LAN reachable from host and from LAN client; DHCP lease present.
check_lan_up() {
  lan "ping -c1 -W2 $MR_LAN_IP >/dev/null 2>&1" && \
  lan "ip -4 -o addr show '$LAN_CLIENT_IF'" | grep -q "192.168.1."
}
# Local DNS records resolve through MR dnsmasq even with WAN down.
check_local_dns() {
  lan 'host router.home.arpa 192.168.1.1 2>/dev/null' | grep -q "192.168.1.1"
}
# PPPoE session up with the fixed lab address.
check_pppoe() {
  mr "ip -4 -o addr show ppp0 2>/dev/null" | grep -q "$MR_WAN_PPP"
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
# Runtime after recovery must not be a hybrid: LAN address, PPP user/address,
# and both dnsmasq pool endpoints must match the canonical configuration.
check_runtime_not_hybrid() {
  api_login || return 1
  cfg="$(api GET /api/v1/config)" || return 1
  lan_if="$(echo "$cfg" | python3 -c 'import json,sys; print(json.load(sys.stdin)["lan"]["interface"])')" || return 1
  lan_ip="$(echo "$cfg" | python3 -c 'import json,sys; print(json.load(sys.stdin)["lan"]["ip_address"])')" || return 1
  ppp_user="$(echo "$cfg" | python3 -c 'import json,sys; print(json.load(sys.stdin)["wan"]["username"])')" || return 1
  pool_start="$(echo "$cfg" | python3 -c 'import json,sys; print(json.load(sys.stdin)["dhcp"]["range_start"])')" || return 1
  pool_end="$(echo "$cfg" | python3 -c 'import json,sys; print(json.load(sys.stdin)["dhcp"]["range_end"])')" || return 1
  mr "ip -4 -o addr show '$lan_if' 2>/dev/null" | grep -Fq "$lan_ip/" && \
  mr "grep -F '$ppp_user' /etc/ppp/chap-secrets 2>/dev/null" | grep -Fq "$ppp_user" && \
  mr 'ip -4 -o addr show ppp0 2>/dev/null' | grep -Fq "$MR_WAN_PPP" && \
  mr 'grep -r "dhcp-range" /etc/dnsmasq.d/minimalrouter*.conf /etc/dnsmasq* 2>/dev/null' | grep -F "$pool_start" | grep -Fq "$pool_end"
}
# No MR-TEST fault may affect production: pfSense reachable + bridge ports unchanged.
check_prod_untouched() {
  PROD_PORTS_BEFORE="$1"
  H "ping -c1 -W2 $PROD_GW >/dev/null 2>&1" && \
  [ "$(prod_ports_fingerprint)" = "$PROD_PORTS_BEFORE" ]
}
prod_ports_fingerprint() {
  H "ip -o link show master vmbr0 2>/dev/null | awk -F': ' '{n=\$2; sub(/@.*/,\"\",n); print n}' | sort | sha256sum" | awk '{print $1}'
}

# Refuse to inject any fault unless every active target is the documented lab
# VM on an isolated vmbr-lab-* segment. VM 106 and vmbr0 are production and
# are intentionally absent from all mutation paths.
assert_lab_topology() {
  case " $MR_VMID $ISP_VMID $SIM_VMID $LAN_VMID " in
    *" 106 "*) echo "REFUSED: production pfSense VM 106 selected as a lab target" >&2; return 1 ;;
  esac
  [ "$MR_VMID" != "$ISP_VMID" ] && [ "$MR_VMID" != "$SIM_VMID" ] && [ "$MR_VMID" != "$LAN_VMID" ] || {
    echo "REFUSED: duplicate VM IDs in lab target map" >&2
    return 1
  }
  H "set -eu
qm config $MR_VMID | grep -q '^name: MR-TEST$'
qm config $MR_VMID | grep -q '^net0:.*bridge=vmbr-lab-wan'
qm config $MR_VMID | grep -q '^net1:.*bridge=vmbr-lab-lan'
qm config $ISP_VMID | grep -q '^name: ISP-LAB$'
qm config $ISP_VMID | grep -q '^net0:.*bridge=vmbr-lab-uplink'
qm config $ISP_VMID | grep -q '^net1:.*bridge=vmbr-lab-wan'
qm config $SIM_VMID | grep -q '^name: SIM-LAB$'
qm config $SIM_VMID | grep -q '^net0:.*bridge=vmbr-lab-wan'
qm config $LAN_VMID | grep -q '^name: LAN-CLIENT2$'
qm config $LAN_VMID | grep -q '^net0:.*bridge=vmbr-lab-lan'
qm status 106 | grep -q '^status: running$'" || {
    echo "REFUSED: Proxmox inventory does not match the isolated lab topology" >&2
    return 1
  }
}

# --- helpers ----------------------------------------------------------------
# mr_save <json-fragment-pairs...> — save a trivial local change via API
mr_save_lease() {  # toggles lease_time; returns canonical revision
  api_login
  cfg="$(api GET /api/v1/config)"
  rev="$(echo "$cfg" | python3 -c 'import json,sys; print(json.load(sys.stdin)["revision"])')"
  cur="$(echo "$cfg" | python3 -c 'import json,sys; print(json.load(sys.stdin)["dhcp"]["lease_time"])')"
  new="$(echo "$cur" | grep -q 12h && echo 2h || echo 12h)"
  api PUT /api/v1/config "$(echo "$cfg" | python3 -c "
import json,sys
c=json.load(sys.stdin)
c['dhcp']['lease_time']='$new'
print(json.dumps(c))")" >/dev/null 2>&1 || return 1
  confirm_pending
}

# wait_pppoe <seconds> — poll until ppp0 has the lab address
wait_pppoe() { t="${1:-90}"; i=0; while [ $i -lt "$t" ]; do check_pppoe && return 0; sleep 3; i=$((i+3)); done; return 1; }
wait_pppoe_down() { t="${1:-30}"; i=0; while [ $i -lt "$t" ]; do check_pppoe || return 0; sleep 3; i=$((i+3)); done; return 1; }

# LAN-CLIENT2 is managed by systemd-networkd. `dhclient` is intentionally not
# installed, so renew through networkctl and wait for the lab subnet to return.
lan_dhcp_renew() {
  lan "networkctl renew '$LAN_CLIENT_IF' >/dev/null 2>&1" || return 1
  retry 30 lan "ip -4 -o addr show '$LAN_CLIENT_IF' | grep -q '192.168.1.'"
}

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
    echo "--- lan client ---"
    lan 'ip -4 -o addr show; ip route; tail -5 /var/log/syslog 2>/dev/null'
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

# reset_mr_baseline — force the canonical feature flags (adguard, squid, qos)
# back to disabled before each scenario. Failed or aborted scenarios leak
# state into canonical (e.g. 65/84 preconditions), so reset_lab normalizes it.
reset_mr_baseline() {
  api_login || return 1
  cfg="$(api GET /api/v1/config)" || return 1
  base="$(echo "$cfg" | python3 -c '
import json,sys
c=json.load(sys.stdin)
c["adguard"]["enabled"]=False
c["squid_proxy"]["enabled"]=False
c["qos"]["enabled"]=False
print(json.dumps(c))')" || return 1
  api PUT /api/v1/config "$base" >/dev/null 2>&1 || return 1
  confirm_pending >/dev/null 2>&1 || true
}

# --- retry / wait helpers ----------------------------------------------------
# retry <secs> <cmd...> — run the command (functions visible) until success
retry() { t="$1"; shift; i=0; while [ $i -lt "$t" ]; do "$@" && return 0; sleep 5; i=$((i+5)); done; return 1; }
# wait_login_window — allow the per-source one-minute login limiter to reset
# without a single long blocking sleep.
wait_login_window() { i=0; while [ "$i" -lt 65 ]; do sleep 5; i=$((i+5)); done; }
# totp_code <base32-secret> — current six-digit RFC 6238 code.
totp_code() {
  printf '%s' "$1" | python3 -c '
import base64,hashlib,hmac,struct,sys,time
secret=sys.stdin.read().strip().upper()
secret += "="*((8-len(secret)%8)%8)
key=base64.b32decode(secret)
counter=struct.pack(">Q",int(time.time())//30)
digest=hmac.new(key,counter,hashlib.sha1).digest()
offset=digest[-1]&15
value=(struct.unpack(">I",digest[offset:offset+4])[0]&0x7fffffff)%1000000
print(f"{value:06d}")'
}
# wait_next_totp — move into the next 30-second TOTP interval so replay
# protection never mistakes two legitimate test operations for reuse.
wait_next_totp() {
  remaining=$((31-$(date +%s)%30))
  while [ "$remaining" -gt 0 ]; do sleep 1; remaining=$((remaining-1)); done
}
# mr_wait <secs> — poll until the MR-TEST guest agent answers
mr_wait() { t="${1:-120}"; i=0; while [ $i -lt "$t" ]; do [ "$(mr 'echo ok' 2>/dev/null)" = "ok" ] && return 0; sleep 5; i=$((i+5)); done; return 1; }
# wait_vm_stopped <vmid> <secs> / wait_vm_running
wait_vm_stopped() { t="${2:-120}"; i=0; while [ $i -lt "$t" ]; do H "qm status $1" 2>/dev/null | grep -qi stopped && return 0; sleep 5; i=$((i+5)); done; return 1; }
wait_vm_running() { t="${2:-120}"; i=0; while [ $i -lt "$t" ]; do H "qm status $1" 2>/dev/null | grep -qi running && return 0; sleep 5; i=$((i+5)); done; return 1; }
# mr_put <local-file> <remote-path> — chunked base64 push via the guest agent
mr_put() {
  src="$1"; dst="$2"
  size=$(wc -c < "$src") || return 1
  mr "rm -f '$dst'" >/dev/null 2>&1
  pos=0
  while [ $pos -lt "$size" ]; do
    chunk=$(dd if="$src" bs=1 skip=$pos count=60000 2>/dev/null | base64 | tr -d '\n')
    mr "echo '$chunk' | base64 -d >> '$dst'" >/dev/null || return 1
    pos=$((pos+60000))
  done
  mr "test -s '$dst'"
}

# lan_put <local-file> <remote-path> — copy evidence or upload fixtures to the
# isolated LAN client without exposing them on the Proxmox host web path.
lan_put() {
  src="$1"; dst="$2"
  size=$(wc -c < "$src") || return 1
  lan "rm -f '$dst'" >/dev/null 2>&1
  pos=0
  while [ $pos -lt "$size" ]; do
    chunk=$(dd if="$src" bs=1 skip=$pos count=60000 2>/dev/null | base64 | tr -d '\n')
    lan "echo '$chunk' | base64 -d >> '$dst'" >/dev/null || return 1
    pos=$((pos+60000))
  done
  lan "test -s '$dst'"
}

# --- config save helpers -----------------------------------------------------
# save_config — GET current config and PUT it back (exercises the full save
# path; pending transactions are confirmed)
save_config() {
  api_login
  cfg="$(api GET /api/v1/config)" || return 1
  api PUT /api/v1/config "$cfg" >/dev/null 2>&1 || return 1
  confirm_pending
}
# confirm_pending — confirm the active connectivity transaction, if any.
# Several scenarios call this after a successful PUT; keeping it here avoids
# each scenario reimplementing transaction discovery and JSON parsing.
confirm_pending() {
  pending="$(api GET /api/v1/transactions/pending)" || return 1
  id="$(echo "$pending" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("id", ""))' 2>/dev/null)"
  [ -z "$id" ] || [ "$id" = "None" ] || api POST "/api/v1/transactions/$id/confirm" >/dev/null 2>&1
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
  api PUT /api/v1/config "$new" >/dev/null 2>&1 || return 1
  confirm_pending
}

# api_reconcile — ask routerd to reconstruct runtime from canonical state.
api_reconcile() {
  api_login || return 1
  code="$(api_status POST /api/v1/recovery/reconcile '{}')"
  [ "$code" = "200" ]
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
# save_expects_error <json> — validation/transition candidates must be rejected
# with a client error, never inferred from a loosely matching response body.
save_expects_error() {
  api_login
  code="$(api_status PUT /api/v1/config "$1")"
  case "$code" in
    400|403|409|415|422) return 0 ;;
    *) return 1 ;;
  esac
}
