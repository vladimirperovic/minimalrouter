#!/bin/bash
# Run one bounded OpenCode lab task per timer activation.

set -euo pipefail
umask 077
. /etc/opencode-lab.env

API="${OPENCODE_API:-http://127.0.0.1:4080}"
MODEL_PROVIDER="${OPENCODE_MODEL_PROVIDER:-opencode}"
MODEL_ID="${OPENCODE_MODEL_ID:-deepseek-v4-flash-free}"
REPO="${LAB_REPO:-/projekti/minimalrouter}"
LOG="${LAB_NIGHTLY_LOG:-/root/nightly-lab.log}"
RUN_TIMEOUT_SECONDS="${LAB_AGENT_TIMEOUT_SECONDS:-1800}"
WORKER_LOCK="${LAB_WORKER_LOCK:-/run/lock/minimalrouter-opencode-worker.lock}"
STATE="${LAB_WORKER_STATE:-/root/.minimalrouter-opencode-worker.state}"

log() { echo "$(date '+%F %T') $*" >> "$LOG"; }

abort_session() {
  session_id="$1"
  [ -n "$session_id" ] || return 0
  curl -fsS -m 10 -u "$OPENCODE_AUTH" -X POST \
    "$API/session/$session_id/abort" >/dev/null 2>&1 || true
}

write_wrapper_report() {
  result="$1"
  reason="$2"
  {
    printf '# MinimalRouter autonomous lab report\n\n'
    printf -- '- Scenario: %s\n' "$SCENARIO"
    printf -- '- Result: %s\n' "$result"
    printf -- '- Session: %s\n' "$SID"
    printf -- '- Model: %s/%s\n' "$MODEL_PROVIDER" "$MODEL_ID"
    printf -- '- Started: %s\n' "$STARTED_AT"
    printf -- '- Finished: %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    printf -- '- Reason: %s\n\n' "$reason"
    printf '## Campaign status\n\n```\n'
    sh "$REPO/scripts/lab/agent-run-next.sh" status
    printf '```\n\nThe wrapper accepted no success without a durable `rc=0` ledger entry.\n'
  } > "$REPORT"
}

exec 9>"$WORKER_LOCK"
if ! flock -n 9; then
  log "SKIP: another OpenCode lab worker is active"
  exit 0
fi

scenario_count="$(find "$REPO/scripts/lab/scenarios" -maxdepth 1 -type f -name '[0-9]*.sh' | wc -l)"
if [ "$scenario_count" -ne 150 ]; then
  log "REFUSED: expected 150 scenarios, found $scenario_count"
  exit 1
fi

campaign_status="$(sh "$REPO/scripts/lab/agent-run-next.sh" status)"
if printf '%s\n' "$campaign_status" | grep -q 'total=150 pass=150 fail=0 not_run=0'; then
  log "COMPLETE: all 150 scenarios passed"
  exit 0
fi

SCENARIO="$(printf '%s\n' "$campaign_status" | sed -n 's/^next=//p' | head -n 1)"
if [ -z "$SCENARIO" ]; then
  log "ERROR: campaign is incomplete but no next scenario was found"
  exit 1
fi

if ! session_status="$(curl -fsS -m 8 -u "$OPENCODE_AUTH" "$API/session/status")"; then
  log "WARN: OpenCode API unavailable; restarting opencode.service once"
  systemctl restart opencode.service
  sleep 10
  session_status="$(curl -fsS -m 8 -u "$OPENCODE_AUTH" "$API/session/status")" || {
    log "ERROR: OpenCode API remains unavailable after restart"
    exit 1
  }
fi

if [ "$session_status" != "{}" ]; then
  if [ -f "$STATE" ]; then
    read -r stale_sid stale_started < "$STATE" || true
    now="$(date +%s)"
    if [ -n "${stale_sid:-}" ] && [ -n "${stale_started:-}" ] && \
       [ $((now - stale_started)) -gt $((RUN_TIMEOUT_SECONDS + 120)) ]; then
      log "RECOVER: aborting stale autonomous session=$stale_sid"
      abort_session "$stale_sid"
      rm -f "$STATE"
      sleep 2
      session_status="$(curl -fsS -m 8 -u "$OPENCODE_AUTH" "$API/session/status")" || {
        log "ERROR: OpenCode status unavailable after stale-session recovery"
        exit 1
      }
      if [ "$session_status" != "{}" ]; then
        log "SKIP: another OpenCode session remains busy after recovery"
        exit 0
      fi
    else
      log "SKIP: an OpenCode session is busy"
      exit 0
    fi
  else
    log "SKIP: an OpenCode session is busy"
    exit 0
  fi
fi

