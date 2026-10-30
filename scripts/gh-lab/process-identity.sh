#!/bin/sh
# Identify one real daemon, never the SSH shell whose command mentions it.
# The optional slot root permits an isolated Linux regression with /bin/sleep.
set -eu
name=${1:?daemon name required}
case "$name" in routerd|router-applyd|dnsmasq|pppd) ;; *) echo "invalid daemon name" >&2; exit 1 ;; esac
slot_root=${2:-/var/lib/minimalrouter-update/slots}
escaped_root=$(printf '%s' "$slot_root" | sed 's/[][\\.^$*+?(){}|]/\\&/g')
pattern="^$escaped_root/[^/]+/bin/$name-amd64([[:space:]]|$)"
case "$name" in
    routerd|router-applyd)
        pids=$(pgrep -f "$pattern") || { echo "$name slot process missing" >&2; exit 1; }
        ;;
    dnsmasq|pppd)
        # These are system daemons, not slot binaries. Match the exact comm,
        # never an SSH shell whose argument happens to mention their name.
        candidates=$(pgrep -x "$name") || { echo "$name process missing" >&2; exit 1; }
        pids=
        for candidate in $candidates; do
            comm=$(cat "/proc/$candidate/comm" 2>/dev/null) || continue
            [ "$comm" != "$name" ] || pids="$pids $candidate"
        done
        [ -n "$pids" ] || { echo "$name process missing" >&2; exit 1; }
        ;;
esac
set -- $pids
[ "$#" -eq 1 ] || { echo "$name has multiple matching processes: $pids" >&2; exit 1; }
pid=$1
case "$name" in
    dnsmasq|pppd)
        # BusyBox may also consider argv[0] for pgrep -x. A renamed argv[0]
        # is insufficient: the kernel comm must name this exact daemon.
        [ "$(cat "/proc/$pid/comm")" = "$name" ] || { echo "$name process comm mismatch" >&2; exit 1; }
        ;;
esac
stat=$(cat "/proc/$pid/stat") || exit 1
# comm is parenthesized and may contain spaces or ')'; fields after its final
# ')' start at field 3. starttime (field 22) distinguishes PID reuse.
fields=${stat##*) }
set -- $fields
[ "$#" -ge 20 ] || { echo "$name has an incomplete process stat" >&2; exit 1; }
case "$1" in Z|X) echo "$name is not running" >&2; exit 1 ;; esac
shift 19
start=$1
case "$start" in ''|*[!0-9]*) echo "$name has an invalid start time" >&2; exit 1 ;; esac
printf '%s %s\n' "$pid" "$start"
