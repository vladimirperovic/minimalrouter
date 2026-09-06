#!/bin/sh
# Dependency/preflight tests only: every writable path and command is isolated.
set -eu
SOURCE=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
WORK=$(mktemp -d "${TMPDIR:-/tmp}/minimalrouter-installer-test.XXXXXX")
trap 'rm -rf "$WORK"' EXIT HUP INT TERM
export WORK
mkdir -p "$WORK/mock-bin" "$WORK/bin" "$WORK/etc/apk"
: > "$WORK/etc/alpine-release"
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
echo 'PASS normal dependency installation'
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
