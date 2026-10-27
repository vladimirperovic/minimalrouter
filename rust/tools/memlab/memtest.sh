#!/bin/sh
# Memory comparison harness for the lab router. Runs as root.
#   memtest.sh backup          one-time copy of both state directories
#   memtest.sh start IMPL      IMPL = installed | go | rust; restores the backup,
#                              bind-mounts /opt/memtest/IMPL binaries over the
#                              active slot's daemons and starts both services
#   memtest.sh sample LABEL    one line of memory figures per daemon
#   memtest.sh restore         installed binaries, original state, services up
set -eu

BACKUP=/var/tmp/memtest-state.tar
SLOT="$(readlink -f /var/lib/minimalrouter-update/current)/bin"

stop_services() {
    rc-service routerd stop >/dev/null 2>&1 || true
    rc-service router-applyd stop >/dev/null 2>&1 || true
    for d in routerd router-applyd; do
        if grep -q " $SLOT/$d-amd64 " /proc/mounts; then
            umount "$SLOT/$d-amd64"
        fi
    done
}

restore_state() {
    rm -rf /var/lib/minimalrouter /var/lib/minimalrouter-applyd
    tar -xf "$BACKUP" -C /var/lib
}

ms() { awk '{ printf "%d", $1 * 1000 }' /proc/uptime; }

start_services() {
    t0=$(ms)
    rc-service router-applyd start >/dev/null
    t1=$(ms)
    rc-service routerd start >/dev/null
    t2=$(ms)
    echo "start applyd_ms=$((t1 - t0)) routerd_ms=$((t2 - t1))"
}

pid_of() {
    pgrep -f "slots/.*/bin/$1-amd64" | head -n 1
}

field() { awk -v k="$1:" '$1 == k { print $2 }' "$2"; }

sample() {
    label="$1"
    avail=$(field MemAvailable /proc/meminfo)
    for d in router-applyd routerd; do
        p=$(pid_of "$d")
        [ -n "$p" ] || { echo "sample $label $d missing"; continue; }
        s=/proc/$p/status
        r=/proc/$p/smaps_rollup
        set -- $(cut -d' ' -f14,15 /proc/$p/stat)
        echo "sample $label $d pid=$p rss=$(field VmRSS $s) hwm=$(field VmHWM $s)" \
            "anon=$(field RssAnon $s) file=$(field RssFile $s) pss=$(field Pss $r)" \
            "private_dirty=$(field Private_Dirty $r) vsz=$(field VmSize $s)" \
            "threads=$(field Threads $s) cpu_ticks=$(($1 + $2)) mem_available=$avail"
    done
}

case "${1:-}" in
    backup)
        [ -f "$BACKUP" ] && { echo "backup exists"; exit 0; }
        stop_services
        tar -cf "$BACKUP" -C /var/lib minimalrouter minimalrouter-applyd
        start_services
        ls -l "$BACKUP"
        ;;
    start)
        impl="$2"
        stop_services
        restore_state
        sync
        echo 3 > /proc/sys/vm/drop_caches
        if [ "$impl" != installed ]; then
            for d in routerd router-applyd; do
                mount --bind "/opt/memtest/$impl/$d" "$SLOT/$d-amd64"
            done
        fi
        start_services
        for d in router-applyd routerd; do
            p=$(pid_of "$d")
            echo "running $d pid=$p exe_sha=$(sha256sum /proc/$p/exe | cut -c1-12)"
        done
        ;;
    sample)
        sample "$2"
        ;;
    restore)
        stop_services
        restore_state
        start_services
        sample restored
        ;;
    *)
        echo "usage: $0 backup|start IMPL|sample LABEL|restore" >&2
        exit 2
        ;;
esac
