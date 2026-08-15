#!/bin/sh
# 78 — A real preshared key is applied to both WireGuard peers, appears in
# runtime, establishes a fresh handshake, and is then removed from both ends.
. "$(dirname "$0")/../lib.sh"
begin "78-wg-preshared-key"
phase "3-fault"
require "fault: none (wg psk)" ispfault status
require "SIM-LAB WireGuard peer available" sim "wg show wg0 >/dev/null"
phase "4.5-operator"
api_login
psk="$(sim 'wg genpsk')"
mr_pub="$(mr 'wg show wg0 public-key' | tr -d ' \n')"
cfg="$(api GET /api/v1/config)"
peer_pub="$(echo "$cfg" | python3 -c 'import json,sys; p=next(x for x in json.load(sys.stdin)["wireguard"]["peers"] if x.get("enabled")); print(p["public_key"])')"
cleanup_psk() {
  current="$(api GET /api/v1/config 2>/dev/null || true)"
  if [ -n "$current" ]; then
    clean_cfg="$(echo "$current" | python3 -c "import json,sys; c=json.load(sys.stdin); p=next((x for x in c['wireguard']['peers'] if x.get('public_key')=='$peer_pub'),None); p is not None and p.update({'preshared_key':''}); print(json.dumps(c))")"
    api PUT /api/v1/config "$clean_cfg" >/dev/null 2>&1 && confirm_pending >/dev/null 2>&1 || true
  fi
  sim "wg set wg0 peer '$mr_pub' preshared-key /dev/null; rm -f /tmp/lab-wg.psk" >/dev/null 2>&1 || true
}
trap cleanup_psk EXIT HUP INT TERM
new="$(echo "$cfg" | python3 -c "import json,sys; c=json.load(sys.stdin); p=next(x for x in c['wireguard']['peers'] if x.get('public_key')=='$peer_pub'); p['preshared_key']='$psk'; print(json.dumps(c))")"
require "preshared key saved on router" api PUT /api/v1/config "$new"
require "preshared-key save confirmed" confirm_pending
require "preshared key installed on remote peer" sim "printf '%s' '$psk' > /tmp/lab-wg.psk; chmod 600 /tmp/lab-wg.psk; wg set wg0 peer '$mr_pub' preshared-key /tmp/lab-wg.psk"
check "router runtime reports configured PSK" mr "wg show wg0 preshared-keys | grep -F '$peer_pub' | grep -vq '(none)'"
check "fresh handshake succeeds with PSK" retry 90 mr "now=\$(date +%s); hs=\$(wg show wg0 latest-handshakes | awk '\$1==\"$peer_pub\"{print \$2}'); test \$((now-hs)) -lt 60"

phase "4.5-cleanup"
cfg="$(api GET /api/v1/config)"
clean="$(echo "$cfg" | python3 -c "import json,sys; c=json.load(sys.stdin); p=next(x for x in c['wireguard']['peers'] if x.get('public_key')=='$peer_pub'); p['preshared_key']=''; print(json.dumps(c))")"
require "preshared key removed from router" api PUT /api/v1/config "$clean"
require "PSK removal confirmed" confirm_pending
require "preshared key removed from remote peer" sim "wg set wg0 peer '$mr_pub' preshared-key /dev/null; rm -f /tmp/lab-wg.psk"
trap - EXIT HUP INT TERM
check "existing peer handshake recovers without PSK" retry 90 mr "now=\$(date +%s); hs=\$(wg show wg0 latest-handshakes | awk '\$1==\"$peer_pub\"{print \$2}'); test \$((now-hs)) -lt 60"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
