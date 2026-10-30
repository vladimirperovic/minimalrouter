#!/bin/bash
# Unplanned-style tests for the GitHub lab: hostile input, hostile HTTP, link
# flaps, a clock that jumps, frozen and killed daemons, floods, a full disk,
# configuration churn and repeated power cuts. Run after suite.sh against the
# same router; every test runs even if another fails.
set -u
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)

RESULTS="$LOGS/chaos-results.tsv"
: > "$RESULTS"
record() { printf '%s\t%s\t%s\n' "$1" "$2" "$3" >> "$RESULTS"; log "$1  $2  $3"; }
run() {
    local name=$1 out rc
    # LAB_ONLY=REGEX runs just the matching tests (plus the kernel-log crash scans).
    if [ -n "${LAB_ONLY:-}" ] && ! printf "%s" "$name" | grep -Eiq "$LAB_ONLY|no crashes"; then
        return 0
    fi
    shift
    out=$("$@" 2>&1)
    rc=$?
    # Preserve tracebacks and every DNS/HTTP observation, including on PASS.
    printf '\n=== %s (exit %s) ===\n%s\n' "$name" "$rc" "$out" | tee -a "$LOGS/chaos-details.txt"
    if [ "$rc" -eq 0 ]; then
        record PASS "$name" "$(printf '%s' "$out" | tail -n 1 | cut -c1-220)"
    elif [ "$rc" -eq 77 ]; then
        record SKIP "$name" "$(printf '%s' "$out" | tail -n 1 | cut -c1-220)"
    else
        record FAIL "$name" "$(printf '%s' "$out" | tail -n 6 | tr '\n' ' ' | cut -c1-500)"
    fi
}
http_code() { api call "$@" | head -n 1 | awk '{print $2}'; }
config_json() { api call GET config | tail -n +2; }
state_digest() { echo "revision=$(config_json | jq -r .revision) hostname=$(config_json | jq -r .system.hostname)"; }

ppp_up() { rsh 'ip -4 -o addr show ppp0 | grep -q "10\.250\.0\."'; }
router_healthy() {
    # Reuses the saved session (labapi signs in again only when it is invalid):
    # the login rate limit is five a minute, and the chaos run restarts a lot.
    rsh 'test -s /run/minimalrouter/routerd.ready && ss -lnt | grep -q ":8443 " && ss -lnt | grep -q ":22 "' && [ "$(http_code GET health)" = 200 ]
}
no_crashes() {
    local bad
    bad=$(rsh 'dmesg | grep -iE "segfault|general protection|invalid opcode|Oops|BUG:|Call Trace|Out of memory|oom-kill|EXT4-fs error"' || true)
    [ -z "$bad" ] || { printf '%s\n' "$bad" | head -n 3; return 1; }
}
daemon_memory_mib() { rsh "awk '\$1==\"VmRSS:\"{print int(\$2/1024)}' /proc/\$(pgrep -f 'slots/.*/bin/$1-amd64' | head -n 1)/status"; }

# ------------------------------------------------------------------- tests

c_api_fuzz() {
    local seed=${CHAOS_SEED:-$(date +%s)}
    python3 "$HERE/fuzz.py" 500 "$seed" 2>&1 | tee "$LOGS/fuzz.txt" | tail -n 12
    [ "${PIPESTATUS[0]}" -eq 0 ] || return 1
    router_healthy || { echo "router unhealthy after the fuzz run"; return 1; }
    no_crashes || return 1
    echo "500 hostile configurations and every mutating endpoint fuzzed (seed $seed): no 5xx, configuration restored"
}

c_http_abuse() {
    python3 "$HERE/http_abuse.py" 2>&1 | tee "$LOGS/http-abuse.txt" | grep -E "^(FAIL|[0-9]+ of)" | tail -n 8
    [ "${PIPESTATUS[0]}" -eq 0 ] || return 1
    router_healthy || return 1
    echo "$(tail -n 1 "$LOGS/http-abuse.txt")"
}

