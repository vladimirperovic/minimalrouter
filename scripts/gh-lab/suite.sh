#!/bin/bash
# The lab test suite: runs every test even when one fails, records PASS, FAIL
# or SKIP with a one-line reason, and exits non-zero if anything failed.
# Run it after run.sh has installed the router (see scripts/gh-lab/run.sh).
set -u
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

RESULTS="$LOGS/results.tsv"
: > "$RESULTS"

record() { printf '%s\t%s\t%s\n' "$1" "$2" "$3" >> "$RESULTS"; log "$1  $2  $3"; }

# run NAME FUNCTION : the function's last line of output is the reason.
run() {
    local name=$1 out rc
    shift
    out=$("$@" 2>&1)
    rc=$?
    if [ "$rc" -eq 0 ]; then
        record PASS "$name" "$(printf '%s' "$out" | tail -n 1 | cut -c1-200)"
    elif [ "$rc" -eq 77 ]; then
        record SKIP "$name" "$(printf '%s' "$out" | tail -n 1 | cut -c1-200)"
    else
        record FAIL "$name" "$(printf '%s' "$out" | tail -n 4 | tr '\n' ' ' | cut -c1-400)"
    fi
}

http_code() { api call "$@" | head -n 1 | awk '{print $2}'; }
curl_code() { curl -sk -o /dev/null --max-time 10 -w '%{http_code}' "$@"; }

collect_router_logs() {
    local label=$1 dir="$LOGS/router-$1"
    mkdir -p "$dir"
    rsh 'ip -d addr; echo; ip route; echo; nft list ruleset' > "$dir/network.txt" 2>&1
    rsh 'rc-status -a; echo; ps w; echo; free -m; df -h; uptime' > "$dir/system.txt" 2>&1
    rsh 'dmesg | tail -n 200' > "$dir/dmesg.txt" 2>&1
    for f in routerd.log routerd.err router-applyd.log router-applyd.err messages; do
        rsh "tail -n 400 /var/log/$f" > "$dir/$f" 2>&1
    done
    rsh 'ls -la /var/lib/minimalrouter /var/lib/minimalrouter-applyd /run/minimalrouter' > "$dir/files.txt" 2>&1
}

# ---------------------------------------------------------------- boot and WAN

t_services_ready() {
    rsh 'rc-service router-applyd status >/dev/null 2>&1 && rc-service routerd status >/dev/null 2>&1 && test -s /run/minimalrouter/routerd.ready && ss -lnt | grep -q ":8443 "' || return 1
    echo "both daemons running, ready marker present, dashboard listening on 8443"
}

# A user-space crash shows up in the kernel log. CI once caught OpenSSL (and so
# sshd) segfaulting under emulated AVX2; with KVM and the host CPU this checks
# the real instruction paths.
t_no_crashes_in_dmesg() {
    local bad
    bad=$(rsh 'dmesg | grep -iE "segfault|general protection|invalid opcode|Oops|BUG:|Call Trace|Out of memory|oom-kill|hung task|EXT4-fs error|I/O error"' || true)
    [ -z "$bad" ] || { printf '%s\n' "$bad" | head -n 5; return 1; }
    echo "no segfault, oops, OOM kill or filesystem error in the kernel log"
}

t_sshd_listening() {
    rsh 'rc-service sshd status >/dev/null 2>&1 && ss -lnt | grep -q ":22 "' || { rsh 'rc-service sshd status; dmesg | tail -n 15'; return 1; }
    echo "sshd is running and listening on port 22"
}

t_version() {
    local want got
    want=$(tr -d '\r\n' < VERSION)
    got=$(rsh 'cat /etc/minimalrouter/VERSION' | tr -d '\r\n')
    [ "$got" = "$want" ] || { echo "installed $got, built $want"; return 1; }
    echo "installed version $got matches the build"
}

t_pppoe_session() {
    wait_for 240 rsh 'ip -4 -o addr show ppp0 | grep -q "10\.250\.0\."' || { rsh 'ip -o addr; tail -n 40 /var/log/messages'; return 1; }
    rsh 'ip -4 -o addr show ppp0' | awk '{print "ppp0 " $4}'
}

