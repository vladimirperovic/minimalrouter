#!/bin/sh
# Dependency/preflight tests only: every writable path and command is isolated.
set -eu
SOURCE=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
WORK=$(mktemp -d "${TMPDIR:-/tmp}/minimalrouter-installer-test.XXXXXX")
trap 'rm -rf "$WORK"' EXIT HUP INT TERM
export WORK
mkdir -p "$WORK/mock-bin" "$WORK/bin" "$WORK/etc/apk"
echo 3.24.3 > "$WORK/etc/alpine-release"
: > "$WORK/etc/apk/repositories"
export PATH="$WORK/mock-bin:$PATH"
cat > "$WORK/mock-bin/id" <<'MOCK'
#!/bin/sh
printf '0\n'
MOCK
cat > "$WORK/mock-bin/uname" <<'MOCK'
#!/bin/sh
printf 'x86_64\n'
MOCK
cat > "$WORK/mock-bin/apk" <<'MOCK'
#!/bin/sh
printf '%s\n' "$*" >> "$WORK/apk.log"
case "$1" in
  update|add) echo "MOCK apk $*" ;;
  info) [ "$3" != "${MISSING_PACKAGE:-}" ] ;;
  *) exit 1 ;;
esac
MOCK
cat > "$WORK/bin/router-update-amd64" <<'MOCK'
#!/bin/sh
printf '%s\n' "$1" >> "$WORK/updater.log"
case "$1" in
  install-preflight) exit "${PREFLIGHT_RC:-0}" ;;
  install-begin) : > "$WORK/intent" ;;
  *) exit 99 ;;
esac
MOCK
cat > "$WORK/mock-bin/dnsmasq" <<'MOCK'
#!/bin/sh
printf 'Compile time options: IPv6 %s DHCP DNSSEC\n' "${DNSMASQ_CAPABILITY:-nftset}"
MOCK
chmod 0755 "$WORK/mock-bin/"* "$WORK/bin/"*
# Stop before kernel/runtime mutation. Only dependency discovery is exercised.
sed '/# Fail before replacing appliance runtime files/,$d' "$SOURCE/install-dist.sh" |
    sed '/# Install the admission\/recovery fence durably/,/^# The all-in-one ISO/{ /^# The all-in-one ISO/!d; }' |
    sed "s|/etc|$WORK/etc|g; s/\[ -f \"\$required\" \]/true/g" > "$WORK/install-dist.sh"
output=$(sh "$WORK/install-dist.sh" 2>&1)
printf '%s\n' "$output" | grep -q 'MOCK apk update'
printf '%s\n' "$output" | grep -q 'MOCK apk add'
grep -qx 'https://dl-cdn.alpinelinux.org/alpine/v3.24/main' "$WORK/etc/apk/repositories"
grep -qx 'https://dl-cdn.alpinelinux.org/alpine/v3.24/community' "$WORK/etc/apk/repositories"
echo 'PASS normal dependency installation from the running release'

# An existing appliance keeps its own release: never point 3.22 at 3.24.
echo 3.22.5 > "$WORK/etc/alpine-release"
printf '%s\n' https://dl-cdn.alpinelinux.org/alpine/v3.22/main https://dl-cdn.alpinelinux.org/alpine/v3.22/community > "$WORK/etc/apk/repositories"
sh "$WORK/install-dist.sh" > /dev/null 2>&1
! grep -q 'v3.24' "$WORK/etc/apk/repositories"
echo 'PASS an older supported release keeps its repositories'

rm -f "$WORK/intent" "$WORK/apk.log"
echo 3.20.7 > "$WORK/etc/alpine-release"
if sh "$WORK/install-dist.sh" > "$WORK/output" 2>&1; then exit 1; fi
grep -q "unsupported Alpine release '3.20'" "$WORK/output"
[ ! -e "$WORK/intent" ] && [ ! -e "$WORK/apk.log" ]
echo 3.24.3 > "$WORK/etc/alpine-release"
echo 'PASS unsupported release refused before install intent and package mutation'
: > "$WORK/apk.log"
output=$(sh "$WORK/install-dist.sh" --offline 2>&1)
printf '%s\n' "$output" | grep -q 'All required dependencies already installed'
! grep -Eq '^(update|add)' "$WORK/apk.log"
echo 'PASS offline checks without package mutation'
if MISSING_PACKAGE=nftables sh "$WORK/install-dist.sh" --offline > "$WORK/output" 2>&1; then exit 1; fi
grep -q 'required packages are missing' "$WORK/output"
echo 'PASS missing dependency refusal'
if sh "$WORK/install-dist.sh" --unknown > "$WORK/output" 2>&1; then exit 1; fi
grep -q Usage: "$WORK/output"
echo 'PASS unknown argument refusal'
rm -f "$WORK/intent" "$WORK/apk.log" "$WORK/updater.log"
if PREFLIGHT_RC=42 sh "$WORK/install-dist.sh" > "$WORK/output" 2>&1; then exit 1; fi
[ ! -e "$WORK/intent" ] && [ ! -e "$WORK/apk.log" ]
[ "$(cat "$WORK/updater.log")" = install-preflight ]
echo 'PASS rejected trust preflight precedes install intent and package mutation'

rm -f "$WORK/intent" "$WORK/updater.log"
if DNSMASQ_CAPABILITY=no-nftset sh "$WORK/install-dist.sh" --offline > "$WORK/output" 2>&1; then exit 1; fi
grep -q 'lacks required NFTSET' "$WORK/output"
[ ! -e "$WORK/intent" ]
[ "$(cat "$WORK/updater.log")" = install-preflight ]
echo 'PASS offline no-nftset refusal before installation mutation'

