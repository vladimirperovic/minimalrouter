#!/bin/sh
# 82 — A new local DNS record propagates to dnsmasq and is removed cleanly.
. "$(dirname "$0")/../lib.sh"
begin "82-dns-record-update"
phase "3-fault"
require "fault: none (dns record)" ispfault status
phase "4.5-operator"
api_login
record_name="lab-update.home.arpa"
cleanup_dns_record() {
  current="$(api GET /api/v1/config 2>/dev/null || true)"
  [ -n "$current" ] || return 0
  clean_cfg="$(echo "$current" | python3 -c 'import json,sys; c=json.load(sys.stdin); c.setdefault("dns",{})["records"]=[r for r in (c.get("dns",{}).get("records") or []) if r.get("name")!="lab-update.home.arpa"]; print(json.dumps(c))')"
  api PUT /api/v1/config "$clean_cfg" >/dev/null 2>&1 && confirm_pending >/dev/null 2>&1 || true
}
trap cleanup_dns_record EXIT HUP INT TERM
cfg="$(api GET /api/v1/config)"
new="$(echo "$cfg" | python3 -c '
import json,sys
c=json.load(sys.stdin)
recs=[r for r in (c.get("dns",{}).get("records") or []) if r.get("name")!="lab-update.home.arpa"]
recs.append({"name":"lab-update.home.arpa","ip":"192.168.1.50"})
c.setdefault("dns",{})["records"]=recs
print(json.dumps(c))')"
require "temporary DNS record saved" api PUT /api/v1/config "$new"
require "temporary DNS record confirmed" confirm_pending
require "record rendered into dnsmasq" retry 30 mr "grep -q 'host-record=lab-update.home.arpa,192.168.1.50' /etc/dnsmasq.d/minimalrouter.conf"
check "LAN resolver returns exact record" lan "host $record_name 192.168.1.1 2>/dev/null | grep -q '192.168.1.50'"

phase "4.5-cleanup"
cfg="$(api GET /api/v1/config)"
clean="$(echo "$cfg" | python3 -c '
import json,sys
c=json.load(sys.stdin)
c.setdefault("dns",{})["records"]=[r for r in (c.get("dns",{}).get("records") or []) if r.get("name")!="lab-update.home.arpa"]
print(json.dumps(c))')"
require "temporary DNS record removed" api PUT /api/v1/config "$clean"
require "DNS record removal confirmed" confirm_pending
trap - EXIT HUP INT TERM
check "removed record no longer resolves" check_not lan "host $record_name 192.168.1.1 >/dev/null 2>&1"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