t_default_route() {
    rsh 'ip route show default' | grep -q 'dev ppp0' || { rsh 'ip route'; return 1; }
    echo "default route goes through ppp0"
}

t_ping_provider() { rsh "ping -c3 -W2 $ISP_PPP_IP" | tail -n 1; }

t_internet_through_nat() {
    rsh 'ping -c3 -W3 1.1.1.1' | tail -n 1
}

# ---------------------------------------------------------------------- LAN

t_lan_dhcp_dns_nat() {
    # AppArmor confines dhclient to these paths.
    local leases=/var/lib/dhcp/dhclient-lab.leases ip
    sudo rm -f "$leases"
    client dhclient -1 -4 -pf /run/dhclient-lab.pid -lf "$leases" cl0 >"$LOGS/dhclient.log" 2>&1 || { cat "$LOGS/dhclient.log"; return 1; }
    ip=$(client ip -4 -o addr show cl0 | awk '{print $4}' | cut -d/ -f1)
    [ -n "$ip" ] || { echo "no DHCP lease"; return 1; }
    sudo grep -q "option routers $ROUTER_LAN_IP;" "$leases" || { sudo cat "$leases"; echo "lease has no router option"; return 1; }
    sudo grep -q "option domain-name-servers $ROUTER_LAN_IP" "$leases" || { sudo cat "$leases"; echo "lease has no DNS option"; return 1; }
    client ip route replace default via "$ROUTER_LAN_IP" dev cl0
    echo "$ip" > "$LAB_RUN/client.ip"
    echo "client leased $ip with the router as gateway and DNS"
}

t_lan_dns_answers() {
    local out
    out=$(client dig +short +time=5 +tries=2 "@$ROUTER_LAN_IP" github.com A)
    [ -n "$out" ] || { echo "no answer from the router's DNS"; return 1; }
    echo "router DNS answered github.com: $(printf '%s' "$out" | head -n 1)"
}

t_lan_nat_to_provider() {
    local body src
    body=$(client curl -s --max-time 10 "http://$ISP_PPP_IP:8080/marker.txt")
    [ "$body" = LAB_ISP_MARKER ] || { echo "client could not fetch through the router: '$body'"; return 1; }
    src=$(grep 'marker.txt' "$LOGS/isp-http.log" | tail -n 1 | awk '{print $1}')
    [ "$src" = "$ROUTER_PPP_IP" ] || { echo "provider saw source $src, expected NAT to $ROUTER_PPP_IP"; return 1; }
    echo "LAN client reached the provider; the provider saw only $src (NAT)"
}

t_lan_services_reachable() {
    local code
    code=$(client curl -sk -o /dev/null --max-time 10 -w '%{http_code}' -H "Origin: $ROUTER_URL" "$ROUTER_URL/api/v1/setup/status")
    [ "$code" = 200 ] || { echo "dashboard API from the client gave $code"; return 1; }
    client bash -c "exec 3<>/dev/tcp/$ROUTER_LAN_IP/22" 2>/dev/null || { echo "SSH not reachable from the LAN client"; return 1; }
    echo "dashboard and SSH reachable from the LAN"
}

# ----------------------------------------------------------- firewall (WAN)

t_wan_ports_closed() {
    local port open=""
    for port in 22 23 53 80 443 3128 8080 8443 51820; do
        if isp nc -z -w2 "$ROUTER_PPP_IP" "$port" 2>/dev/null; then open="$open $port"; fi
    done
    [ -z "$open" ] || { echo "open on the WAN address:$open"; return 1; }
    echo "management, DNS, proxy and VPN ports are closed on the WAN address"
}

t_wan_full_port_scan() {
    local out
    out=$(isp nmap -Pn -n -T4 --min-rate 3000 -p- "$ROUTER_PPP_IP" 2>&1)
    printf '%s\n' "$out" > "$LOGS/nmap-wan.txt"
    if printf '%s' "$out" | grep -E '^[0-9]+/tcp +open' >/dev/null; then
        printf '%s' "$out" | grep -E '^[0-9]+/tcp +open'
        return 1
    fi
    echo "no open TCP port on the WAN address (all 65535 scanned)"
}