c_wan_link_flaps() {
    local fw i
    fw=$(firewall_fingerprint) || return 1
    for i in 1 2 3 4 5 6; do
        qemu_monitor "set_link nicwan off"
        sleep 4
        qemu_monitor "set_link nicwan on"
        sleep 6
    done
    wait_for 300 ppp_up || { rsh 'ip -o addr; ps w | grep -E "pppd|pppoe"'; echo "PPPoE did not come back after 6 link flaps"; return 1; }
    [ "$(firewall_fingerprint)" = "$fw" ] || { echo "firewall rules changed during link flaps"; return 1; }
    router_healthy || return 1
    wait_for 60 client curl -s --max-time 5 "http://$INET_IP:8080/marker.txt" || { echo "LAN traffic did not recover"; return 1; }
    no_crashes || return 1
    echo "6 WAN link flaps: PPPoE re-established, firewall identical, LAN traffic flows"
}

c_lan_link_flaps() {
    local i
    for i in 1 2 3 4 5; do
        qemu_monitor "set_link niclan off"
        sleep 3
        qemu_monitor "set_link niclan on"
        sleep 5
    done
    wait_for 120 rsh true || { echo "SSH did not come back after LAN link flaps"; return 1; }
    router_healthy || return 1
    wait_for 60 client curl -s --max-time 5 "http://$INET_IP:8080/marker.txt" || { echo "client lost its path through the router"; return 1; }
    echo "5 LAN link flaps survived; management and routing intact"
}

c_clock_jumps() {
    local now
    now=$(rsh 'date +%s')
    rsh "date -s @$((now + 7200))" >/dev/null
    sleep 15
    router_healthy || { echo "unhealthy after the clock moved 2 h forward"; return 1; }
    rsh "date -s @$((now - 3600))" >/dev/null
    sleep 15
    router_healthy || { echo "unhealthy after the clock moved 1 h back"; return 1; }
    rsh "date -s @$((now + 60))" >/dev/null
    sleep 5
    ppp_up || { echo "PPPoE dropped after clock jumps"; return 1; }
    no_crashes || return 1
    echo "clock +2 h, then -1 h, then restored: daemons healthy, PPPoE up"
}

c_frozen_routerd() {
    local pid
    pid=$(router_pid routerd) || return 1
    rsh "kill -STOP $pid"
    sleep 25
    # Frozen, the management plane cannot answer, but routing must carry on.
    client curl -s --max-time 10 "http://$INET_IP:8080/marker.txt" | grep -q LAB_ISP_MARKER || { rsh "kill -CONT $pid"; echo "routing stopped while routerd was frozen"; return 1; }
    rsh "kill -CONT $pid"
    wait_for 60 router_healthy || { echo "routerd did not recover after SIGCONT"; return 1; }
    echo "routerd frozen 25 s: routing unaffected, management plane recovered"
}

c_frozen_applyd() {
    python3 "$HERE/scenarios.py" frozen-helper
}

c_kill_everything() {
    local before="$LOGS/firewall-mass-kill-before.json" after="$LOGS/firewall-mass-kill-after.json"
    firewall_snapshot "$before" || return 1
    rsh 'pkill -9 -f "slots/.*/bin/routerd-amd64"; pkill -9 -f "slots/.*/bin/router-applyd-amd64"; pkill -9 dnsmasq; pkill -9 pppd; true'
    wait_for 300 router_healthy || { rsh 'rc-status -a | head -30'; echo "router did not recover from killing every daemon"; return 1; }
    wait_for 300 ppp_up || { echo "PPPoE did not come back"; return 1; }
    wait_for 60 client python3 "$HERE/dns_stress.py" probe "$ROUTER_LAN_IP" github.com public udp || { echo "DNS did not come back"; return 1; }
    firewall_snapshot "$after" || return 1
    python3 "$HERE/firewall_observer.py" "$before" "$after" || return 1
    no_crashes || return 1
    echo "routerd, router-applyd, dnsmasq and pppd killed at once: all back, firewall identical"
}

c_dns_flood() {
    python3 "$HERE/scenarios.py" dns-flood
}