# Execute only the two real binary-install commands in a temporary tree.
# Assert production ownership explicitly, then omit chown for this non-root host
# harness; all paths and permissions still use the actual installer commands.
mkdir -p "$WORK/bootstrap/bin"
printf '#!/bin/sh\nexit 0\n' > "$WORK/bin/router-recovery-amd64"
for command in router-recovery router-update; do
    line=$(sed -n "/^install .*\"bin\/$command-\${BIN_ARCH}\"/p" "$SOURCE/install-dist.sh")
    [ -n "$line" ]
    printf '%s\n' "$line" | grep -q -- '-o root -g root'
    printf '%s\n' "$line" |
        sed "s| -o root -g root||; s|/usr/libexec/minimalrouter/bootstrap|$WORK/bootstrap|g" > "$WORK/install-one.sh"
    (cd "$WORK"; BIN_ARCH=amd64; export BIN_ARCH; sh ./install-one.sh)
done
[ "$(ls -l "$WORK/bootstrap/bin/router-recovery-amd64" | cut -c1-10)" = '-rwxr-xr-x' ]
[ "$(ls -l "$WORK/bootstrap/bin/router-update-amd64" | cut -c1-10)" = '-rwxr-x---' ]
echo 'PASS recovery worker executable 0755; updater private 0750; explicit root ownership'

# The module list is installed to /etc/modules-load.d, which only OpenRC's
# modules service reads; without it a Golden image never loads ppp_generic.
grep -q '^rc-update add modules boot$' "$SOURCE/install-dist.sh"
for module in ppp_generic pppox pppoe; do
    grep -qx "$module" "$SOURCE/minimalrouter.modules"
done
echo 'PASS kernel module list is loaded at boot through the modules service'

# Appliances provisioned before SSH was limited to root get AllowUsers root from
# the full installer. Run only the real function against a temporary sshd_config;
# the mock sshd prints the effective keywords (-T) and validates candidates (-t).
mkdir -p "$WORK/etc/ssh"
cat > "$WORK/mock-bin/sshd" <<'MOCK'
#!/bin/sh
case "$1" in
  -T) [ -z "${SSHD_REJECT_CURRENT:-}" ] || exit 1
      awk '!/^[[:space:]]*(#|$)/ { $1 = tolower($1); print }' "$3" ;;
  -t) [ -z "${SSHD_REJECT_CANDIDATE:-}" ] || exit 1
      grep -qx 'AllowUsers root' "$3" ;;
  *) exit 2 ;;
esac
MOCK
chmod 0755 "$WORK/mock-bin/sshd"
awk '/^restrict_appliance_ssh_to_root\(\)/ {copy=1} copy {print} copy && /^}/ {exit}' "$SOURCE/install-dist.sh" |
    sed "s|/etc/ssh/|$WORK/etc/ssh/|g; s|/usr/sbin/sshd|$WORK/mock-bin/sshd|g" > "$WORK/restrict-ssh.sh"
printf '\nrestrict_appliance_ssh_to_root\n' >> "$WORK/restrict-ssh.sh"
config="$WORK/etc/ssh/sshd_config"
unchanged() {
    cmp -s "$config" "$WORK/original" && [ ! -e "$config.minimalrouter-new" ]
}
cat > "$config" <<'CONFIG'
Port 22
PermitRootLogin yes
PasswordAuthentication yes
Match Address 192.0.2.0/24
    PasswordAuthentication no
CONFIG
chmod 0600 "$config"
cp -p "$config" "$WORK/original"
mode="$(ls -l "$config" | cut -c1-10)"
sh "$WORK/restrict-ssh.sh" > "$WORK/output" 2>&1
grep -q 'limited to root' "$WORK/output"
# Before every original line, so no Match block can scope the directive.
[ "$(grep -v '^#' "$config" | head -n 1)" = 'AllowUsers root' ]
tail -n +3 "$config" | cmp -s - "$WORK/original"
[ "$(ls -l "$config" | cut -c1-10)" = "$mode" ]
[ ! -e "$config.minimalrouter-new" ]
cp -p "$config" "$WORK/original"
sh "$WORK/restrict-ssh.sh" > "$WORK/output" 2>&1
unchanged
grep -q 'already set' "$WORK/output"
echo 'PASS provisioned appliance SSH is limited to root once, ahead of every Match block'

printf 'AllowGroups wheel\nPort 22\n' > "$config"
cp -p "$config" "$WORK/original"
sh "$WORK/restrict-ssh.sh" > /dev/null 2>&1
unchanged
printf 'Port 22\n' > "$config"
cp -p "$config" "$WORK/original"
SSHD_REJECT_CANDIDATE=1 sh "$WORK/restrict-ssh.sh" > "$WORK/output" 2>&1
unchanged
grep -q 'WARNING' "$WORK/output"
SSHD_REJECT_CURRENT=1 sh "$WORK/restrict-ssh.sh" > "$WORK/output" 2>&1
unchanged
grep -q 'WARNING' "$WORK/output"
echo 'PASS operator SSH policy, invalid configs and rejected candidates stay untouched'

# A generic Alpine host keeps its owner's SSH logins: only a provisioned Golden
# appliance runs the migration.
grep -A1 '^if \[ -f /etc/minimalrouter/installed \] && \[ -f /etc/minimalrouter/firstboot-complete \]; then$' \
    "$SOURCE/install-dist.sh" | tail -n 1 | grep -qx '    restrict_appliance_ssh_to_root'
[ "$(grep -c '^ *restrict_appliance_ssh_to_root$' "$SOURCE/install-dist.sh")" = 1 ]
echo 'PASS only a provisioned Golden appliance has its SSH policy changed'