t_wan_udp_probe() {
    local out
    out=$(isp nmap -Pn -n -sU -T4 -p 53,67,68,123,161,500,1900,5353,51820 "$ROUTER_PPP_IP" 2>&1)
    printf '%s\n' "$out" > "$LOGS/nmap-wan-udp.txt"
    if printf '%s' "$out" | grep -E '^[0-9]+/udp +open( |$)' >/dev/null; then
        printf '%s' "$out" | grep -E '^[0-9]+/udp +open( |$)'
        return 1
    fi
    echo "no UDP service answers on the WAN address"
}

t_wan_cannot_reach_lan() {
    isp ip route replace 192.168.1.0/24 via "$ROUTER_PPP_IP" 2>/dev/null || isp ip route replace 192.168.1.0/24 dev ppp0
    if isp ping -c3 -W2 "$HOST_LAN_IP" >/dev/null 2>&1; then echo "the provider side reached $HOST_LAN_IP through the router"; return 1; fi
    if isp nc -z -w2 "$(cat "$LAB_RUN/client.ip" 2>/dev/null || echo 192.168.1.100)" 22 2>/dev/null; then echo "the provider side reached a LAN host"; return 1; fi
    isp ip route del 192.168.1.0/24 2>/dev/null
    echo "traffic from the WAN side is not forwarded into the LAN"
}

t_wan_spoofed_lan_source() {
    # A packet that claims a LAN source address but arrives on the WAN link.
    isp ip route replace 192.168.1.0/24 via "$ROUTER_PPP_IP" 2>/dev/null || true
    isp ip addr add 192.168.1.77/32 dev lo 2>/dev/null || true
    if isp ping -c3 -W2 -I 192.168.1.77 "$ROUTER_LAN_IP" >/dev/null 2>&1; then
        isp ip addr del 192.168.1.77/32 dev lo 2>/dev/null; isp ip route del 192.168.1.0/24 2>/dev/null
        echo "the router answered a LAN-sourced packet that came from the WAN"
        return 1
    fi
    isp ip addr del 192.168.1.77/32 dev lo 2>/dev/null; isp ip route del 192.168.1.0/24 2>/dev/null
    echo "a spoofed LAN source on the WAN link got no answer"
}

# --------------------------------------------------------------- API surface

t_auth_basics() {
    local code
    code=$(curl_code -H "Origin: $ROUTER_URL" "$ROUTER_URL/api/v1/config")
    [ "$code" = 401 ] || { echo "unauthenticated config read gave $code, expected 401"; return 1; }
    code=$(api wrong 1 | awk '{print $1}')
    [ "$code" = 401 ] || { echo "wrong password gave $code, expected 401"; return 1; }
    api login >/dev/null || return 1
    [ "$(http_code GET auth/session)" = 200 ] || { echo "no session after login"; return 1; }
    echo "unauthenticated 401, wrong password 401, correct password opens a session"
}

t_api_reads() {
    local path bad=""
    api reads > "$LAB_RUN/reads.txt"
    while read -r path; do
        [ "$(http_code GET "$path")" = 200 ] || bad="$bad $path"
    done < "$LAB_RUN/reads.txt"
    [ -z "$bad" ] || { echo "not HTTP 200:$bad"; return 1; }
    echo "$(wc -l < "$LAB_RUN/reads.txt") read endpoints all answer 200"
}

