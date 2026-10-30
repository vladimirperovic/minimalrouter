#!/bin/sh
# Identify one real daemon, never the SSH shell whose command mentions it.
# The optional slot root permits an isolated Linux process fixture.
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
        # Read the kernel comm directly. BusyBox pgrep -x can reject an
        # absolute argv[0] before considering its matching comm.
        pids=
        for comm_file in /proc/[0-9]*/comm; do
            IFS= read -r comm 2>/dev/null < "$comm_file" || continue
            [ "$comm" = "$name" ] || continue
            candidate=${comm_file#/proc/}
            candidate=${candidate%/comm}
            case "$candidate" in ''|*[!0-9]*) continue ;; esac
            pids="$pids $candidate"
        done
        [ -n "$pids" ] || { echo "$name process missing" >&2; exit 1; }
        ;;
esac
set -- $pids
[ "$#" -eq 1 ] || { echo "$name has multiple matching processes: $pids" >&2; exit 1; }
pid=$1
case "$name" in
    dnsmasq|pppd)
        # Recheck after discovery: a vanished or renamed process is not
        # evidence of the exact daemon whose start time we are observing.
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
