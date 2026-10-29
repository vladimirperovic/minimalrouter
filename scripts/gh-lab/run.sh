#!/bin/bash
# Runs the whole lab on one Linux host with sudo and QEMU: builds the network,
# installs the router from the production ISO, brings PPPoE and the LAN up,
# runs the test suite and collects every log. Usage: run.sh PRODUCTION_ISO
set -euo pipefail
HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
. "$HERE/lib.sh"
ISO=${1:?usage: run.sh production.iso}
[ -f "$ISO" ] || { echo "ISO not found: $ISO" >&2; exit 2; }
cd "$HERE/../.."

cleanup() {
    set +e
    if [ -f "$LAB_RUN/router.pid" ]; then
        qemu_monitor quit 2>/dev/null || kill "$(cat "$LAB_RUN/router.pid")" 2>/dev/null
    fi
    "$HERE/topology.sh" down
}
trap cleanup EXIT

sha256sum "$ISO" | tee "$LOGS/iso.sha256"

# The production ISO boots to VGA; the lab drives the serial entry of the very
# same ISO (only the default menu entry changes), like scripts/ci/iso-validate.sh.
xorriso -osirrox on -indev "$ISO" -extract /boot/syslinux/syslinux.cfg "$LAB_DIR/syslinux.cfg"
grep -qx 'DEFAULT minimalrouter' "$LAB_DIR/syslinux.cfg"
sed 's/^DEFAULT minimalrouter$/DEFAULT minimalrouter-serial/' "$LAB_DIR/syslinux.cfg" > "$LAB_DIR/syslinux-serial.cfg"
rm -f "$LAB_DIR/serial-test.iso"
xorriso -indev "$ISO" -outdev "$LAB_DIR/serial-test.iso" -boot_image any replay \
    -map "$LAB_DIR/syslinux-serial.cfg" /boot/syslinux/syslinux.cfg -commit -end

rm -f "$LAB_DIR/router.raw"
truncate -s 8G "$LAB_DIR/router.raw"
bash "$HERE/topology.sh" up

accel=tcg
# Without KVM, TCG's AVX2 emulation made OpenSSL (and with it sshd) segfault in
# CI, so emulate a CPU without AVX. With KVM the real host CPU is used, which
# exercises the AVX2 code paths on hardware.
cpu=Westmere
if [ -w /dev/kvm ]; then
    accel=kvm
    cpu=host
fi
log "starting the router VM (accel=$accel)"
rm -f "$LAB_RUN/router.sock" "$LAB_RUN/router.mon"
qemu-system-x86_64 -name lab-router -machine "pc,accel=$accel" -cpu "$cpu" -smp 2 -m "${ROUTER_RAM_MB:-1024}" \
    -boot once=d -cdrom "$LAB_DIR/serial-test.iso" -drive "file=$LAB_DIR/router.raw,format=raw,if=virtio" \
    -netdev tap,id=wan,ifname=tap-wan,script=no,downscript=no -device virtio-net-pci,netdev=wan,id=nicwan,mac=52:54:00:aa:00:01 \
    -netdev tap,id=lan,ifname=tap-lan,script=no,downscript=no -device e1000,netdev=lan,id=niclan,mac=52:54:00:aa:00:02 \
    -chardev "socket,id=ser0,path=$LAB_RUN/router.sock,server=on,wait=off,logfile=$LOGS/router-serial.log" -serial chardev:ser0 \
    -monitor "unix:$LAB_RUN/router.mon,server,nowait" \
    -display none -daemonize -pidfile "$LAB_RUN/router.pid"

log "installing the router and answering first boot (PPPoE credentials included)"
export DASH_PASS ROOT_PASS PPPOE_USER PPPOE_PASS
expect "$HERE/install-router.exp" "$LAB_RUN/router.sock" "$LOGS/install.log"

log "waiting for the router's SSH on the LAN"
wait_for 900 rsh true || { echo 'ERROR: SSH did not come up after first boot' >&2; exit 1; }
wait_for 600 rsh 'test -s /run/minimalrouter/routerd.ready' || { echo 'ERROR: routerd never became ready' >&2; rsh 'tail -n 80 /var/log/routerd.err /var/log/router-applyd.err' >&2 || true; exit 1; }

set +e
bash "$HERE/suite.sh"
suite_status=$?
contracts_status=0
if [ -z "${LAB_ONLY:-}" ] || printf '%s' '3000 API contracts' | grep -Eiq "$LAB_ONLY"; then
    log "running 3,000 deterministic API contracts against the installed router"
    python3 -I "$HERE/contracts.py" --out "$LOGS/contracts" 2>&1 | tee "$LOGS/contracts.log"
    contracts_status=${PIPESTATUS[0]}
    if [ "$contracts_status" -ne 0 ]; then
        log "API contract failure: stopping before further disruptive tests; unexecuted cases are not passes"
        exit "$contracts_status"
    fi
else
    log "API contracts not selected by LAB_ONLY; this is not a full validation run"
fi
if [ "${LAB_CHAOS:-1}" = 1 ] && router_ok=$(rsh true 2>/dev/null); then
    bash "$HERE/chaos.sh"
    chaos_status=$?
else
    log "skipping the chaos phase (router unreachable or LAB_CHAOS=0)"
    chaos_status=1
    [ "${LAB_CHAOS:-1}" = 0 ] && chaos_status=0
fi
log "suite exit $suite_status, contracts exit $contracts_status, chaos exit $chaos_status"
[ "$suite_status" -eq 0 ] && [ "$contracts_status" -eq 0 ] && [ "$chaos_status" -eq 0 ]