t_http_hygiene() {
    local cookie csrf code
    cookie=$(jq -r .cookie "$LAB_RUN/session.json")
    csrf=$(jq -r .csrf "$LAB_RUN/session.json")
    code=$(curl_code -X POST -H "Origin: $ROUTER_URL" -H "Cookie: $cookie" -H 'Content-Type: application/json' -d '{}' "$ROUTER_URL/api/v1/snapshots")
    [ "$code" = 403 ] || { echo "POST without a CSRF token gave $code, expected 403"; return 1; }
    code=$(curl_code -X POST -H 'Origin: https://evil.example' -H "Cookie: $cookie" -H "X-CSRF-Token: $csrf" -H 'Content-Type: application/json' -d '{}' "$ROUTER_URL/api/v1/snapshots")
    [ "$code" = 403 ] || { echo "POST from a foreign Origin gave $code, expected 403"; return 1; }
    code=$(curl_code -H 'Host: evil.example' -H "Cookie: $cookie" "$ROUTER_URL/api/v1/health")
    case "$code" in 4*) ;; *) echo "foreign Host header gave $code, expected 4xx"; return 1 ;; esac
    code=$(curl_code -X DELETE -H "Origin: $ROUTER_URL" -H "Cookie: $cookie" -H "X-CSRF-Token: $csrf" "$ROUTER_URL/api/v1/health")
    [ "$code" = 405 ] || { echo "DELETE on a read-only route gave $code, expected 405"; return 1; }
    code=$(curl_code -X POST -H "Origin: $ROUTER_URL" -H 'Content-Type: application/json' -d '{bad json' "$ROUTER_URL/api/v1/auth/login")
    case "$code" in 400|401|422) ;; *) echo "malformed JSON gave $code"; return 1 ;; esac
    code=$(head -c 6000000 /dev/zero | tr '\0' 'a' | curl_code -X POST -H "Origin: $ROUTER_URL" -H 'Content-Type: application/json' --data-binary @- "$ROUTER_URL/api/v1/auth/login")
    case "$code" in 400|413) ;; *) echo "6 MB body gave $code, expected 413"; return 1 ;; esac
    echo "CSRF, Origin, Host, method, malformed and oversized requests are all refused"
}

t_security_headers() {
    local headers
    headers=$(curl -skI --max-time 10 "$ROUTER_URL/")
    printf '%s' "$headers" | grep -qi '^x-content-type-options: *nosniff' || { echo "no X-Content-Type-Options: nosniff"; return 1; }
    printf '%s' "$headers" | grep -qi '^content-security-policy:' || { echo "no Content-Security-Policy"; return 1; }
    printf '%s' "$headers" | grep -qi '^x-frame-options:\|frame-ancestors' || { echo "no clickjacking protection"; return 1; }
    echo "nosniff, CSP and framing protection present"
}

t_tls_policy() {
    if curl -sk --tlsv1.1 --tls-max 1.1 --max-time 8 -o /dev/null "$ROUTER_URL/" 2>/dev/null; then echo "TLS 1.1 was accepted"; return 1; fi
    curl -sk --tlsv1.2 --max-time 8 -o /dev/null "$ROUTER_URL/" || { echo "TLS 1.2 was refused"; return 1; }
    echo "TLS 1.1 refused, TLS 1.2 and up accepted"
}

t_login_rate_limit() {
    local codes
    codes=$(client env ROUTER_URL="$ROUTER_URL" LAB_RUN="$LAB_RUN" python3 "$(dirname "${BASH_SOURCE[0]}")/labapi.py" wrong 25)
    printf '%s' "$codes" | grep -q 429 || { echo "25 wrong logins never got 429: $codes"; return 1; }
    echo "repeated wrong logins are rate limited (429)"
}

# ------------------------------------------------- configuration and rollback

config_json() { api call GET config | tail -n +2; }

t_snapshot() {
    [ "$(http_code POST snapshots '{"label":"lab-snapshot"}')" = 200 ] || { echo "snapshot not created"; return 1; }
    api call GET snapshots | grep -q lab-snapshot || { echo "snapshot missing from the list"; return 1; }
    echo "manual snapshot created and listed"
}

