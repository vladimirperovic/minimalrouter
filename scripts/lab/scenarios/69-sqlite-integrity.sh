#!/bin/sh
# 69 — Configuration-store persistence: the SQLite database exists, a full
# save round-trip remains readable, and canonical/helper revisions converge.
. "$(dirname "$0")/../lib.sh"
begin "69-sqlite-integrity"
phase "3-fault"
require "fault: none (sqlite)" ispfault status
phase "4.5-operator"
api_login
cfg="$(api GET /api/v1/config)"
require "SQLite configuration database exists" mr "test -s /var/lib/minimalrouter/minimalrouter.db"
require "config save round-trip" api PUT /api/v1/config "$cfg"
require "config round-trip confirmed" confirm_pending
check "config readable after save" api GET /api/v1/config
phase "4-mr-runtime-2"
check "routerd still alive" mr "rc-service routerd status | grep -q started"
check "canonical + last-good converge" check_converge
check "production untouched" check_prod_untouched "$PROD_PORTS_BEFORE"
capture_state "evidence"
finish_scenario
