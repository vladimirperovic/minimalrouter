#!/bin/sh
# 148 — QoS accepts only CAKE or FQ-CoDel algorithms.
. "$(dirname "$0")/../lib.sh"
begin "148-qos-algorithm"
phase "3-fault"
require "fault: none (QoS algorithm)" ispfault status
phase "4.5-operator"
api_login
cfg="$(api GET /api/v1/config)"
bad="$(echo "$cfg" | python3 -c 'import json,sys; c=json.load(sys.stdin); c["qos"].update({"enabled":True,"algorithm":"fifo","download_limit_mbps":100,"upload_limit_mbps":20}); print(json.dumps(c))')"
require "unsupported QoS algorithm rejected" save_expects_error "$bad"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
