#!/bin/sh
set -eu

API="https://192.168.1.1:8443"
PASSWD="${MINIMALROUTER_TEST_PASSWORD:-SuperSecure12345678}"
OUT="${MINIMALROUTER_DASHBOARD_TEST_OUTPUT:-/tmp/dashboard-test.txt}"
umask 077
echo "========================================" > "$OUT"
echo "  MINIMAL ROUTER OS — DASHBOARD E2E TEST" >> "$OUT"
echo "========================================" >> "$OUT"
echo "" >> "$OUT"

echo "=== 1. LOGIN ===" >> $OUT
LOGIN_RESP=$(curl -sk -X POST $API/api/v1/auth/login -H 'Content-Type: application/json' -d "{\"password\":\"$PASSWD\"}" -c /tmp/cookies.txt -w '\n%{http_code}')
LOGIN_CODE=$(echo "$LOGIN_RESP" | tail -1)
echo "HTTP: $LOGIN_CODE" >> $OUT
LOGIN_SUCCESS=$(echo "$LOGIN_RESP" | head -1 | jq -r '.success // false' 2>/dev/null)
echo "success: $LOGIN_SUCCESS" >> $OUT
echo "" >> $OUT

echo "=== 2. CSRF TOKEN ===" >> "$OUT"
CSRF=$(curl -sk $API/api/v1/auth/session -b /tmp/cookies.txt 2>/dev/null | jq -r '.csrf_token // ""')
echo "csrf_token: present=$([ -n "$CSRF" ] && echo yes || echo no) (len=${#CSRF})" >> "$OUT"
echo "" >> "$OUT"

echo "=== 3. DASHBOARD HTML ===" >> $OUT
DASH_HTTP=$(curl -sk $API/ -b /tmp/cookies.txt -o /tmp/dash.html -w '%{http_code}' 2>/dev/null)
DASH_SIZE=$(wc -c < /tmp/dash.html 2>/dev/null)
DASH_TITLE=$(grep -o '<title>[^<]*</title>' /tmp/dash.html 2>/dev/null)
echo "HTTP: $DASH_HTTP" >> $OUT
echo "Size: $DASH_SIZE bytes" >> $OUT
echo "Title: $DASH_TITLE" >> $OUT
echo "" >> $OUT

echo "=== 4. JS ASSET ===" >> $OUT
JS_FILE=$(grep -o '/assets/index-[^"]*\.js' /tmp/dash.html 2>/dev/null | head -1)
JS_HTTP=$(curl -sk $API$JS_FILE -b /tmp/cookies.txt -o /tmp/dash.js -w '%{http_code}' 2>/dev/null)
JS_SIZE=$(wc -c < /tmp/dash.js 2>/dev/null)
echo "File: $JS_FILE" >> $OUT
echo "HTTP: $JS_HTTP" >> $OUT
echo "Size: $JS_SIZE bytes" >> $OUT
echo "" >> $OUT

echo "=== 5. CSS ASSET ===" >> $OUT
CSS_FILE=$(grep -o '/assets/index-[^"]*\.css' /tmp/dash.html 2>/dev/null | head -1)
CSS_HTTP=$(curl -sk $API$CSS_FILE -b /tmp/cookies.txt -o /tmp/dash.css -w '%{http_code}' 2>/dev/null)
CSS_SIZE=$(wc -c < /tmp/dash.css 2>/dev/null)
echo "File: $CSS_FILE" >> $OUT
echo "HTTP: $CSS_HTTP" >> $OUT
echo "Size: $CSS_SIZE bytes" >> $OUT
echo "" >> $OUT

echo "=== 6. CONFIG API ===" >> $OUT
CFG=$(curl -sk $API/api/v1/config -b /tmp/cookies.txt -H "X-CSRF-Token: $CSRF" 2>/dev/null)
echo "revision: $(echo "$CFG" | jq -r '.revision')" >> $OUT
echo "hostname: $(echo "$CFG" | jq -r '.system.hostname')" >> $OUT
echo "wan_input: $(echo "$CFG" | jq -r '.firewall.default_wan_input_policy')" >> $OUT
echo "wan_ingress: $(echo "$CFG" | jq -r '.firewall.wan_ingress_mode')" >> $OUT
echo "port_forwards: $(echo "$CFG" | jq -r '.firewall.port_forwards | length')" >> $OUT
echo "lan_ip: $(echo "$CFG" | jq -r '.lan.ip_address')" >> $OUT
echo "wg_enabled: $(echo "$CFG" | jq -r '.wireguard.enabled')" >> $OUT
echo "" >> $OUT

echo "=== 7. SYSTEM API ===" >> $OUT
SYS=$(curl -sk $API/api/v1/system -b /tmp/cookies.txt -H "X-CSRF-Token: $CSRF" 2>/dev/null)
echo "hostname: $(echo "$SYS" | jq -r '.hostname')" >> $OUT
echo "ram_used_mb: $(echo "$SYS" | jq -r '.ram_used_bytes / 1048576')" >> $OUT
echo "disk_used_mb: $(echo "$SYS" | jq -r '.disk_used_bytes / 1048576')" >> $OUT
echo "uptime: $(echo "$SYS" | jq -r '.uptime_seconds // 0')s" >> $OUT
echo "installed_packages: $(echo "$SYS" | jq -r '.installed_packages // "null"')" >> $OUT
echo "" >> $OUT

echo "=== 8. AUDIT EVENTS ===" >> $OUT
AUDIT=$(curl -sk $API/api/v1/audit/events -b /tmp/cookies.txt -H "X-CSRF-Token: $CSRF" 2>/dev/null)
echo "events_count: $(echo "$AUDIT" | jq -r 'if type == "array" then length else .events | length end')" >> $OUT
echo "" >> $OUT

echo "=== 9. NFTABLES FIREWALL ===" >> $OUT
echo "input_policy: $(nft list ruleset 2>/dev/null | grep 'policy drop' | head -1 | tr -d '\t')" >> $OUT
echo "wan_bogon_drop: $(nft list ruleset 2>/dev/null | grep -c 'eth0.*drop')" >> $OUT
echo "ipv6_drop: $(nft list ruleset 2>/dev/null | grep -c 'nfproto ipv6 drop')" >> $OUT
echo "urpf: $(nft list ruleset 2>/dev/null | grep -c 'fib saddr')" >> $OUT
echo "" >> $OUT

echo "=== 10. LISTENING PORTS ===" >> $OUT
ss -lntup 2>/dev/null >> $OUT
echo "" >> $OUT

echo "=== 11. SERVICE STATUS ===" >> $OUT
rc-service routerd status >> $OUT 2>&1
rc-service router-applyd status >> $OUT 2>&1
echo "" >> $OUT

echo "=== 12. RESOURCE USAGE ===" >> $OUT
echo "VM RAM (free): $(free -m 2>/dev/null | grep Mem | awk '{print $3}') MB used / $(free -m 2>/dev/null | grep Mem | awk '{print $2}') MB total" >> $OUT
echo "VM Disk: $(df -m / 2>/dev/null | tail -1 | awk '{print $3}') MB used / $(df -m / 2>/dev/null | tail -1 | awk '{print $2}') MB total" >> $OUT
echo "Packages: $(apk list --installed 2>/dev/null | wc -l)" >> $OUT
echo "" >> $OUT

echo "========================================" >> $OUT
echo "  TEST COMPLETE" >> $OUT
echo "========================================" >> $OUT

cat /tmp/dashboard-test.txt
