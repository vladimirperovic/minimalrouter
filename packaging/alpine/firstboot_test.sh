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
grep -q '^supervisor=supervise-daemon$' "$SOURCE/firstboot.sh"
grep -q '^command_args="-D -o PidFile=none"$' "$SOURCE/firstboot.sh"
grep -q "cat > /etc/conf.d/sshd <<'SSHD_SERVICE'" "$SOURCE/firstboot.sh"
echo 'PASS first boot supervises sshd so a crash is not permanent'
# Root password login is reachable from the LAN: bound guessing and the
# unauthenticated connections of each source, with the limits just before
# Subsystem. No global MaxStartups: a low one would let a single host fill the
# unauthenticated table and lock the administrator out.
sed -n "/^    cat > \/etc\/ssh\/sshd_config <<'SSHD'\$/,/^SSHD\$/p" "$SOURCE/firstboot.sh" > "$WORK/sshd_config.source"
grep -qx 'Subsystem sftp internal-sftp' "$WORK/sshd_config.source"
[ "$(tail -n 5 "$WORK/sshd_config.source" | head -n 4 | tr '\n' ' ')" = 'MaxAuthTries 4 LoginGraceTime 30 PerSourceMaxStartups 4 Subsystem sftp internal-sftp ' ]
if grep -Eq '^MaxStartups' "$WORK/sshd_config.source"; then echo 'FAIL global MaxStartups must stay at the OpenSSH default'; exit 1; fi
echo 'PASS sshd limits authentication attempts, login time and unauthenticated connections per source'
# The recovery password is read by firstboot itself: the prompts must keep
# matching what scripts/ci/iso-full-install.exp and scripts/gh-lab/install-router.exp expect.
awk '/^(read_hidden|set_root_password)\(\)/ {copy=1} copy {print} copy && /^}/ {copy=0}' "$SOURCE/firstboot.sh" > "$WORK/password.sh"
grep -m1 '^MIN_ROOT_PASSWORD_LENGTH=12$' "$SOURCE/firstboot.sh" > "$WORK/password-min.sh"
grep -q 'read_hidden' "$WORK/password.sh" && grep -q 'set_root_password' "$WORK/password.sh"
grep -q "^    set_root_password\$" "$SOURCE/firstboot.sh"
if grep -Eq '^[[:space:]]*(while ! )?passwd( |$)' "$SOURCE/firstboot.sh"; then echo 'FAIL firstboot still calls passwd'; exit 1; fi
ROOT=$(CDPATH= cd -- "$SOURCE/../.." && pwd)
grep -Fq '{New.*password:}' "$ROOT/scripts/ci/iso-full-install.exp"
grep -Fq '{Retype.*password:}' "$ROOT/scripts/ci/iso-full-install.exp"
grep -Fq '{New.*password:}' "$ROOT/scripts/gh-lab/install-router.exp"
grep -Fq '{Retype.*password:}' "$ROOT/scripts/gh-lab/install-router.exp"
export PATH="$WORK/bin:$PATH"
cat > "$WORK/bin/stty" <<'MOCK'
#!/bin/sh
printf '%s\n' "$*" >> "$WORK/stty.log"
case "$1" in
    -g) [ "${STTY_GET_RC:-0}" = 0 ] || exit 1; printf 'SAVED-TERMINAL-STATE\n' ;;
    -echo) exit "${STTY_ECHO_RC:-0}" ;;
