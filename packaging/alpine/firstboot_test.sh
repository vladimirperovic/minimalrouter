#!/bin/sh
# No OpenRC/installer execution: isolate admission functions and every path.
set -eu
SOURCE=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
WORK=$(mktemp -d "${TMPDIR:-/tmp}/minimalrouter-firstboot-test.XXXXXX")
trap 'rm -rf "$WORK"' EXIT HUP INT TERM
export WORK
mkdir -p "$WORK/etc/minimalrouter" "$WORK/etc/ssh" "$WORK/bin"
# Extract only fail(), never the console redirection, resizing or main().
awk '/^fail\(\)/ {copy=1} copy {print} copy && /^}/ {exit}' "$SOURCE/firstboot.sh" > "$WORK/fail.sh"
printf '\nfail synthetic-test\n' >> "$WORK/fail.sh"
if sh "$WORK/fail.sh" </dev/null > "$WORK/fail.log" 2>&1; then
    echo 'FAIL recovery shell exit turned failure into success'; exit 1
fi
echo 'PASS clean recovery shell exit retains firstboot failure'
cat > "$WORK/bin/router-setup" <<'MOCK'
#!/bin/sh
exit "${SETUP_RC:-0}"
MOCK
cat > "$WORK/bin/sshd" <<'MOCK'
#!/bin/sh
exit "${SSHD_RC:-0}"
MOCK
cat > "$WORK/bin/stat" <<'MOCK'
#!/bin/sh
printf '%s\n' "${MARKER_OWNER_MODE:-0:600}"
MOCK
chmod 0755 "$WORK/bin/"*
export PATH="$WORK/bin:$PATH"
sed "s|/etc/|$WORK/etc/|g; s|/usr/sbin/|$WORK/bin/|g; s|/var/lib/|$WORK/var/lib/|g" "$SOURCE/firstboot-ready" > "$WORK/ready"
: > "$WORK/etc/minimalrouter/installed"
if sh "$WORK/ready"; then echo 'FAIL missing marker'; exit 1; fi
printf 'version=test\nconfigured_at=synthetic\n' > "$WORK/etc/minimalrouter/firstboot-complete"
printf 'root:synthetic-nonsecret-hash:0:0:99999:7:::\n' > "$WORK/etc/shadow"
printf 'synthetic-not-a-key\n' > "$WORK/etc/ssh/ssh_host_ed25519_key"
sh "$WORK/ready"
mkdir -p "$WORK/var/lib/minimalrouter-update"
: > "$WORK/var/lib/minimalrouter-update/installation.json"
if sh "$WORK/ready"; then echo 'FAIL pending installer admitted runtime'; exit 1; fi
rm "$WORK/var/lib/minimalrouter-update/installation.json"
mkdir -p "$WORK/var/lib/minimalrouter-migration"
: > "$WORK/var/lib/minimalrouter-migration/pending.json"
if sh "$WORK/ready"; then echo 'FAIL pending migration admitted runtime'; exit 1; fi
rm "$WORK/var/lib/minimalrouter-migration/pending.json"
if MARKER_OWNER_MODE=0:644 sh "$WORK/ready"; then echo 'FAIL permissive marker'; exit 1; fi
if SETUP_RC=1 sh "$WORK/ready"; then echo 'FAIL invalid canonical setup'; exit 1; fi
if SSHD_RC=1 sh "$WORK/ready"; then echo 'FAIL invalid SSH'; exit 1; fi
printf 'root:!:0:0:99999:7:::\n' > "$WORK/etc/shadow"
if sh "$WORK/ready"; then echo 'FAIL locked recovery account'; exit 1; fi
echo 'PASS marker requires private mode, canonical credentials, root credential and SSH readiness'
# OpenRC's start() must propagate command/admission failures. start_pre is never
# called here (it changes gettys on a real appliance).
cat > "$WORK/bin/admit" <<'MOCK'
#!/bin/sh
exit "${READY_RC:-0}"
MOCK
cat > "$WORK/bin/provision" <<'MOCK'
#!/bin/sh
printf 'called\n' >> "$WORK/provision.log"
exit "${PROVISION_RC:-0}"
MOCK
chmod 0755 "$WORK/bin/"*
sed "s|/etc/|$WORK/etc/|g; s|/usr/libexec/minimalrouter/firstboot-ready|$WORK/bin/admit|g; s|/usr/libexec/minimalrouter/firstboot|$WORK/bin/provision|g" "$SOURCE/firstboot.initd" > "$WORK/initd"
cat > "$WORK/start" <<'MOCK'
#!/bin/sh
ebegin() { :; }
eend() { :; }
. "$WORK/initd"
start
MOCK
if READY_RC=1 sh "$WORK/start"; then echo 'FAIL corrupt existing marker accepted'; exit 1; fi
[ -s "$WORK/provision.log" ] || { echo 'FAIL invalid existing marker did not enter recovery'; exit 1; }
rm "$WORK/etc/minimalrouter/firstboot-complete"
if READY_RC=1 PROVISION_RC=1 sh "$WORK/start"; then echo 'FAIL provisioning failure accepted'; exit 1; fi
if READY_RC=1 sh "$WORK/start"; then echo 'FAIL command success without ready state accepted'; exit 1; fi
sh "$WORK/start"
echo 'PASS OpenRC propagates provisioning and admission failures'
