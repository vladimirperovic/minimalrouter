#!/bin/sh
# Exercise the real dispatcher against isolated synthetic executable payloads.
set -eu
SOURCE=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
WORK=$(mktemp -d "${TMPDIR:-/tmp}/minimalrouter-dispatch-test.XXXXXX")
trap 'rm -rf "$WORK"' EXIT HUP INT TERM
export WORK
mkdir -p "$WORK/bin" "$WORK/bootstrap/bin" "$WORK/updates/slots/current/bin" "$WORK/updates/slots/current/web/dist" "$WORK/migration"
cat > "$WORK/bin/uname" <<'MOCK'
#!/bin/sh
echo x86_64
MOCK
cat > "$WORK/bin/stat" <<'MOCK'
#!/bin/sh
exec python3 -c 'import os,sys; print(format(os.stat(sys.argv[-1]).st_mode & 0o7777,"o"))' "$@"
MOCK
cat > "$WORK/updates/slots/current/bin/routerd-amd64" <<'MOCK'
#!/bin/sh
printf 'SLOT_ROUTERD %s\n' "$MINIMALROUTER_WEB_DIR"
MOCK
cat > "$WORK/updates/slots/current/bin/router-applyd-amd64" <<'MOCK'
#!/bin/sh
echo SLOT_APPLYD
MOCK
for command in routerd router-applyd router-update router-recovery; do
 printf '#!/bin/sh\necho BOOTSTRAP_%s\n' "$command" > "$WORK/bootstrap/bin/$command-amd64"
done
chmod 0755 "$WORK/bin/"* "$WORK/bootstrap/bin/"* "$WORK/updates/slots/current/bin/"*
printf 'synthetic Dashboard\n' > "$WORK/updates/slots/current/web/dist/index.html"
ln -s slots/current "$WORK/updates/current"
sed -e "s|^PATH=.*|PATH=$WORK/bin:$PATH|" \
    -e "s|/bin/uname|$WORK/bin/uname|g" \
    -e "s|/var/lib/minimalrouter-update|$WORK/updates|g" \
    -e "s|/var/lib/minimalrouter-migration|$WORK/migration|g" \
    -e "s|/usr/libexec/minimalrouter/bootstrap|$WORK/bootstrap|g" \
    "$SOURCE/slot-exec" > "$WORK/slot-exec"
sh "$WORK/slot-exec" routerd | grep -q SLOT_ROUTERD
sh "$WORK/slot-exec" router-applyd | grep -q SLOT_APPLYD
for journal in "$WORK/updates/installation.json" "$WORK/migration/pending.json"; do
 : > "$journal"
 for daemon in routerd router-applyd; do
  if sh "$WORK/slot-exec" "$daemon" > "$WORK/output" 2>&1; then echo 'FAIL maintenance journal admitted daemon'; exit 1; fi
 done
 sh "$WORK/slot-exec" router-recovery | grep -q BOOTSTRAP_router-recovery
 sh "$WORK/slot-exec" router-update | grep -q BOOTSTRAP_router-update
 rm "$journal"
done
chmod 0700 "$WORK/updates/slots/current/bin/routerd-amd64"
for daemon in routerd router-applyd; do
 if sh "$WORK/slot-exec" "$daemon" > "$WORK/output" 2>&1; then echo 'FAIL invalid pair fell back to bootstrap'; exit 1; fi
done
chmod 0755 "$WORK/updates/slots/current/bin/routerd-amd64"
rm "$WORK/updates/current"
ln -s slots/missing "$WORK/updates/current"
if sh "$WORK/slot-exec" routerd > "$WORK/output" 2>&1; then echo 'FAIL broken slot fell back'; exit 1; fi
echo 'PASS dispatcher pairs stay together; installation/migration fences block daemons and preserve recovery'
