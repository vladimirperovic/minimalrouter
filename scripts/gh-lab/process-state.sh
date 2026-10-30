#!/bin/sh
# Inspect or signal an already identified process in the disposable lab.
# Sent on stdin: no process-search expression enters an SSH command line.
set -eu
[ "$#" -eq 3 ] || { echo "expected action, PID and start time" >&2; exit 1; }
action=$1 pid=$2 expected=$3
case "$action" in rss|STOP|CONT) ;; *) echo "invalid process action" >&2; exit 1 ;; esac
case "$pid" in ''|*[!0-9]*) echo "invalid process PID" >&2; exit 1 ;; esac
case "$expected" in ''|*[!0-9]*) echo "invalid process start time" >&2; exit 1 ;; esac
[ "$pid" -gt 1 ] || { echo "invalid process PID" >&2; exit 1; }

require_identity() {
    stat=$(cat "/proc/$pid/stat") || return 1
    fields=${stat##*) }
    set -- $fields
    [ "$#" -ge 20 ] || { echo "incomplete process stat" >&2; return 1; }
    case "$1" in Z|X) echo "process is not running" >&2; return 1 ;; esac
    shift 19
    [ "$1" = "$expected" ] || { echo "process identity changed" >&2; return 1; }
}

require_identity
case "$action" in
    rss)
        rss=$(awk '$1 == "VmRSS:" {
            count++; if (NF != 3 || $2 !~ /^[0-9]+$/ || $3 != "kB") bad=1; value=$2
        } END { if (count != 1 || bad) exit 1; print value }' "/proc/$pid/status") || {
            echo "missing or invalid process RSS" >&2; exit 1;
        }
        case "$rss" in ''|*[!0-9]*) echo "missing or invalid process RSS" >&2; exit 1 ;; esac
        require_identity
        printf '%s\n' "$rss"
        ;;
    STOP|CONT)
        kill -"$action" "$pid"
        ;;
esac
