#!/bin/sh
# 147 — Outbound WireGuard cannot capture the default route.
. "$(dirname "$0")/../lib.sh"
begin "147-wireguard-client-full-tunnel"
phase "3-fault"
require "fault: none (WireGuard full tunnel)" ispfault status
phase "4.5-operator"
api_login
cfg="$(api GET /api/v1/config)"
wg_private="$(mr "wg genkey")"
wg_public="$(mr "printf '%s' '$wg_private' | wg pubkey")"
require "synthetic WireGuard private key generated" test -n "$wg_private"
require "synthetic WireGuard public key generated" test -n "$wg_public"
bad="$(printf '%s' "$cfg" | WG_PRIVATE="$wg_private" WG_PUBLIC="$wg_public" python3 -c '
import json,sys
import os
c=json.load(sys.stdin)
c["wg_client"]={"enabled":True,"interface":"wg1","private_key":os.environ["WG_PRIVATE"],"address":"10.9.0.2/32","public_key":os.environ["WG_PUBLIC"],"preshared_key":"","endpoint":"203.0.113.5:51820","allowed_ips":["0.0.0.0/0"],"persistent_keepalive":25}
print(json.dumps(c))')"
require "outbound full-tunnel route rejected" save_expects_error "$bad"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