SID="$(curl -fsS -m 10 -u "$OPENCODE_AUTH" -X POST "$API/session" \
  -H 'Content-Type: application/json' \
  -d "{\"title\":\"minimalrouter lab scenario $SCENARIO $(date +%F-%H%M)\"}" | \
  python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')"
STARTED_EPOCH="$(date +%s)"
STARTED_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
REPORT="/root/nightly-report-$(date +%F).md"
RESPONSE="/root/nightly-response-$SID.json"
printf '%s %s\n' "$SID" "$STARTED_EPOCH" > "$STATE"

cleanup() { rm -f "$STATE"; }
on_signal() {
  abort_session "$SID"
  exit 143
}
trap cleanup EXIT
trap on_signal HUP INT TERM

prompt="$(printf '%s\n' \
  "You are the isolated MinimalRouter lab worker in $REPO." \
  "" \
  "Complete only scenario $SCENARIO in this session. Work directly and stay bounded:" \
  "1. Read scripts/lab/OPENCODE.md." \
  "2. Run sh scripts/lab/agent-run-next.sh status once." \
  "3. Run sh scripts/lab/agent-run-next.sh next once." \
  "4. If it passes, write the report and stop immediately." \
  "5. If it fails, inspect only that scenario's failing assertion, evidence and relevant product logs. Classify the cause as product, test or lab. Make at most one smallest justified fix. Preserve every unrelated working-tree change." \
  "6. If code changed, run the relevant targeted unit test and then the normal unit suite once." \
  "7. Retry exactly the same scenario once with: sh scripts/lab/agent-run-next.sh run $SCENARIO. If it still fails, write BLOCKED with the precise remaining cause and stop. Do not continue investigating or retrying." \
  "8. Write the final report to $REPORT with scenario, evidence, changes and PASS or BLOCKED result, then stop." \
  "" \
  "Hard limits: never run lab-run.sh all; never start another scenario; at most two executions of $SCENARIO; do not browse the internet; do not commit, push, reset, clean, stash or overwrite unrelated changes." \
  "" \
  "Hard safety boundary: fault targets are only VMs 150, 151, 153 and 154 on vmbr-lab-*. pfSense VM 106, vmbr0, Home Assistant, Nextcloud and all home services are production and must never be changed, restarted, scanned or used as API targets. The runner preflight is mandatory.")"

payload="$(python3 -c 'import json,sys; print(json.dumps({"model":{"providerID":sys.argv[1],"modelID":sys.argv[2]},"agent":"build","parts":[{"type":"text","text":sys.argv[3]}]}))' \
  "$MODEL_PROVIDER" "$MODEL_ID" "$prompt")"

log "START: session=$SID scenario=$SCENARIO model=$MODEL_PROVIDER/$MODEL_ID timeout=${RUN_TIMEOUT_SECONDS}s"
set +e
http_code="$(timeout --signal=TERM --kill-after=15s "${RUN_TIMEOUT_SECONDS}s" \
  curl -sS --max-time "$RUN_TIMEOUT_SECONDS" -o "$RESPONSE" -w '%{http_code}' \
  -u "$OPENCODE_AUTH" -X POST "$API/session/$SID/message" \
  -H 'Content-Type: application/json' -d "$payload")"
run_rc=$?
set -e

if [ "$run_rc" -ne 0 ]; then
  abort_session "$SID"
  write_wrapper_report "BLOCKED" "OpenCode timed out or disconnected (curl/timeout rc=$run_rc, HTTP=${http_code:-none})."
  log "BLOCKED: session=$SID scenario=$SCENARIO transport_rc=$run_rc http=${http_code:-none}"
  exit 1
fi

if [ "$http_code" != "200" ]; then
  abort_session "$SID"
  write_wrapper_report "BLOCKED" "OpenCode returned HTTP $http_code."
  log "BLOCKED: session=$SID scenario=$SCENARIO http=$http_code"
  exit 1
fi

ledger_rc="$REPO/scripts/lab/results/.agent-ledger/$SCENARIO.rc"
if [ -f "$ledger_rc" ] && [ "$(sed -n '1p' "$ledger_rc")" = "0" ]; then
  if [ ! -s "$REPORT" ] || [ "$(stat -c %Y "$REPORT")" -lt "$STARTED_EPOCH" ]; then
    write_wrapper_report "PASS" "The durable scenario ledger recorded rc=0."
  fi
  log "PASS: session=$SID scenario=$SCENARIO"
  exit 0
fi

if [ ! -s "$REPORT" ] || [ "$(stat -c %Y "$REPORT")" -lt "$STARTED_EPOCH" ]; then
  write_wrapper_report "BLOCKED" "OpenCode returned without a durable rc=0 result for the assigned scenario."
fi
log "BLOCKED: session=$SID scenario=$SCENARIO no durable pass"
exit 1
