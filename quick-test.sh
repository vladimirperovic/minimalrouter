#!/bin/sh
# Minimal Router OS — Quick VM test (one command)
# Boots Alpine VM, installs router, runs tests, leaves VM running for dashboard access.
# Usage: sh quick-test.sh [--teardown]
set -e

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
VM_DIR="${MINIMALROUTER_VM_DIR:-/private/tmp/minimalrouter-alpine-3.22.5}"
REPO="$SCRIPT_DIR"
API="https://192.168.1.1:8443"
PASSWD="${MINIMALROUTER_TEST_PASSWORD:-SuperSecure12345678}"
LOG="$VM_DIR/quick-test.log"
PID_FILE="$VM_DIR/quick-test.pid"

# Parse args
TEARDOWN=0
for arg in "$@"; do
    case "$arg" in
        --teardown) TEARDOWN=1 ;;
    esac
done

cleanup() {
    if [ "$TEARDOWN" = "1" ]; then
        echo "Tearing down existing VM..."
        if [ -f "$PID_FILE" ]; then
            old_pid="$(cat "$PID_FILE")"
            case "$old_pid" in
                *[!0-9]*|"") echo "Ignoring invalid PID file: $PID_FILE" >&2 ;;
                *) kill "$old_pid" 2>/dev/null || true ;;
            esac
            rm -f "$PID_FILE"
            sleep 2
        fi
    fi
}

# Build everything first
echo "=== Step 1: Building binaries + web assets ==="
cd "$REPO"
make build-linux-arm64 2>&1 | tail -3
pnpm --dir web build 2>&1 | tail -8

# Verify builds
[ -f bin/routerd-linux-arm64 ] || { echo "ERROR: bin/routerd-linux-arm64 not found" >&2; exit 1; }
[ -f bin/router-applyd-linux-arm64 ] || { echo "ERROR: bin/router-applyd-linux-arm64 not found" >&2; exit 1; }
[ -f web/dist/index.html ] || { echo "ERROR: web/dist/index.html not found" >&2; exit 1; }

echo "=== Step 2: Preparing VM ==="
mkdir -p "$VM_DIR"

echo "=== Step 3: Booting VM ==="
cleanup

# Write the pty script — all VM-side logic uses ash + jq (no python3, no node)
cat > "$VM_DIR/quick-test-pty.py" << 'PYEOF'
import fcntl
import os
import pty
import signal
import sys
import time

VM_DIR = os.environ["MINIMALROUTER_VM_DIR"]
IMAGE = f"{VM_DIR}/boot/Image"
INITRAMFS = f"{VM_DIR}/boot/initramfs-virt"
ISO = f"{VM_DIR}/alpine-virt-3.22.5-aarch64.iso"
REPO = os.environ["MINIMALROUTER_REPO"]
RUNNER = f"{VM_DIR}/minimalrouter-alpine-vm"
API = "https://192.168.1.1:8443"
PASSWD = os.environ["MINIMALROUTER_TEST_PASSWORD"]

master_fd, slave_fd = pty.openpty()
pid = os.fork()
if pid == 0:
    os.close(master_fd)
    os.dup2(slave_fd, 0)
    os.dup2(slave_fd, 1)
    os.dup2(slave_fd, 2)
    os.close(slave_fd)
    os.execv(RUNNER, [RUNNER, IMAGE, INITRAMFS, ISO, REPO])
