#!/bin/sh
# Minimal Router OS — Quick VM test (one command)
# Boots Alpine VM, installs router, runs tests, leaves VM running for dashboard access.
# Usage: sh quick-test.sh [--teardown]
set -e

VM_DIR="/private/tmp/minimalrouter-alpine-3.22.5"
REPO="/Users/Vladimir/Documents/minimalrouter"
API="https://192.168.1.1:8443"
PASSWD="SuperSecure12345678"
LOG="$VM_DIR/quick-test.log"

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
        ps aux | grep minimalrouter-alpine-vm | grep -v grep | awk '{print $2}' | xargs kill 2>/dev/null || true
        sleep 2
    fi
}

# Build everything first
echo "=== Step 1: Building binaries + web assets ==="
cd "$REPO"
make build-linux-arm64 2>&1 | tail -3
if [ ! -f web/dist/index.html ]; then
    cd web && npm run build 2>&1 | tail -3
else
    echo "  (web/dist already built, skipping npm)"
fi
cd "$REPO"

# Verify builds
[ -f bin/routerd-linux-arm64 ] || { echo "ERROR: bin/routerd-linux-arm64 not found" >&2; exit 1; }
[ -f bin/router-applyd-linux-arm64 ] || { echo "ERROR: bin/router-applyd-linux-arm64 not found" >&2; exit 1; }
[ -f web/dist/index.html ] || { echo "ERROR: web/dist/index.html not found" >&2; exit 1; }

# Copy binaries to expected locations
cp bin/routerd-linux-arm64 bin/routerd
cp bin/router-applyd-linux-arm64 bin/router-applyd

echo "=== Step 2: Preparing VM ==="
mkdir -p "$VM_DIR"

# Prepare VM directory with dist content
mkdir -p "$VM_DIR/dist/bin" "$VM_DIR/dist/web/dist" "$VM_DIR/dist/init.d" "$VM_DIR/dist/sysctl" "$VM_DIR/dist/modules"
cp bin/routerd-linux-arm64 "$VM_DIR/dist/bin/routerd-arm64"
cp bin/router-applyd-linux-arm64 "$VM_DIR/dist/bin/router-applyd-arm64"
cp -R web/dist/. "$VM_DIR/dist/web/dist/"
cp packaging/alpine/routerd.initd "$VM_DIR/dist/init.d/routerd"
cp packaging/alpine/router-applyd.initd "$VM_DIR/dist/init.d/router-applyd"
cp packaging/alpine/pppoe-wan.initd "$VM_DIR/dist/init.d/pppoe-wan"
cp packaging/alpine/99-minimalrouter.conf "$VM_DIR/dist/sysctl/99-minimalrouter.conf"
cp packaging/alpine/minimalrouter.modules "$VM_DIR/dist/modules/minimalrouter.conf"
cp packaging/alpine/install-dist.sh "$VM_DIR/dist/install.sh"
chmod +x "$VM_DIR/dist/install.sh"

echo "=== Step 3: Booting VM ==="
cleanup

# Write the pty script — all VM-side logic uses ash + jq (no python3, no node)
cat > "$VM_DIR/quick-test-pty.py" << 'PYEOF'
import pty, os, sys, time, fcntl

VM_DIR = "/private/tmp/minimalrouter-alpine-3.22.5"
IMAGE = f"{VM_DIR}/boot/Image"
INITRAMFS = f"{VM_DIR}/boot/initramfs-virt"
ISO = f"{VM_DIR}/alpine-virt-3.22.5-aarch64.iso"
REPO = "/Users/Vladimir/Documents/minimalrouter"
RUNNER = f"{VM_DIR}/minimalrouter-alpine-vm"
API = "https://192.168.1.1:8443"
PASSWD = "SuperSecure12345678"

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
    cmd("apk add --no-cache nftables ppp ppp-pppoe dnsmasq iproute2 curl ca-certificates wireguard-tools-wg-quick squid jq", 60)
    cmd("cd /mnt/minimalrouter && MINIMALROUTER_ALLOW_UNENCRYPTED=1 sh packaging/alpine/install.sh", 30)
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
    cmd(f"curl -sk {API}/api/v1/system -b /tmp/cookies.txt -H \"X-CSRF-Token: $(cat /tmp/csrf.txt)\" 2>/dev/null | jq -r '.ram_used_bytes / 1048576'", 3)
    cmd(f"curl -sk {API}/api/v1/system -b /tmp/cookies.txt -H \"X-CSRF-Token: $(cat /tmp/csrf.txt)\" 2>/dev/null | jq -r '.disk_used_bytes / 1048576'", 3)
    cmd(f"curl -sk {API}/api/v1/system -b /tmp/cookies.txt -H \"X-CSRF-Token: $(cat /tmp/csrf.txt)\" 2>/dev/null | jq -r '.installed_packages'", 3)

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
    print("  Password:  SuperSecure12345678")
    print("=" * 60 + "\n")
    sys.stdout.flush()

    # Keep VM alive
    try:
        while True:
            time.sleep(60)
    except KeyboardInterrupt:
        os.kill(pid, 15)
        os.waitpid(pid, 0)
PYEOF

# Start VM in detached process so it survives parent shell exit
nohup python3 "$VM_DIR/quick-test-pty.py" > "$LOG" 2>&1 &
VM_PID=$!
disown 2>/dev/null || true
echo "VM PID: $VM_PID"

# Wait for setup to complete
echo "Waiting for VM setup (~3 min)..."
for i in $(seq 1 120); do
    if grep -q "TESTS COMPLETE" "$LOG" 2>/dev/null; then
        echo "VM setup and tests complete!"
        break
    fi
    if grep -q "VM READY" "$LOG" 2>/dev/null; then
        echo "VM ready, tests running..."
    fi
    sleep 2
done

echo ""
echo "=== VM running in background (PID: $VM_PID) ==="
echo "Dashboard: https://192.168.1.1:8443"
echo "Password:  SuperSecure12345678"
echo "Stop: kill $VM_PID"
echo "Log:  $LOG"
