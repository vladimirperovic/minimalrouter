#!/bin/sh
set -eu

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
SCRIPT="$ROOT/packaging/alpine/install-dist.sh"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

mkdir -p "$TMP/mock-bin" "$TMP/mock-etc/apk" "$TMP/dist/bin" "$TMP/dist/web/dist" "$TMP/dist/init.d" "$TMP/dist/sysctl" "$TMP/dist/modules" "$TMP/dist/logrotate"

cat > "$TMP/mock-bin/apk" <<'EOF'
#!/bin/sh
case "${1:-}" in
  update) echo "NETWORK apk update" ;;
  add) echo "NETWORK apk add" ;;
  info)
    [ "${2:-}" = "-e" ] || exit 2
    [ "${3:-}" != "missing-pkg" ]
    ;;
  *) exit 2 ;;
esac
EOF
chmod +x "$TMP/mock-bin/apk"

cat > "$TMP/mock-bin/id" <<'EOF'
#!/bin/sh
echo 0
EOF
cat > "$TMP/mock-bin/uname" <<'EOF'
#!/bin/sh
echo x86_64
EOF
chmod +x "$TMP/mock-bin/id" "$TMP/mock-bin/uname"

# Build a dependency-phase-only copy so the test does not modify the host.
sed 's|/etc|mock-etc|g' "$SCRIPT" > "$TMP/install.sh"
sed -i.bak '/SCRIPT_DIR=/c\SCRIPT_DIR="'$TMP'/dist"' "$TMP/install.sh"
sed -i.bak '/# Router authentication, TLS,/,$d' "$TMP/install.sh"

for f in \
  bin/routerd-amd64 bin/router-applyd-amd64 bin/router-recovery-amd64 bin/router-update-amd64 \
  web/dist/index.html slot-exec init.d/routerd init.d/router-applyd init.d/pppoe-wan \
  sysctl/99-minimalrouter.conf modules/minimalrouter.conf logrotate/minimalrouter
do
  mkdir -p "$TMP/dist/$(dirname "$f")"
  : > "$TMP/dist/$f"
done
: > "$TMP/mock-etc/alpine-release"
: > "$TMP/mock-etc/apk/repositories"

run() {
  (cd "$TMP" && PATH="$TMP/mock-bin:$PATH" sh "$TMP/install.sh" "$@")
}

normal="$(run 2>&1)"
echo "$normal" | grep -q 'NETWORK apk update'
echo "$normal" | grep -q 'NETWORK apk add'

offline="$(run --offline 2>&1)"
if echo "$offline" | grep -q 'NETWORK'; then
  echo "offline mode attempted network package operations" >&2
  exit 1
fi
echo "$offline" | grep -q 'All required dependencies are already installed.'

unknown_rc=0
run --unknown >/dev/null 2>&1 || unknown_rc=$?
[ "$unknown_rc" -ne 0 ]

echo "install-dist offline-mode tests passed"
