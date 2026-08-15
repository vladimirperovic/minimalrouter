#!/bin/sh
# 150 — The portable 5 GHz Wi-Fi profile accepts only channels 36/40/44/48.
. "$(dirname "$0")/../lib.sh"
begin "150-wifi-channel"
phase "3-fault"
require "fault: none (Wi-Fi channel)" ispfault status
phase "4.5-operator"
api_login
cfg="$(api GET /api/v1/config)"
bad="$(echo "$cfg" | python3 -c 'import json,sys; c=json.load(sys.stdin); c["wifi"].update({"enabled":True,"interface":"wlan9","ssid":"MinimalRouter-Lab","passphrase":"LongEnoughPass2026","band":"5ghz","channel":13}); print(json.dumps(c))')"
require "invalid 5 GHz channel rejected" save_expects_error "$bad"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