c_wan_syn_flood() {
    command -v hping3 >/dev/null || { echo "hping3 not installed"; return 77; }
    for port in 22 53 8443 3128; do
        isp timeout 8 hping3 -S -p "$port" --faster -c 20000 "$ROUTER_PPP_IP" >/dev/null 2>&1 || true
    done
    sleep 5
    router_healthy || return 1
    ppp_up || { echo "PPPoE dropped during the flood"; return 1; }
    no_crashes || return 1
    echo "SYN floods at WAN ports 22, 53, 8443 and 3128: router healthy, PPPoE up"
}

c_lan_connection_storm() {
    client python3 - "$ROUTER_LAN_IP" <<'PY'
import socket, sys, time
sockets = []
for _ in range(3000):
    try:
        s = socket.create_connection((sys.argv[1], 8443), timeout=2)
        sockets.append(s)
    except OSError:
        break
time.sleep(5)
print(len(sockets), "connections held")
PY
    sleep 5
    router_healthy || { echo "dashboard did not recover after the connection storm"; return 1; }
    echo "thousands of simultaneous connections to the dashboard: it refused the excess and recovered"
}

c_dhcp_churn() {
    local i ok=0
    command -v udhcpc >/dev/null || { echo "udhcpc not installed"; return 77; }
    for i in $(seq 1 60); do
        client ip link add "mv$i" link cl0 type macvlan mode bridge 2>/dev/null
        client ip link set "mv$i" address "$(printf '02:00:00:%02x:%02x:%02x' $((RANDOM % 256)) $((RANDOM % 256)) $i)" up
    done
    for i in $(seq 1 60); do
        client udhcpc -i "mv$i" -n -q -t 2 -T 1 -s /bin/true >/dev/null 2>&1 && ok=$((ok + 1))
    done
    for i in $(seq 1 60); do client ip link del "mv$i" 2>/dev/null; done
    router_healthy || { echo "router unhealthy after DHCP churn"; return 1; }
    rsh 'rc-service dnsmasq status >/dev/null 2>&1' || { echo "dnsmasq not running"; return 1; }
    echo "60 DHCP clients with random MACs: $ok got an offer; dnsmasq and routerd healthy"
}

c_config_churn() {
    local original code i bad=0 start end
    api login >/dev/null || return 1
    original=$(config_json | jq -r .system.hostname)
    start=$(config_json | jq -r .revision)
    for i in $(seq 1 40); do
        (
            code=$(api call PUT config "$(config_json | jq -c ".system.hostname = \"churn-$i\"")" | head -n 1 | awk '{print $2}')
            case "$code" in 200|202|409|422|429) ;; *) echo "$code" >> "$LAB_RUN/churn-bad" ;; esac
        ) &
        [ $((i % 4)) -eq 0 ] && wait
    done
    wait
    [ ! -s "$LAB_RUN/churn-bad" ] || { echo "unexpected statuses: $(sort "$LAB_RUN/churn-bad" | uniq -c | tr '\n' ' ')"; rm -f "$LAB_RUN/churn-bad"; return 1; }
    pending=$(api call GET transactions/pending | tail -n +2 | jq -r .pending)
    if [ "$pending" = true ]; then
        api call POST "transactions/$(api call GET transactions/pending | tail -n +2 | jq -r .id)/confirm" >/dev/null
    fi
    api call PUT config "$(config_json | jq -c ".system.hostname = \"$original\"")" >/dev/null
    end=$(config_json | jq -r .revision)
    [ "$end" -ge "$start" ] || { echo "revision went backwards: $start -> $end"; return 1; }
    router_healthy || return 1
    echo "40 concurrent configuration changes (4 at a time): revision $start -> $end, nothing pending, router healthy"
}

c_disk_pressure() {
    local avail fill code
    avail=$(rsh "df -k /var/lib | awk 'NR==2{print \$4}'")
    fill=$((avail - 12288))
    [ "$fill" -gt 10240 ] || { echo "not enough free space to fill ($avail KiB)"; return 77; }
    rsh "fallocate -l ${fill}K /var/tmp/lab-fill || dd if=/dev/zero of=/var/tmp/lab-fill bs=1M count=$((fill / 1024)) 2>/dev/null; sync"
    sleep 20
    api login >/dev/null 2>&1
    code=$(http_code GET health)
    rsh 'rm -f /var/tmp/lab-fill; sync'
    [ "$code" = 200 ] || { echo "health gave HTTP $code while the disk was almost full"; return 1; }
    sleep 20
    api login >/dev/null || { echo "cannot sign in after the space was freed"; return 1; }
    [ "$(http_code POST snapshots '{"label":"after-fill"}')" = 200 ] || { echo "writes did not resume after the space was freed"; return 1; }
    router_healthy || return 1
    no_crashes || return 1
    echo "disk filled to ~12 MiB free: management stayed up, writes resumed after cleanup"
}

