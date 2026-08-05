#!/bin/sh
# Minimal Router OS — self-contained Alpine distribution installer.
set -eu

if [ "$(id -u)" -ne 0 ]; then
    echo "ERROR: install.sh must run as root" >&2
    exit 1
fi
if [ ! -f /etc/alpine-release ] || ! command -v apk >/dev/null 2>&1; then
    echo "ERROR: this distribution installer supports Alpine Linux only" >&2
    exit 1
fi

echo "=== Minimal Router OS Distribution Installer ==="
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

ARCH="$(uname -m)"
case "$ARCH" in
    x86_64)  BIN_ARCH="amd64" ;;
    aarch64) BIN_ARCH="arm64" ;;
    *)       echo "ERROR: Unsupported architecture: $ARCH" >&2; exit 1 ;;
esac
echo "Architecture: $ARCH ($BIN_ARCH)"

for required in \
    "bin/routerd-${BIN_ARCH}" \
    "bin/router-applyd-${BIN_ARCH}" \
    "bin/router-recovery-${BIN_ARCH}" \
    "bin/router-update-${BIN_ARCH}" \
    "web/dist/index.html" \
    "slot-exec" \
    "init.d/routerd" \
    "init.d/router-applyd" \
    "init.d/pppoe-wan" \
    "sysctl/99-minimalrouter.conf" \
    "modules/minimalrouter.conf" \
    "logrotate/minimalrouter"
do
    [ -f "$required" ] || {
        echo "ERROR: Missing distribution file: $required" >&2
        exit 1
    }
done

ALPINE_VERSION="v3.22"
if ! grep -q "$ALPINE_VERSION" /etc/apk/repositories 2>/dev/null; then
    echo "https://dl-cdn.alpinelinux.org/alpine/$ALPINE_VERSION/main" > /etc/apk/repositories
    echo "https://dl-cdn.alpinelinux.org/alpine/$ALPINE_VERSION/community" >> /etc/apk/repositories
fi

OFFLINE_MODE=0
if [ "${1:-}" = "--offline" ]; then
    OFFLINE_MODE=1
elif [ -n "${1:-}" ]; then
    echo "Usage: $0 [--offline]" >&2
    exit 1
fi

if [ "${MINIMALROUTER_OFFLINE:-}" = "1" ]; then
    OFFLINE_MODE=1
fi

REQUIRED_PACKAGES="nftables ppp ppp-pppoe dnsmasq iproute2 iputils-ping iputils-arping doas ca-certificates wireguard-tools-wg squid hostapd hostapd-openrc iw inadyn inadyn-openrc chrony chrony-openrc logrotate"

if [ "$OFFLINE_MODE" -eq 1 ]; then
    echo "[1/7] Checking dependencies (offline mode)..."
    MISSING_PKGS=""
    for pkg in $REQUIRED_PACKAGES; do
        if apk info -e "$pkg" >/dev/null 2>&1; then
            echo "  ✓ $pkg"
        else
            echo "  ✗ $pkg (MISSING)"
            MISSING_PKGS="$MISSING_PKGS $pkg"
        fi
    done
    
    if [ -n "$MISSING_PKGS" ]; then
        echo "ERROR: The following required packages are missing for offline installation:" >&2
        echo "$MISSING_PKGS" >&2
        exit 1
    fi
    echo "All required dependencies already installed."
    echo "Continuing offline installation..."
else
    echo "[1/7] Installing dependencies..."
    apk update
    apk add --no-cache $REQUIRED_PACKAGES
fi

# Router authentication, TLS, schedules, audit ordering, and signed-update
# verification all depend on a trustworthy clock. Run chronyd as a client only:
# no NTP server socket and no remote command socket are exposed.
install -d -m 0755 -o root -g root /etc/chrony
cat > /etc/chrony/chrony.conf <<'CHRONY_CONFIG'
pool pool.ntp.org iburst maxsources 4
driftfile /var/lib/chrony/chrony.drift
makestep 1.0 3
rtcsync
port 0
cmdport 0
noclientlog
CHRONY_CONFIG
chmod 0644 /etc/chrony/chrony.conf
if [ -f /etc/conf.d/chronyd ]; then
    if grep -q '^FAST_STARTUP=' /etc/conf.d/chronyd; then
        sed -i 's/^FAST_STARTUP=.*/FAST_STARTUP=yes/' /etc/conf.d/chronyd
    else
        printf '\nFAST_STARTUP=yes\n' >> /etc/conf.d/chronyd
    fi
fi

echo "[2/7] Creating user..."
if ! id -u routerd >/dev/null 2>&1; then
    addgroup -S routerd
    adduser -S -D -H -h /var/lib/minimalrouter -s /sbin/nologin -G routerd routerd
fi

echo "[3/7] Installing bootstrap payload and stable command dispatcher..."
install -d -m 0700 -o routerd -g routerd /var/lib/minimalrouter
install -d -m 0700 -o root -g root /var/lib/minimalrouter-applyd
install -d -m 0750 -o root -g routerd /run/minimalrouter
install -d -m 0750 -o root -g inadyn /etc/inadyn
install -d -m 0755 -o root -g root \
    /usr/libexec/minimalrouter/bootstrap/bin \
    /usr/libexec/minimalrouter/bootstrap/web/dist \
    /usr/share/minimalrouter \
    /etc/minimalrouter \
    /var/lib/minimalrouter-update \
    /var/lib/minimalrouter-update/slots
