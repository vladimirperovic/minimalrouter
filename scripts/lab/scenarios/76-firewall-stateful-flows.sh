#!/bin/sh
# 76 — A long-lived TCP transfer survives the nftables regeneration caused by
# a real configuration save; no tautological assertion is allowed.
. "$(dirname "$0")/../lib.sh"
begin "76-firewall-stateful-flows"
phase "3-fault"
require "fault: none (stateful)" ispfault status
phase "4.5-operator"
api_login
original="$(api GET /api/v1/config)"
original_lease="$(echo "$original" | python3 -c 'import json,sys; print(json.load(sys.stdin)["dhcp"]["lease_time"])')"
cleanup_stateful() {
  mr "kill \$(cat /tmp/lab-stateful-server.pid 2>/dev/null) 2>/dev/null || true" >/dev/null 2>&1 || true
  current="$(api GET /api/v1/config 2>/dev/null || true)"
  [ -n "$current" ] || return 0
  restored="$(echo "$current" | python3 -c "import json,sys; c=json.load(sys.stdin); c['dhcp']['lease_time']='$original_lease'; print(json.dumps(c))")"
  api PUT /api/v1/config "$restored" >/dev/null 2>&1 && confirm_pending >/dev/null 2>&1 || true
}
trap cleanup_stateful EXIT HUP INT TERM
require "start slow HTTP stream on router" mr "python3 - <<'PY' >/tmp/lab-stateful-server.log 2>&1 &
import http.server,time
class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200); self.end_headers()
        for _ in range(20):
            self.wfile.write(b'x'*65536); self.wfile.flush(); time.sleep(.5)
    def log_message(self,*args): pass
http.server.ThreadingHTTPServer(('192.168.1.1',19999),Handler).serve_forever()
PY
echo \$! > /tmp/lab-stateful-server.pid"
require "stream endpoint listening" retry 20 mr "ss -tln | grep -q ':19999'"
lan "rm -f /tmp/lab-stateful.bin; curl -fsS --max-time 30 http://192.168.1.1:19999/ -o /tmp/lab-stateful.bin; test \$(wc -c < /tmp/lab-stateful.bin) -eq 1310720" &
FLOW_PID=$!
sleep 3
require "flow established before reload" retry 20 mr "ss -tn | grep -q ':19999'"
check "config save reloads policy during flow" mr_save_lease
wait "$FLOW_PID"
FLOW_RC=$?
check "established flow completed through reload" test "$FLOW_RC" -eq 0

phase "4.5-cleanup"
mr "kill \$(cat /tmp/lab-stateful-server.pid 2>/dev/null) 2>/dev/null || true" >/dev/null 2>&1
current="$(api GET /api/v1/config)"
restore="$(echo "$current" | python3 -c "import json,sys; c=json.load(sys.stdin); c['dhcp']['lease_time']='$original_lease'; print(json.dumps(c))")"
require "original lease setting restored" api PUT /api/v1/config "$restore"
require "restoration confirmed" confirm_pending
trap - EXIT HUP INT TERM
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