t_safe_config_change() {
    local before after cfg code
    before=$(config_json | jq -r .revision)
    cfg=$(config_json | jq -c '.system.hostname = "lab-router"')
    code=$(http_code PUT config "$cfg")
    case "$code" in
        200) ;;
        202) api call POST "transactions/$(api call GET transactions/pending | tail -n +2 | jq -r .id)/confirm" >/dev/null ;;
        *) echo "hostname change gave HTTP $code"; return 1 ;;
    esac
    after=$(config_json | jq -r .revision)
    [ "$(config_json | jq -r .system.hostname)" = lab-router ] || { echo "hostname not applied"; return 1; }
    [ "$after" -gt "$before" ] || { echo "revision did not increase ($before -> $after)"; return 1; }
    [ "$(rsh hostname | tr -d '\r\n')" = lab-router ] || { echo "the running system did not take the hostname"; return 1; }
    echo "hostname change applied: revision $before -> $after, running system updated"
}

t_confirm_flow() {
    local cfg code id
    cfg=$(config_json | jq -c '.trusted_networks += ["192.168.77.0/24"]')
    code=$(http_code PUT config "$cfg")
    [ "$code" = 202 ] || { echo "trusted-network change gave HTTP $code, expected 202 (confirmation required)"; return 1; }
    id=$(api call GET transactions/pending | tail -n +2 | jq -r .id)
    [ -n "$id" ] && [ "$id" != null ] || { echo "no pending transaction"; return 1; }
    [ "$(http_code POST "transactions/$id/confirm")" = 200 ] || { echo "confirm failed"; return 1; }
    config_json | jq -e '.trusted_networks | index("192.168.77.0/24")' >/dev/null || { echo "confirmed change not persisted"; return 1; }
    echo "provisional change confirmed and persisted"
}

t_rollback_unconfirmed() {
    local cfg code revision i pending
    revision=$(config_json | jq -r .revision)
    cfg=$(config_json | jq -c '.trusted_networks -= ["192.168.77.0/24"]')
    code=$(http_code PUT config "$cfg")
    [ "$code" = 202 ] || { echo "change gave HTTP $code, expected 202"; return 1; }
    for i in $(seq 1 90); do
        pending=$(api call GET transactions/pending | tail -n +2 | jq -r .pending)
        [ "$pending" = false ] && break
        sleep 5
    done
    [ "$pending" = false ] || { echo "the unconfirmed change never rolled back"; return 1; }
    config_json | jq -e '.trusted_networks | index("192.168.77.0/24")' >/dev/null || { echo "rollback did not restore the previous configuration"; return 1; }
    [ "$(config_json | jq -r .revision)" -ge "$revision" ] || { echo "revision went backwards"; return 1; }
    t_services_ready >/dev/null || return 1
    echo "an unconfirmed change rolled back on its own and the router stayed reachable"
}

# ---------------------------------------------------------------- resilience

firewall_fingerprint() { rsh 'nft list ruleset | sha256sum | cut -c1-16'; }

t_kill_routerd() {
    local old new fw
    fw=$(firewall_fingerprint)
    old=$(router_pid routerd)
    rsh "kill -9 $old"
    wait_for 120 rsh 'test -s /run/minimalrouter/routerd.ready && ss -lnt | grep -q ":8443 "' || { echo "dashboard did not come back"; return 1; }
    new=$(router_pid routerd)
    [ -n "$new" ] && [ "$new" != "$old" ] || { echo "routerd was not restarted (pid $old -> $new)"; return 1; }
    api login >/dev/null || { echo "cannot sign in after the restart"; return 1; }
    [ "$(firewall_fingerprint)" = "$fw" ] || { echo "firewall rules changed while routerd restarted"; return 1; }
    rsh 'ip -4 -o addr show ppp0 | grep -q "10\.250\.0\."' || { echo "PPPoE dropped"; return 1; }
    echo "routerd restarted (pid $old -> $new); firewall and PPPoE untouched"
}

t_kill_applyd() {
    local old new fw
    fw=$(firewall_fingerprint)
    old=$(router_pid router-applyd)
    rsh "kill -9 $old"
    wait_for 180 rsh 'test -S /run/minimalrouter/apply.sock && test -s /run/minimalrouter/routerd.ready' || { echo "privileged helper did not come back"; return 1; }
    new=$(router_pid router-applyd)
    [ -n "$new" ] && [ "$new" != "$old" ] || { echo "router-applyd was not restarted (pid $old -> $new)"; return 1; }
    [ "$(firewall_fingerprint)" = "$fw" ] || { echo "firewall rules changed after the helper restarted"; return 1; }
    [ "$(http_code GET health)" = 200 ] || { echo "API unhealthy after the helper restart"; return 1; }
    echo "router-applyd restarted (pid $old -> $new); firewall identical"
}

