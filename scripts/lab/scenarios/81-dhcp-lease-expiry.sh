#!/bin/sh
# 81 — A one-minute DHCP lease expires and the client receives a later expiry
# when it renews. The original lease duration is restored on every exit path.
. "$(dirname "$0")/../lib.sh"
begin "81-dhcp-lease-expiry"
phase "3-fault"
require "fault: none (lease expiry)" ispfault status
phase "4.5-operator"
api_login
original="$(api GET /api/v1/config)"
original_lease="$(echo "$original" | python3 -c 'import json,sys; print(json.load(sys.stdin)["dhcp"]["lease_time"])')"
restore_lease() {
  current="$(api GET /api/v1/config 2>/dev/null || true)"
  [ -n "$current" ] || return 0
  restored="$(echo "$current" | python3 -c "import json,sys; c=json.load(sys.stdin); c['dhcp']['lease_time']='$original_lease'; print(json.dumps(c))")"
  api PUT /api/v1/config "$restored" >/dev/null 2>&1 && confirm_pending >/dev/null 2>&1 || true
}
trap restore_lease EXIT HUP INT TERM

short="$(echo "$original" | python3 -c 'import json,sys; c=json.load(sys.stdin); c["dhcp"]["lease_time"]="1m"; print(json.dumps(c))')"
require "one-minute lease saved" api PUT /api/v1/config "$short"
require "one-minute lease confirmed" confirm_pending
require "LAN client renewed its lease" lan_dhcp_renew
mac="$(lan "cat /sys/class/net/$LAN_CLIENT_IF/address" | tr -d '\r\n')"
first_expiry="$(mr "awk -v m='$mac' 'tolower(\$2)==tolower(m){print \$1; exit}' /var/lib/minimalrouter-dhcp/dnsmasq.leases" | tr -d '\r\n')"
require "renewal written with future expiry" sh -c "case '$first_expiry' in (*[!0-9]*|'') exit 1;; esac; [ '$first_expiry' -gt '$(date +%s)' ]"

# Cross the one-minute boundary using short polling intervals so the runner
# remains responsive, then force a new discover/renew cycle.
elapsed=0
while [ "$elapsed" -lt 70 ]; do sleep 5; elapsed=$((elapsed+5)); done
require "client renews after expiry boundary" lan_dhcp_renew
second_expiry="$(mr "awk -v m='$mac' 'tolower(\$2)==tolower(m){print \$1; exit}' /var/lib/minimalrouter-dhcp/dnsmasq.leases" | tr -d '\r\n')"
check "renewal advanced lease expiry" sh -c "case '$second_expiry' in (*[!0-9]*|'') exit 1;; esac; [ '$second_expiry' -gt '$first_expiry' ]"

phase "4.5-cleanup"
restore_lease
trap - EXIT HUP INT TERM
restored_lease="$(api GET /api/v1/config | python3 -c 'import json,sys; print(json.load(sys.stdin)["dhcp"]["lease_time"])')"
check "original lease duration restored" test "$restored_lease" = "$original_lease"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
