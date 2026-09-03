#!/bin/sh
# 88 — A real pfSense XML payload is parsed as a preview without mutating the
# canonical Minimal Router configuration.
. "$(dirname "$0")/../lib.sh"
begin "88-pfsense-import-preview"
phase "3-fault"
require "fault: none (pfsense import)" ispfault status
phase "4.5-operator"
api_login
before_revision="$(api GET /api/v1/config | python3 -c 'import json,sys; print(json.load(sys.stdin)["revision"])')"
xml='<?xml version="1.0"?><pfsense><system><hostname>import-preview</hostname><domain>home.arpa</domain></system><interfaces><wan><if>em0</if><ipaddr>pppoe</ipaddr></wan><lan><if>em1</if><ipaddr>192.168.1.1</ipaddr><subnet>24</subnet></lan></interfaces><dhcpd><lan><enable/><range><from>192.168.1.100</from><to>192.168.1.200</to></range></lan></dhcpd></pfsense>'
resp="$(api_xml POST "/api/v1/import/pfsense/preview?wan=$MR_WAN_IF&lan=$MR_LAN_IF" "$xml")"
import_id="$(echo "$resp" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("import_id",""))' 2>/dev/null)"
hostname="$(echo "$resp" | python3 -c 'import json,sys; print(json.load(sys.stdin)["report"]["config"]["system"]["hostname"])' 2>/dev/null)"
wan_iface="$(echo "$resp" | python3 -c 'import json,sys; print(json.load(sys.stdin)["report"]["config"]["wan"]["interface"])' 2>/dev/null)"
check "preview returns expiring import identifier" test -n "$import_id"
check "preview parses hostname" test "$hostname" = "import-preview"
check "preview applies explicit Linux interface mapping" test "$wan_iface" = "$MR_WAN_IF"
after_revision="$(api GET /api/v1/config | python3 -c 'import json,sys; print(json.load(sys.stdin)["revision"])')"
check "preview does not mutate canonical config" test "$after_revision" = "$before_revision"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
