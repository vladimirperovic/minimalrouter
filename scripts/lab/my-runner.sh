#!/bin/sh
cd "$(dirname "$0")" || exit 1
. ./lib.sh
for s in "$@"; do
  lg=$(mr 'cat /var/lib/minimalrouter-applyd/last-good.json 2>/dev/null' 2>/dev/null)
  if ! printf '%s' "$lg" | grep -q '"revision"'; then
    echo "[$(date -u +%H:%M:%S)] [heal] last-good corrupt/missing -> quarantine + restart applyd/routerd (before $s)"
    mr 'mv /var/lib/minimalrouter-applyd/last-good.json /var/lib/minimalrouter-applyd/last-good.json.corrupt.$(date -u +%Y%m%dT%H%M%SZ) 2>/dev/null; nohup rc-service router-applyd start >/dev/null 2>&1 &' >/dev/null 2>&1
    sleep 14
    mr 'touch /run/minimalrouter/routerd.ready; chown routerd:routerd /run/minimalrouter/routerd.ready; chmod 600 /run/minimalrouter/routerd.ready; nohup rc-service routerd start >/dev/null 2>&1 &' >/dev/null 2>&1
    sleep 18
    wg=$(mr 'wg show 2>/dev/null | grep -c interface' 2>/dev/null)
    echo "[$(date -u +%H:%M:%S)] [heal] wg interfaces after heal: $wg"
  fi
  sh lab-run.sh "$s" || echo "[$(date -u +%H:%M:%S)] [runner] scenario $s exited nonzero"
done
echo "[$(date -u +%H:%M:%S)] [runner] CAMPAIGN FINISHED"
