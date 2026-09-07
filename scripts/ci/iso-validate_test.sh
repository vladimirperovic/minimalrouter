#!/bin/sh
# Test orchestration fail-closed behavior with command mocks, never QEMU.
set -eu
REPO=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
WORK=$(mktemp -d "${TMPDIR:-/tmp}/minimalrouter-iso-gate-test.XXXXXX")
trap 'rm -rf "$WORK"' EXIT HUP INT TERM
mkdir "$WORK/bin"
export PATH="$WORK/bin:$PATH"
cat > "$WORK/bin/xorriso" <<'MOCK'
#!/bin/sh
while [ "$#" -gt 0 ]; do
 case "$1" in
 -extract) printf 'DEFAULT minimalrouter\n' > "$3"; exit ;;
 -outdev) printf 'synthetic serial ISO\n' > "$2"; exit ;;
 esac
 shift
done
MOCK
cat > "$WORK/bin/truncate" <<'MOCK'
#!/bin/sh
printf 'synthetic test disk\n' > "$3"
MOCK
cat > "$WORK/bin/timeout" <<'MOCK'
#!/bin/sh
shift
exec "$@"
MOCK
cat > "$WORK/bin/expect" <<'MOCK'
#!/bin/sh
case "$1" in
 *iso-full-install.exp) stage=install; logfile=$4; markers='FULL_ISO_INSTALL_OK INSTALLED_SSH_OK' ;;
 *iso-installed-reboot.exp) stage=reboot; logfile=$3; markers='INSTALLED_COLD_BOOT_OK ROUTERD_CRASH_RECOVERY_OK INSTALLED_WARM_REBOOT_OK INSTALLED_REBOOT_MATRIX_OK' ;;
 *iso-installer-safety.exp) stage=$2; logfile=$5
  case "$2" in existing) markers=EXISTING_INSTALL_GUARD_OK ;; small) markers=UNDERSIZED_DISK_GUARD_OK ;; *) exit 99 ;; esac ;;
 *) exit 99 ;;
esac
[ "${FAIL_STAGE:-}" != "$stage" ] || exit 42
printf '%s\n' "$markers" > "$logfile"
MOCK
# macOS developer hosts may have shasum but not sha256sum.
if ! command -v sha256sum >/dev/null 2>&1; then
cat > "$WORK/bin/sha256sum" <<'MOCK'
#!/bin/sh
exec shasum -a 256 "$@"
MOCK
fi
chmod 0755 "$WORK/bin/"*
printf 'synthetic production ISO\n' > "$WORK/production.iso"
sh "$REPO/scripts/ci/iso-validate.sh" "$WORK/production.iso" "$WORK/pass" > "$WORK/pass.log" 2>&1
[ "$(cat "$WORK/pass/result.txt")" = COMPLETE_ISO_GATE_OK ]
for stage in install reboot existing small; do
 if FAIL_STAGE="$stage" sh "$REPO/scripts/ci/iso-validate.sh" "$WORK/production.iso" "$WORK/$stage" > "$WORK/$stage.log" 2>&1; then
  echo "FAIL failed $stage gate was accepted"; exit 1
 fi
 [ ! -e "$WORK/$stage/result.txt" ]
done
[ "$(cat "$WORK/production.iso")" = 'synthetic production ISO' ]
echo 'PASS mocked ISO install/reboot/existing/small failures block completion; production ISO stays unchanged'
