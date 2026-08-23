#!/bin/sh
# Apply a saved configuration to a running Minimal Router appliance.
#
# The appliance ships nothing pre-configured: a fresh install has PPPoE,
# WireGuard, DDNS, the proxy, QoS and Wi-Fi all switched off. This script is how
# an operator puts their own settings back on such an install without retyping
# them page by page, which matters after a reinstall or a lab rebuild.
#
# The settings file belongs to the operator, not to this repository. It holds
# PPPoE credentials and WireGuard private keys, so keep it in a private
# location. See docs/SEEDING.md.
#
# Usage:
#   MINIMALROUTER_PASSWORD=... scripts/apply-config.sh \
#       --host https://192.168.1.1:8443 \
#       --config ~/minimalrouterhome/my-router.json
#
# Options:
#   --host      Dashboard base URL. Default https://192.168.1.1:8443
#   --config    JSON file with the sections to apply. Required.
#   --insecure  Accept the appliance's self-signed certificate.
#   --dry-run   Print the merged configuration and exit without writing.

set -eu

HOST="https://192.168.1.1:8443"
CONFIG=""
CURL_TLS=""
DRY_RUN=0

while [ $# -gt 0 ]; do
	case "$1" in
	--host) HOST="$2"; shift 2 ;;
	--config) CONFIG="$2"; shift 2 ;;
	--insecure) CURL_TLS="--insecure"; shift ;;
	--dry-run) DRY_RUN=1; shift ;;
	-h | --help) sed -n '2,25p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
	*) echo "Unknown option: $1" >&2; exit 2 ;;
	esac
done

if [ -z "$CONFIG" ]; then
	echo "A --config file is required. See docs/SEEDING.md." >&2
	exit 2
fi
if [ ! -r "$CONFIG" ]; then
	echo "Cannot read settings file: $CONFIG" >&2
	exit 2
fi
for tool in curl jq; do
	command -v "$tool" >/dev/null 2>&1 || {
		echo "$tool is required and was not found in PATH." >&2
		exit 2
	}
done
if ! jq empty "$CONFIG" >/dev/null 2>&1; then
	echo "Settings file is not valid JSON: $CONFIG" >&2
	exit 2
fi

if [ -z "${MINIMALROUTER_PASSWORD:-}" ]; then
	printf 'Dashboard password: ' >&2
	stty -echo 2>/dev/null || true
	read -r MINIMALROUTER_PASSWORD
	stty echo 2>/dev/null || true
	printf '\n' >&2
fi

JAR="$(mktemp)"
BODY="$(mktemp)"
MERGED="$(mktemp)"
cleanup() { rm -f "$JAR" "$BODY" "$MERGED"; }
trap cleanup EXIT INT TERM

api() {
	# api METHOD PATH [json-file]
	method="$1"
	path="$2"
	payload="${3:-}"
	set -- $CURL_TLS -sS -o "$BODY" -w '%{http_code}' \
		-X "$method" \
		-b "$JAR" -c "$JAR" \
		-H "Content-Type: application/json"
	[ -n "${CSRF:-}" ] && set -- "$@" -H "X-CSRF-Token: $CSRF"
	[ -n "$payload" ] && set -- "$@" --data-binary "@$payload"
	curl "$@" "$HOST$path"
}

fail() {
	echo "$1" >&2
	[ -s "$BODY" ] && sed 's/^/  /' "$BODY" >&2
	exit 1
}

# --- 1. sign in -------------------------------------------------------------
LOGIN="$(mktemp)"
jq -n --arg p "$MINIMALROUTER_PASSWORD" \
	--arg t "${MINIMALROUTER_TOTP:-}" \
	'{password: $p} + (if $t == "" then {} else {totp_code: $t} end)' >"$LOGIN"
CODE="$(api POST /api/v1/auth/login "$LOGIN")"
rm -f "$LOGIN"
[ "$CODE" = "200" ] || fail "Login failed (HTTP $CODE)."
CSRF="$(jq -r '.csrf_token // empty' "$BODY")"
[ -n "$CSRF" ] || fail "Login succeeded but returned no CSRF token."

# --- 2. merge the operator's sections over the live configuration ----------
CODE="$(api GET /api/v1/config)"
[ "$CODE" = "200" ] || fail "Could not read the current configuration (HTTP $CODE)."

# `*` merges recursively, so the file only has to carry the sections it owns.
# Redacted secret placeholders in the live copy are preserved by the server
# unless the file supplies a real value.
jq -s '.[0] * .[1]' "$BODY" "$CONFIG" >"$MERGED" ||
	fail "Could not merge the settings file into the live configuration."

if [ "$DRY_RUN" -eq 1 ]; then
	cat "$MERGED"
	echo "Dry run: nothing was written." >&2
	exit 0
fi

# --- 3. apply ---------------------------------------------------------------
CODE="$(api PUT /api/v1/config "$MERGED")"
[ "$CODE" = "200" ] || fail "Apply rejected (HTTP $CODE)."

STATE="$(jq -r '.state // empty' "$BODY")"
TXID="$(jq -r '.id // empty' "$BODY")"

# A change that touches WAN or LAN is applied provisionally and rolls back
# unless access is confirmed. The script has just proved it can still reach the
# appliance, so confirming here is exactly what the rollback is asking about.
if [ "$STATE" = "AwaitingConfirmation" ] && [ -n "$TXID" ]; then
	echo "Connectivity-critical change is provisional; confirming access..." >&2
	CODE="$(api POST "/api/v1/transactions/$TXID/confirm")"
	[ "$CODE" = "200" ] || fail "Could not confirm the change (HTTP $CODE). It will roll back."
fi

REVISION="$(jq -r '.revision // "unknown"' "$BODY" 2>/dev/null || echo unknown)"
echo "Configuration applied. Revision: $REVISION" >&2