esac
exit 0
MOCK
cat > "$WORK/bin/chpasswd" <<'MOCK'
#!/bin/sh
printf '%s\n' "$*" >> "$WORK/chpasswd.args"
cat >> "$WORK/chpasswd.stdin"
exit "${CHPASSWD_RC:-0}"
MOCK
chmod 0755 "$WORK/bin/stty" "$WORK/bin/chpasswd"
cat > "$WORK/password-driver" <<'DRIVER'
set -eu
fail() { printf 'FAIL-CALLED: %s\n' "$*"; exit 97; }
. "$WORK/password-min.sh"
. "$WORK/password.sh"
set_root_password
printf 'PASSWORD-SET\n'
DRIVER
run_password() {
    rm -f "$WORK/stty.log" "$WORK/chpasswd.args" "$WORK/chpasswd.stdin"
    : > "$WORK/stty.log"; : > "$WORK/chpasswd.args"; : > "$WORK/chpasswd.stdin"
    sh "$WORK/password-driver" > "$WORK/password.out" 2> "$WORK/password.err" || return 1
}
# A short password is refused, then a valid one is accepted.
printf '%s\n' short short elevenchars elevenchars 'correct horse 12' 'correct horse 12' | run_password
grep -q 'PASSWORD-SET' "$WORK/password.out"
[ "$(grep -c 'at least 12 characters long' "$WORK/password.out")" = 2 ]
[ "$(grep -c '' "$WORK/chpasswd.stdin")" = 1 ]
[ "$(cat "$WORK/chpasswd.stdin")" = 'root:correct horse 12' ]
[ "$(cat "$WORK/chpasswd.args")" = '-c sha512' ]
echo 'PASS firstboot refuses a recovery password shorter than 12 characters'
# The prompts are exactly what the Expect harnesses wait for, and echo is
# switched off before each prompt and restored after each read.
grep -Eq 'New.*password:' "$WORK/password.err"
grep -Eq 'Retype.*password:' "$WORK/password.err"
grep -Fq 'New password: ' "$WORK/password.err"
grep -Fq 'Retype password: ' "$WORK/password.err"
[ "$(grep -c '^-g$' "$WORK/stty.log")" = 6 ]
[ "$(grep -c '^-echo$' "$WORK/stty.log")" = 6 ]
# The saved state is restored after every read (once explicitly, once by the EXIT trap).
[ "$(grep -c '^SAVED-TERMINAL-STATE$' "$WORK/stty.log")" -ge 6 ]
[ "$(tail -n 1 "$WORK/stty.log")" = SAVED-TERMINAL-STATE ]
if grep -q 'correct horse' "$WORK/stty.log" "$WORK/chpasswd.args" "$WORK/password.err" "$WORK/password.out"; then echo 'FAIL password leaked outside chpasswd stdin'; exit 1; fi
echo 'PASS recovery password prompts match the automation and echo stays off while typing'
# A mismatch is refused and nothing is set from it.
printf '%s\n' firstpassword-1 secondpassword2 'valid password 1' 'valid password 1' | run_password
grep -q 'did not match' "$WORK/password.out"
[ "$(cat "$WORK/chpasswd.stdin")" = 'root:valid password 1' ]
echo 'PASS firstboot refuses mismatching recovery passwords'
# chpasswd is the only way the hash is set, and it takes the password on stdin.
printf '%s\n' 'back\slash:colon 12' 'back\slash:colon 12' | run_password
[ "$(cat "$WORK/chpasswd.stdin")" = 'root:back\slash:colon 12' ]
if grep -Eq '(^|[^h])passwd( |$)|shadow' "$WORK/password.sh"; then echo 'FAIL password function must only use chpasswd'; exit 1; fi
echo 'PASS recovery password is hashed by chpasswd -c sha512 from stdin, preserving backslashes and colons'
# Without a usable stty the password is never read with echo on, and nothing loops.
if printf '%s\n' 'valid password 1' 'valid password 1' | STTY_GET_RC=1 run_password; then echo 'FAIL missing stty accepted'; exit 1; fi
grep -q 'FAIL-CALLED: Cannot read a password without echo' "$WORK/password.out"
[ ! -s "$WORK/chpasswd.stdin" ]
if printf '%s\n' 'valid password 1' 'valid password 1' | STTY_ECHO_RC=1 run_password; then echo 'FAIL stty that cannot disable echo accepted'; exit 1; fi
grep -q 'FAIL-CALLED: Cannot read a password without echo' "$WORK/password.out"
[ ! -s "$WORK/chpasswd.stdin" ]
[ "$(tail -n 1 "$WORK/stty.log")" = SAVED-TERMINAL-STATE ]
if printf '' | run_password; then echo 'FAIL end of input looped or succeeded'; exit 1; fi
grep -q 'FAIL-CALLED: Password entry ended' "$WORK/password.out"
if printf '%s\n' 'valid password 1' 'valid password 1' | CHPASSWD_RC=1 run_password; then echo 'FAIL chpasswd failure accepted'; exit 1; fi
grep -q 'FAIL-CALLED: Could not set the recovery password' "$WORK/password.out"
echo 'PASS firstboot fails closed when echo cannot be disabled, input ends or chpasswd fails'
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