c_reboot_loop() {
    local before after i
    before=$(state_digest)
    for i in 1 2 3; do
        rsh reboot >/dev/null 2>&1
        sleep 25
        wait_for 900 rsh true || { echo "SSH never came back on reboot $i"; return 1; }
        wait_for 600 router_healthy || { echo "router not healthy after reboot $i"; return 1; }
    done
    for i in 1 2; do
        qemu_monitor system_reset
        sleep 25
        wait_for 900 rsh true || { echo "SSH never came back after reset $i"; return 1; }
        wait_for 600 router_healthy || { echo "router not healthy after reset $i"; return 1; }
    done
    wait_for 300 ppp_up || { echo "PPPoE did not come back"; return 1; }
    after=$(state_digest)
    [ "$before" = "$after" ] || { echo "state changed across the reboot loop: $before -> $after"; return 1; }
    echo "3 reboots and 2 power cuts in a row: same state ($after), router healthy"
}

c_memory_after_chaos() {
    local routerd applyd
    routerd=$(daemon_memory_mib routerd)
    applyd=$(daemon_memory_mib router-applyd)
    echo "routerd ${routerd} MiB, router-applyd ${applyd} MiB after the whole chaos run"
    [ "$routerd" -lt 150 ] && [ "$applyd" -lt 60 ]
}

# -------------------------------------------------------------------- main

log "chaos phase start"
api login >/dev/null 2>&1
run "chaos: API fuzzing (hostile configurations and bodies)" c_api_fuzz
run "chaos: hostile HTTP (slowloris, smuggling, bombs, garbage)" c_http_abuse
run "chaos: WAN link flaps" c_wan_link_flaps
run "chaos: LAN link flaps" c_lan_link_flaps
run "chaos: clock jumps" c_clock_jumps
run "chaos: routerd frozen" c_frozen_routerd
run "chaos: privileged helper frozen during a change" c_frozen_applyd
run "chaos: every daemon killed at once" c_kill_everything
run "chaos: DNS flood" c_dns_flood
run "chaos: SYN flood on the WAN address" c_wan_syn_flood
run "chaos: connection storm on the dashboard" c_lan_connection_storm
run "chaos: DHCP churn with random MACs" c_dhcp_churn
run "chaos: concurrent configuration churn" c_config_churn
run "chaos: disk almost full" c_disk_pressure
run "chaos: memory after all of it" c_memory_after_chaos
run "chaos: reboots and power cuts in a row" c_reboot_loop
run "chaos: no crashes in the kernel log" no_crashes

mkdir -p "$LOGS/router-chaos"
rsh 'ip -d addr; nft list ruleset; ps w; free -m; df -h; dmesg | tail -n 200' > "$LOGS/router-chaos/state.txt" 2>&1
for f in routerd.log routerd.err router-applyd.log router-applyd.err; do rsh "tail -n 400 /var/log/$f" > "$LOGS/router-chaos/$f" 2>&1; done
fails=$(grep -c '^FAIL' "$RESULTS")
passes=$(grep -c '^PASS' "$RESULTS")
skips=$(grep -c '^SKIP' "$RESULTS")
log "chaos done: $passes passed, $fails failed, $skips skipped"
if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
    {
        echo "### Chaos results: $passes passed, $fails failed, $skips skipped"
        echo
        echo '| Result | Test | Detail |'
        echo '|---|---|---|'
        awk -F'\t' '{gsub(/\|/, "/", $3); printf "| %s | %s | %s |\n", $1, $2, $3}' "$RESULTS"
    } >> "$GITHUB_STEP_SUMMARY"
fi
[ "$fails" -eq 0 ]
