#!/bin/sh
# lab-stats.sh — scenario result summary with local-time display
# Usage: sh lab-stats.sh [path/to/results]
#   LAB_STATS_TZ overrides the display timezone (default Europe/Podgorica)
set -u
RESULTS_DIR="${1:-$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)/results}"
TZ_LAB="${LAB_STATS_TZ:-Europe/Podgorica}"

rows=""
pass=0
fail=0

for f in "$RESULTS_DIR"/*/result.txt; do
	[ -f "$f" ] || continue
	scenario=$(basename "$(dirname "$f")")
	ts=$(sed -n 's/^\(.*\) rc=[0-9]*$/\1/p' "$f")
	rc=$(sed -n 's/^.* rc=\([0-9]*\)$/\1/p' "$f")
	if [ -n "$ts" ]; then
		if display=$(TZ="$TZ_LAB" date -d "$ts" +"%Y-%m-%d %H:%M:%S %Z" 2>/dev/null); then
			ts=$display
		fi
	fi
	if [ "${rc:-}" = "0" ]; then
		status="PASS"
		pass=$((pass + 1))
	else
		status="FAIL"
		fail=$((fail + 1))
	fi
	rows="$rows$status|$ts|$scenario
"
done

total=$((pass + fail))
if [ "$total" -eq 0 ]; then
	echo "no scenario results found in $RESULTS_DIR"
	exit 0
fi

echo "== lab results ($TZ_LAB) — $total scenarios =="
printf '%s' "$rows" | sort -t'|' -k2 | awk -F'|' '{ printf "  %s  %-22s  %s\n", $1, $2, $3 }'
echo "== summary: $pass PASS, $fail FAIL =="
