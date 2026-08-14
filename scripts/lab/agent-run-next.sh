#!/bin/sh
# Deterministic one-scenario-at-a-time entry point for the OpenCode agent.
# A scenario is complete only after this wrapper records rc=0 in its ledger.

set -eu
LABDIR="$(cd "$(dirname "$0")" && pwd)"
SCENDIR="$LABDIR/scenarios"
RESULTS_DIR="${LAB_RESULTS:-$LABDIR/results}"
LEDGER="$RESULTS_DIR/.agent-ledger"
LOCK="${LAB_AGENT_LOCK:-/tmp/minimalrouter-lab-agent.lock}"
mkdir -p "$LEDGER"

scenarios() {
  find "$SCENDIR" -maxdepth 1 -type f -name '[0-9]*.sh' | sort -V
}

scenario_rc() {
  file="$LEDGER/$1.rc"
  [ -f "$file" ] && sed -n '1p' "$file" || echo missing
}

next_scenario() {
  for path in $(scenarios); do
    name="$(basename "$path" .sh)"
    [ "$(scenario_rc "$name")" = "0" ] || { echo "$name"; return 0; }
  done
  return 1
}

status() {
  total=0; passed=0; failed=0; missing=0
  for path in $(scenarios); do
    name="$(basename "$path" .sh)"; total=$((total+1))
    case "$(scenario_rc "$name")" in
      0) passed=$((passed+1)) ;;
      1) failed=$((failed+1)) ;;
      *) missing=$((missing+1)) ;;
    esac
  done
  printf 'total=%d pass=%d fail=%d not_run=%d\n' "$total" "$passed" "$failed" "$missing"
  next="$(next_scenario || true)"
  [ -z "$next" ] || echo "next=$next"
}

acquire_lock() {
  cleanup_lock() {
    rm -f "$LOCK/pid"
    rmdir "$LOCK" 2>/dev/null || true
  }
  if mkdir "$LOCK" 2>/dev/null; then
    echo "$$" > "$LOCK/pid"
    trap cleanup_lock EXIT HUP INT TERM
    return 0
  fi
  pid="$(sed -n '1p' "$LOCK/pid" 2>/dev/null || true)"
  if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
    echo "REFUSED: another lab scenario is active (pid $pid)" >&2
    return 1
  fi
  rm -f "$LOCK/pid"
  rmdir "$LOCK" 2>/dev/null || {
    echo "REFUSED: stale non-empty lock at $LOCK; inspect it manually" >&2
    return 1
  }
  mkdir "$LOCK"
  echo "$$" > "$LOCK/pid"
  trap cleanup_lock EXIT HUP INT TERM
}

run_one() {
  name="$1"
  case "$name" in
    *[!0-9]*) ;;
    *)
      resolved="$(scenarios | awk -F/ -v wanted="$name" '{base=$NF; split(base,p,"-"); if ((p[1]+0)==(wanted+0)) {print base; exit}}')"
      [ -n "$resolved" ] || { echo "unknown scenario number: $name" >&2; return 2; }
      name="${resolved%.sh}"
      ;;
  esac
  [ -f "$SCENDIR/$name.sh" ] || { echo "unknown scenario: $name" >&2; return 2; }
  acquire_lock
  started="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  if sh "$LABDIR/lab-run.sh" "$name"; then
    printf '0\n' > "$LEDGER/$name.rc"
    printf '%s pass\n' "$started" > "$LEDGER/$name.meta"
    echo "completed=$name rc=0"
    return 0
  fi
  printf '1\n' > "$LEDGER/$name.rc"
  printf '%s fail\n' "$started" > "$LEDGER/$name.meta"
  echo "completed=$name rc=1" >&2
  return 1
}

case "${1:-status}" in
  status) status ;;
  next)
    name="$(next_scenario || true)"
    [ -n "$name" ] || { echo "all scenarios passed"; exit 0; }
    run_one "$name"
    ;;
  run)
    [ -n "${2:-}" ] || { echo "usage: $0 run <scenario-number-or-name>" >&2; exit 2; }
    run_one "$2"
    ;;
  *) echo "usage: $0 status | next | run <scenario>" >&2; exit 2 ;;
esac