install -d -m 0700 -o root -g root /etc/ppp/peers /etc/hostapd
install -d -m 0755 -o root -g root /etc/dnsmasq.d /etc/modules-load.d /etc/logrotate.d

install -m 0755 "bin/routerd-${BIN_ARCH}" "/usr/libexec/minimalrouter/bootstrap/bin/routerd-${BIN_ARCH}"
install -m 0755 "bin/router-applyd-${BIN_ARCH}" "/usr/libexec/minimalrouter/bootstrap/bin/router-applyd-${BIN_ARCH}"
install -m 0750 "bin/router-recovery-${BIN_ARCH}" "/usr/libexec/minimalrouter/bootstrap/bin/router-recovery-${BIN_ARCH}"
install -m 0750 "bin/router-update-${BIN_ARCH}" "/usr/libexec/minimalrouter/bootstrap/bin/router-update-${BIN_ARCH}"
install -m 0755 slot-exec /usr/libexec/minimalrouter/slot-exec

ln -sf /usr/libexec/minimalrouter/slot-exec /usr/bin/routerd
ln -sf /usr/libexec/minimalrouter/slot-exec /usr/sbin/router-applyd
ln -sf /usr/libexec/minimalrouter/slot-exec /usr/sbin/router-recovery
ln -sf /usr/libexec/minimalrouter/slot-exec /usr/sbin/router-update

if [ -f firmware-signing.pub ]; then
    if [ -f /etc/minimalrouter/firmware-signing.pub ] && \
       ! cmp -s firmware-signing.pub /etc/minimalrouter/firmware-signing.pub; then
        echo "ERROR: refusing to replace the installed firmware trust anchor" >&2
        exit 1
    fi
    install -m 0644 -o root -g root firmware-signing.pub /etc/minimalrouter/firmware-signing.pub
else
    echo "NOTE: unsigned development archive; router-update staging remains disabled until a trusted public key is installed."
fi

echo "[4/7] Installing dashboard..."
rm -rf /usr/libexec/minimalrouter/bootstrap/web/dist/*
cp -R web/dist/. /usr/libexec/minimalrouter/bootstrap/web/dist/
chown -R root:root /usr/libexec/minimalrouter/bootstrap/web
chmod -R a+rX /usr/libexec/minimalrouter/bootstrap/web
rm -rf /usr/share/minimalrouter/web
ln -s /usr/libexec/minimalrouter/bootstrap/web/dist /usr/share/minimalrouter/web

echo "[5/7] Installing service and kernel configuration..."
cp init.d/routerd /etc/init.d/routerd
cp init.d/router-applyd /etc/init.d/router-applyd
cp init.d/pppoe-wan /etc/init.d/pppoe-wan
cp sysctl/99-minimalrouter.conf /etc/sysctl.d/99-minimalrouter.conf
cp modules/minimalrouter.conf /etc/modules-load.d/minimalrouter.conf
cp logrotate/minimalrouter /etc/logrotate.d/minimalrouter
chmod 0755 /etc/init.d/router-applyd /etc/init.d/routerd /etc/init.d/pppoe-wan
chmod 0644 /etc/sysctl.d/99-minimalrouter.conf /etc/modules-load.d/minimalrouter.conf /etc/logrotate.d/minimalrouter

echo "[6/7] Loading router kernel modules and sysctls..."
while IFS= read -r module; do
    case "$module" in ""|\#*) continue ;; esac
    grep -qxF "$module" /etc/modules 2>/dev/null || printf '%s\n' "$module" >> /etc/modules
    if ! modprobe "$module"; then
        if [ "$module" = "pppoe" ]; then
            echo "ERROR: the running Alpine kernel does not provide the required PPPoE module." >&2
            echo "The 2026-08-01 Proxmox pilot required linux-lts; boot linux-lts, confirm 'modprobe pppoe', then rerun this installer." >&2
        else
            echo "ERROR: required kernel module '$module' could not be loaded." >&2
        fi
        exit 1
    fi
done < modules/minimalrouter.conf

sysctl -p /etc/sysctl.d/99-minimalrouter.conf >/dev/null
[ "$(sysctl -n net.ipv4.ip_forward)" = "1" ] || {
    echo "ERROR: IPv4 forwarding did not activate" >&2
    exit 1
}
[ "$(sysctl -n net.ipv4.conf.all.rp_filter)" = "2" ] || {
    echo "ERROR: loose reverse-path filtering did not activate" >&2
    exit 1
}
[ "$(sysctl -n net.netfilter.nf_conntrack_max)" = "131072" ] || {
    echo "ERROR: conntrack state ceiling did not activate" >&2
    exit 1
}

echo "[7/7] Enabling services..."
for svc in sshd dropbear telnetd httpd miniupnpd upnpd rpcbind; do
    rc-service "$svc" stop >/dev/null 2>&1 || true
    rc-update del "$svc" default >/dev/null 2>&1 || true
done
rc-update add chronyd default
rc-update add router-applyd default
rc-update add routerd default

echo "=== Installation complete ==="
echo "Start now: rc-service chronyd start && rc-service router-applyd start && rc-service routerd start"
echo "Or reboot once; all three services are enabled for the default runlevel."
LAN_IP="$(ip -4 addr show 2>/dev/null | grep -o 'inet [0-9.]*' | grep -v '127.0.0.1' | head -1 | cut -d' ' -f2)"
[ -n "$LAN_IP" ] && echo "Current management candidate: https://${LAN_IP}:8443"
echo "Default first-run management address after routerd reconciliation: https://192.168.1.1:8443"