t_pppoe_flap() {
    isp pkill -x pppd || { echo "no provider pppd to kill"; return 1; }
    wait_for 60 rsh '! ip -4 -o addr show ppp0 2>/dev/null | grep -q "10\.250\.0\."' || echo "(link did not drop visibly)"
    wait_for 240 rsh 'ip -4 -o addr show ppp0 | grep -q "10\.250\.0\."' || { rsh 'tail -n 60 /var/log/messages'; echo "PPPoE did not redial"; return 1; }
    wait_for 60 client curl -s --max-time 5 "http://$ISP_PPP_IP:8080/marker.txt" || { echo "LAN traffic did not recover after the redial"; return 1; }
    echo "PPPoE redialled after the provider dropped the session; LAN traffic flows again"
}

t_provider_vanishes() {
    # The concentrator disappears entirely for a while, then returns.
    sudo pkill -f 'pppoe-server -F -I isp0' || true
    isp pkill -x pppd || true
    sleep 45
    sudo ip netns exec isp bash -c "nohup pppoe-server -F -I isp0 -k -L $ISP_PPP_IP -R $ROUTER_PPP_IP -N 8 -S isp-lab -C isp-lab -O /etc/ppp/pppoe-lab.options >>'$LOGS/pppoe-server.log' 2>&1 &"
    wait_for 360 rsh 'ip -4 -o addr show ppp0 | grep -q "10\.250\.0\."' || { rsh 'tail -n 60 /var/log/messages'; echo "no redial after the provider came back"; return 1; }
    [ "$(http_code GET health)" = 200 ] || return 1
    echo "the router redialled after the provider was unreachable for 45 s"
}

state_digest() { echo "revision=$(config_json | jq -r .revision) hostname=$(config_json | jq -r .system.hostname)"; }

t_warm_reboot() {
    local before after
    before=$(state_digest)
    rsh reboot >/dev/null 2>&1
    sleep 20
    wait_for 900 rsh true || { echo "SSH never came back"; return 1; }
    wait_for 600 rsh 'test -s /run/minimalrouter/routerd.ready && ss -lnt | grep -q ":8443 "' || { echo "dashboard did not come back"; return 1; }
    wait_for 300 rsh 'ip -4 -o addr show ppp0 | grep -q "10\.250\.0\."' || { echo "PPPoE did not come back"; return 1; }
    api login >/dev/null || return 1
    after=$(state_digest)
    [ "$before" = "$after" ] || { echo "state changed across the reboot: $before -> $after"; return 1; }
    echo "after a reboot everything is back with the same state ($after)"
}

t_hard_reset() {
    local before after
    before=$(state_digest)
    qemu_monitor system_reset
    sleep 20
    wait_for 900 rsh true || { echo "SSH never came back after the reset"; return 1; }
    wait_for 600 rsh 'test -s /run/minimalrouter/routerd.ready && ss -lnt | grep -q ":8443 "' || { echo "dashboard did not come back"; return 1; }
    wait_for 300 rsh 'ip -4 -o addr show ppp0 | grep -q "10\.250\.0\."' || { echo "PPPoE did not come back"; return 1; }
    api login >/dev/null || return 1
    after=$(state_digest)
    [ "$before" = "$after" ] || { echo "state changed across the power cut: $before -> $after"; return 1; }
    rsh 'dmesg | grep -iE "EXT4-fs error|I/O error|corrupt"' && { echo "filesystem errors after the reset"; return 1; }
    echo "after an abrupt reset the router recovered with the same state ($after)"
}

# ----------------------------------------------------------- recovery tools

t_recovery_tools() {
    rsh 'router-recovery --help' 2>&1 | grep -q 'Usage: router-recovery' || { echo "router-recovery gave no usage text"; return 1; }
    rsh 'router-update status' | jq -e '.current != null' >/dev/null || { echo "router-update status is not JSON with a current slot"; return 1; }
    echo "router-recovery and router-update respond over SSH"
}