else:
    os.close(slave_fd)
    flags = fcntl.fcntl(master_fd, fcntl.F_GETFL)
    fcntl.fcntl(master_fd, fcntl.F_SETFL, flags | os.O_NONBLOCK)

    def stop_vm(_signum=None, _frame=None):
        try:
            os.kill(pid, signal.SIGTERM)
            os.waitpid(pid, 0)
        except OSError:
            pass
        raise SystemExit(0)

    signal.signal(signal.SIGTERM, stop_vm)
    signal.signal(signal.SIGINT, stop_vm)

    def drain(timeout=2):
        end = time.time() + timeout
        out = ""
        while time.time() < end:
            try:
                data = os.read(master_fd, 4096).decode("utf-8", errors="replace")
                if data:
                    sys.stdout.write(data)
                    sys.stdout.flush()
                    out += data
            except (BlockingIOError, OSError):
                time.sleep(0.05)
        return out

    def send(text):
        os.write(master_fd, (text + "\n").encode())
        time.sleep(0.3)

    def cmd(command, wait=3):
        send(command)
        return drain(wait)

    # ── Boot & Install ──
    drain(15)
    cmd("root", 5)
    cmd("setup-interfaces -a", 3)
    for _ in range(8):
        send("")
        drain(1)
    cmd("ifup -a", 5)
    drain(2)
    cmd("mkdir -p /mnt/minimalrouter", 1)
    cmd("mount -t virtiofs minimalrouter /mnt/minimalrouter", 2)
    cmd("printf '%s\\n' https://dl-cdn.alpinelinux.org/alpine/v3.22/main https://dl-cdn.alpinelinux.org/alpine/v3.22/community > /etc/apk/repositories", 1)
    cmd("apk update", 15)
    cmd("apk add --no-cache nftables ppp ppp-pppoe dnsmasq iproute2 curl ca-certificates wireguard-tools-wg squid jq", 60)
    cmd("cd /mnt/minimalrouter && sh packaging/alpine/install.sh", 30)
    cmd("ip link add eth1 type dummy && ip link set eth1 up", 2)
    cmd("touch /var/log/routerd.log /var/log/routerd.err /var/log/router-applyd.log /var/log/router-applyd.err", 1)
    cmd("chown routerd:routerd /var/log/routerd.log /var/log/routerd.err", 1)
    cmd("rc-service router-applyd start", 5)
    cmd("rc-service routerd start", 5)
    cmd("sleep 2", 3)

    # Run setup wizard
    cmd(f"curl -sk -X POST {API}/api/v1/setup/apply -H 'Content-Type: application/json' -d '{{\"admin_password\":\"{PASSWD}\",\"pppoe_username\":\"\", \"pppoe_password\":\"\", \"lan_ip_address\":\"192.168.1.1\", \"lan_interface\":\"eth1\", \"wan_interface\":\"eth0\"}}' -c /tmp/cookies.txt 2>&1", 10)

    # ── Run tests from inside the VM using ash + jq (no python3, no node) ──
    print("\n" + "=" * 60)
    print("  VM READY — Running tests from inside VM (ash + jq)")
    print("=" * 60 + "\n")
    sys.stdout.flush()

    # Login test — no _csrf field (DisallowUnknownFields rejects it)
    cmd(f"curl -sk -X POST {API}/api/v1/auth/login -H 'Content-Type: application/json' -d '{{\"password\":\"{PASSWD}\"}}' -c /tmp/cookies.txt -o /dev/null -w '%{{http_code}}' 2>&1", 5)

    # Get CSRF token from session endpoint
    cmd(f"curl -sk {API}/api/v1/auth/session -b /tmp/cookies.txt 2>/dev/null | jq -r '.csrf_token' > /tmp/csrf.txt", 3)
    cmd("echo \"CSRF=$(cat /tmp/csrf.txt)\"", 2)

    # Config test (includes firewall config)
    cmd(f"curl -sk {API}/api/v1/config -b /tmp/cookies.txt -H \"X-CSRF-Token: $(cat /tmp/csrf.txt)\" 2>/dev/null | jq -r '.revision'", 3)
    cmd(f"curl -sk {API}/api/v1/config -b /tmp/cookies.txt -H \"X-CSRF-Token: $(cat /tmp/csrf.txt)\" 2>/dev/null | jq -r '.firewall.default_wan_input_policy'", 3)
    cmd(f"curl -sk {API}/api/v1/config -b /tmp/cookies.txt -H \"X-CSRF-Token: $(cat /tmp/csrf.txt)\" 2>/dev/null | jq -r '.firewall.wan_ingress_mode'", 3)
    cmd(f"curl -sk {API}/api/v1/config -b /tmp/cookies.txt -H \"X-CSRF-Token: $(cat /tmp/csrf.txt)\" 2>/dev/null | jq -r '.firewall.port_forwards | length'", 3)

    # System test
    cmd(f"curl -sk {API}/api/v1/system -b /tmp/cookies.txt -H \"X-CSRF-Token: $(cat /tmp/csrf.txt)\" 2>/dev/null | jq -r '.hostname'", 3)
    cmd(f"curl -sk {API}/api/v1/system -b /tmp/cookies.txt -H \"X-CSRF-Token: $(cat /tmp/csrf.txt)\" 2>/dev/null | jq -r '.runtime.memory_used_bytes / 1048576'", 3)
    cmd(f"curl -sk {API}/api/v1/system -b /tmp/cookies.txt -H \"X-CSRF-Token: $(cat /tmp/csrf.txt)\" 2>/dev/null | jq -r '.runtime.disk_used_bytes / 1048576'", 3)
    cmd("printf '%s\\n' '4102444800 aa:bb:cc:dd:ee:ff 192.168.1.42 test-phone 01:aa:bb' > /run/minimalrouter/dnsmasq.leases && chmod 0644 /run/minimalrouter/dnsmasq.leases", 2)
    cmd(f"curl -sk {API}/api/v1/system -b /tmp/cookies.txt -H \"X-CSRF-Token: $(cat /tmp/csrf.txt)\" 2>/dev/null | jq -e '.runtime.dhcp_leases[0] | .hostname == \"test-phone\" and .ip_address == \"192.168.1.42\"' && echo 'PASS:live DHCP lease telemetry'", 3)
    cmd("apk info | wc -l", 3)
    cmd("apk stats", 3)

    # Removed runtime dependencies and secret-file placement
    cmd("for p in bash wireguard-tools cryptsetup e2fsprogs lvm2; do apk info -e \"$p\" >/dev/null 2>&1 && echo \"UNEXPECTED:$p\" || echo \"ABSENT:$p\"; done", 5)
    cmd("for p in hostapd hostapd-openrc iw inadyn inadyn-openrc; do apk info -e \"$p\" >/dev/null 2>&1 && echo \"PRESENT:$p\" || echo \"MISSING:$p\"; done", 5)
    cmd("hostapd -v 2>&1 | head -2; iw --version 2>&1; inadyn --version 2>&1 | head -2", 4)
    cmd("test -f /run/minimalrouter/wg0.runtime.conf && echo 'PASS:WireGuard runtime config is in RAM'", 3)
    cmd("test ! -e /etc/wireguard/wg0.conf && test ! -e /etc/minimalrouter/wg0.runtime.conf && echo 'PASS:no persistent WireGuard service files'", 3)

    # New feature adapters: generated syntax/package path and fail-closed
    # behavior when the VM has no physical Wi-Fi radio or Cloudflare account.
    cmd("printf '%s\\n' 'period = 300' 'secure-ssl = true' 'provider cloudflare.com:1 {' ' username = \"example.com\"' ' password = \"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA\"' ' hostname = \"home.example.com\"' ' ttl = 300' ' proxied = false' '}' > /tmp/inadyn.conf && inadyn --check-config -f /tmp/inadyn.conf && echo 'PASS:inadyn Cloudflare config accepted'", 5)
    cmd(f"curl -sk {API}/api/v1/config -b /tmp/cookies.txt > /tmp/wifi-base.json && jq '.wifi={{\"enabled\":true,\"interface\":\"wlan0\",\"ssid\":\"MinimalRouter-Test\",\"passphrase\":\"TestWiFiPassword123\",\"band\":\"5ghz\",\"channel\":36,\"hide_ssid\":false}}' /tmp/wifi-base.json > /tmp/wifi-test.json && curl -sk -o /tmp/wifi-result.json -w 'WIFI_HTTP:%{{http_code}}\\n' -X PUT {API}/api/v1/config -b /tmp/cookies.txt -H \"X-CSRF-Token: $(cat /tmp/csrf.txt)\" -H 'Content-Type: application/json' --data-binary @/tmp/wifi-test.json && jq -r '.error // .state' /tmp/wifi-result.json", 8)
    cmd(f"jq '.cloudflare={{\"ddns_enabled\":true,\"tunnel_enabled\":false,\"domain\":\"home.example.com\",\"zone_name\":\"example.com\",\"api_token\":\"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA\"}}' /tmp/wifi-base.json > /tmp/cf-test.json && curl -sk -o /tmp/cf-result.json -w 'CLOUDFLARE_HTTP:%{{http_code}}\\n' -X PUT {API}/api/v1/config -b /tmp/cookies.txt -H \"X-CSRF-Token: $(cat /tmp/csrf.txt)\" -H 'Content-Type: application/json' --data-binary @/tmp/cf-test.json && jq -r '.error // .state' /tmp/cf-result.json", 6)

    # Audit events
    cmd(f"curl -sk {API}/api/v1/audit/events -b /tmp/cookies.txt -H \"X-CSRF-Token: $(cat /tmp/csrf.txt)\" 2>/dev/null | jq -r '. | length'", 3)

    # nftables verification — proves WAN default-deny
    cmd("nft list ruleset 2>/dev/null | head -30", 3)

    # Service status
    cmd("rc-service routerd status 2>&1; rc-service router-applyd status 2>&1", 3)

    # Listening ports — should only show 8443, 53, 67
    cmd("ss -lntup 2>/dev/null", 3)

    print("\n" + "=" * 60)
    print("  TESTS COMPLETE")
    print("  Dashboard: https://192.168.1.1:8443")
    print(f"  Password:  {PASSWD}")
    print("=" * 60 + "\n")
    sys.stdout.flush()

    # Keep VM alive
    try:
        while True:
            time.sleep(60)
    except KeyboardInterrupt:
        stop_vm()
