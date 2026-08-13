#!/bin/sh
# 99 — Gateway history contains typed, chronologically ordered samples.
. "$(dirname "$0")/../lib.sh"
begin "99-gateway-quality-history"
phase "3-fault"
require "fault: none (gw history)" ispfault status
phase "4.5-operator"
api_login
resp="$(api GET '/api/v1/gateway/history?window=1h')"
point_count="$(echo "$resp" | python3 -c '
import json,sys
d=json.load(sys.stdin)
assert d.get("window")=="1h"
points=d.get("points")
assert isinstance(points,list) and points
stamps=[]
for p in points:
    assert isinstance(p.get("timestamp"),str) and p.get("state") in {"healthy","degraded","offline","unknown"}
    assert isinstance(p.get("packet_loss_percent"),(int,float))
    stamps.append(p["timestamp"])
assert stamps==sorted(stamps)
print(len(points))')"
check "history returns valid ordered samples" test "$point_count" -gt 0
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