# ------------------------------------------------------ memory, CPU and soak

sample_memory() {
    rsh sh -s "$1" <<'EOF'
label=$1
for d in router-applyd routerd; do
  p=$(pgrep -f "slots/.*/bin/$d-amd64" | head -n 1)
  [ -n "$p" ] || { echo "sample $label $d missing"; continue; }
  rss=$(awk '$1=="VmRSS:"{print $2}' /proc/$p/status)
  hwm=$(awk '$1=="VmHWM:"{print $2}' /proc/$p/status)
  anon=$(awk '$1=="RssAnon:"{print $2}' /proc/$p/status)
  thr=$(awk '$1=="Threads:"{print $2}' /proc/$p/status)
  ticks=$(awk '{print $14+$15}' /proc/$p/stat)
  echo "sample $label $d pid=$p rss=$rss hwm=$hwm anon=$anon threads=$thr ticks=$ticks"
done
echo "sample $label system mem_available=$(awk '$1=="MemAvailable:"{print $2}' /proc/meminfo)"
EOF
}

field() { printf '%s' "$1" | grep -o "$2=[0-9]*" | head -n 1 | cut -d= -f2; }

t_memory_and_cpu() {
    local idle1 idle2 load1 load2 rss_idle rss_load hwm t1 t2 ticks
    sleep 60
    idle1=$(sample_memory idle_start | grep ' routerd ')
    sleep 120
    idle2=$(sample_memory idle_end)
    echo "$idle2" >> "$LOGS/memory.txt"
    rss_idle=$(field "$(printf '%s' "$idle2" | grep ' routerd ')" rss)
    t1=$(field "$idle1" ticks)
    t2=$(field "$(printf '%s' "$idle2" | grep ' routerd ')" ticks)
    ticks=$((t2 - t1))
    api login >/dev/null || return 1
    api load 120 4 > "$LOGS/load.json"
    load1=$(sample_memory after_load)
    echo "$load1" >> "$LOGS/memory.txt"
    rss_load=$(field "$(printf '%s' "$load1" | grep ' routerd ')" rss)
    hwm=$(field "$(printf '%s' "$load1" | grep ' routerd ')" hwm)
    sleep 60
    sample_memory settled >> "$LOGS/memory.txt"
    echo "routerd idle=$((rss_idle / 1024)) MiB, after load=$((rss_load / 1024)) MiB, peak=$((hwm / 1024)) MiB, idle CPU=${ticks} ticks in 120 s, load: $(jq -c '{requests,errors,status}' "$LOGS/load.json")" | tee "$LOGS/memory-summary.txt"
    [ "$(jq .errors "$LOGS/load.json")" = 0 ] || { echo "requests failed under load"; return 1; }
    [ "$((rss_idle / 1024))" -lt 100 ] || { echo "routerd idle memory above 100 MiB"; return 1; }
    [ "$((hwm / 1024))" -lt 200 ] || { echo "routerd peak memory above 200 MiB"; return 1; }
    [ "$ticks" -lt 240 ] || { echo "idle CPU above 2 percent of a core"; return 1; }
    cat "$LOGS/memory-summary.txt"
}

t_soak() {
    local pid_d pid_a start end i rss_start rss_end code
    pid_d=$(router_pid routerd)
    pid_a=$(router_pid router-applyd)
    rss_start=$(field "$(sample_memory soak_start | grep ' routerd ')" rss)
    api login >/dev/null || return 1
    end=$((SECONDS + 300))
    i=0
    while [ "$SECONDS" -lt "$end" ]; do
        api load 15 2 >/dev/null
        code=$(http_code POST snapshots "{\"label\":\"soak-$i\"}")
        [ "$code" = 200 ] || { echo "snapshot during the soak gave $code"; return 1; }
        i=$((i + 1))
    done
    rss_end=$(field "$(sample_memory soak_end | grep ' routerd ')" rss)
    [ "$(router_pid routerd)" = "$pid_d" ] || { echo "routerd restarted during the soak"; return 1; }
    [ "$(router_pid router-applyd)" = "$pid_a" ] || { echo "router-applyd restarted during the soak"; return 1; }
    [ "$(((rss_end - rss_start) / 1024))" -lt 25 ] || { echo "routerd grew by $(((rss_end - rss_start) / 1024)) MiB in 5 minutes"; return 1; }
    echo "5 minute soak: no restart, memory $((rss_start / 1024)) -> $((rss_end / 1024)) MiB, $i rounds"
}