PYEOF

# Start VM in detached process so it survives parent shell exit
: > "$LOG"
MINIMALROUTER_VM_DIR="$VM_DIR" \
MINIMALROUTER_REPO="$REPO" \
MINIMALROUTER_TEST_PASSWORD="$PASSWD" \
nohup python3 "$VM_DIR/quick-test-pty.py" > "$LOG" 2>&1 &
VM_PID=$!
printf '%s\n' "$VM_PID" > "$PID_FILE"
disown 2>/dev/null || true
echo "VM PID: $VM_PID"

# Wait for setup to complete
echo "Waiting for VM setup (~3 min)..."
VM_TEST_COMPLETE=0
for i in $(seq 1 180); do
    if grep -q "TESTS COMPLETE" "$LOG" 2>/dev/null; then
        echo "VM setup and tests complete!"
        VM_TEST_COMPLETE=1
        break
    fi
    if grep -q "VM READY" "$LOG" 2>/dev/null; then
        echo "VM ready, tests running..."
    fi
    sleep 2
done

if [ "$VM_TEST_COMPLETE" != "1" ]; then
    echo "ERROR: VM tests did not complete within 6 minutes." >&2
    tail -40 "$LOG" >&2
    exit 1
fi

echo ""
echo "=== VM running in background (PID: $VM_PID) ==="
echo "Dashboard: https://192.168.1.1:8443"
echo "Password:  $PASSWD"
echo "Stop: kill \$(cat \"$PID_FILE\")"
echo "Log:  $LOG"