t_throughput() {
    isp bash -c "nohup iperf3 -s -1 >'$LOGS/iperf-server.log' 2>&1 &"
    sleep 2
    local mbit
    mbit=$(client iperf3 -c "$ISP_PPP_IP" -t 10 -J 2>/dev/null | jq '.end.sum_received.bits_per_second / 1e6 | floor')
    [ -n "$mbit" ] && [ "$mbit" != null ] || { echo "iperf3 gave no result"; return 1; }
    echo "LAN client to provider through NAT and PPPoE: ${mbit} Mbit/s (software-emulated NICs, informational)"
}

# ---------------------------------------------------------------------- main

log "suite start"
run "services ready" t_services_ready
run "installed version matches the build" t_version
run "sshd running" t_sshd_listening
run "no crashes in the kernel log (after boot)" t_no_crashes_in_dmesg
run "PPPoE session comes up" t_pppoe_session
run "default route via PPPoE" t_default_route
run "provider reachable over PPP" t_ping_provider
run "internet reachable through the provider NAT" t_internet_through_nat
run "LAN DHCP lease with router and DNS options" t_lan_dhcp_dns_nat
run "LAN DNS answers through the router" t_lan_dns_answers
run "LAN client NAT through PPPoE" t_lan_nat_to_provider
run "LAN reaches dashboard and SSH" t_lan_services_reachable
run "WAN: management ports closed" t_wan_ports_closed
run "WAN: full TCP port scan" t_wan_full_port_scan
run "WAN: UDP probe" t_wan_udp_probe
run "WAN cannot reach the LAN" t_wan_cannot_reach_lan
run "WAN: spoofed LAN source refused" t_wan_spoofed_lan_source
run "API authentication basics" t_auth_basics
run "every read endpoint answers 200" t_api_reads
run "HTTP hygiene: CSRF, Origin, Host, method, size" t_http_hygiene
run "security headers" t_security_headers
run "TLS policy" t_tls_policy
run "login rate limit" t_login_rate_limit
run "snapshot create and list" t_snapshot
run "safe configuration change" t_safe_config_change
run "provisional change confirmed" t_confirm_flow
run "unconfirmed change rolls back" t_rollback_unconfirmed
run "recovery tools respond" t_recovery_tools
run "kill -9 routerd" t_kill_routerd
run "kill -9 router-applyd" t_kill_applyd
run "PPPoE redials after a dropped session" t_pppoe_flap
run "PPPoE redials after the provider vanishes" t_provider_vanishes
run "memory and idle CPU under load" t_memory_and_cpu
run "five minute soak" t_soak
run "throughput through NAT and PPPoE" t_throughput
run "warm reboot" t_warm_reboot
run "abrupt reset (power cut)" t_hard_reset

run "no crashes in the kernel log (after the whole suite)" t_no_crashes_in_dmesg
collect_router_logs final
fails=$(grep -c '^FAIL' "$RESULTS")
passes=$(grep -c '^PASS' "$RESULTS")
skips=$(grep -c '^SKIP' "$RESULTS")
log "suite done: $passes passed, $fails failed, $skips skipped"
if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
    {
        echo "### Lab results: $passes passed, $fails failed, $skips skipped"
        echo
        echo '| Result | Test | Detail |'
        echo '|---|---|---|'
        awk -F'\t' '{gsub(/\|/, "/", $3); printf "| %s | %s | %s |\n", $1, $2, $3}' "$RESULTS"
    } >> "$GITHUB_STEP_SUMMARY"
fi
[ "$fails" -eq 0 ]
