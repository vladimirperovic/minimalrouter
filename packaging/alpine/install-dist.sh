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
    "compatibility.json" \
    "init.d/routerd" \
    "init.d/router-applyd" \
    "init.d/pppoe-wan" \
    "sysctl/99-minimalrouter.conf" \
    "modules/minimalrouter.conf" \
    "logrotate/minimalrouter" \
    "ip-up.d-minimalrouter-qos"
do
    [ -f "$required" ] || {
        echo "ERROR: Missing distribution file: $required" >&2
        exit 1
    }
done

ALPINE_VERSION="v3.22"
IMAGE_BUILD="${MINIMALROUTER_IMAGE_BUILD:-0}"
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

# The all-in-one ISO supplies a caller-managed local Alpine repository. Never
# replace it with CDN URLs in offline mode: setup-disk runs later in the same
# live environment and must remain installable with zero WAN connectivity.
if [ "$OFFLINE_MODE" -eq 0 ] && ! grep -q "$ALPINE_VERSION" /etc/apk/repositories 2>/dev/null; then
    echo "https://dl-cdn.alpinelinux.org/alpine/$ALPINE_VERSION/main" > /etc/apk/repositories
    echo "https://dl-cdn.alpinelinux.org/alpine/$ALPINE_VERSION/community" >> /etc/apk/repositories
fi

REQUIRED_PACKAGES="nftables ppp ppp-pppoe dnsmasq iproute2 iputils-ping iputils-arping ca-certificates openssh-server wireguard-tools-wg doas squid hostapd hostapd-openrc iw inadyn inadyn-openrc chrony chrony-openrc logrotate"

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

# Fail before replacing appliance runtime files if the running kernel cannot
# support a required router feature. Loading an already-available module is
# harmless and avoids a half-installed system whose A/B pointer still claims a
# healthy release after a late module failure.
if [ "$IMAGE_BUILD" != "1" ]; then
while IFS= read -r module; do
    case "$module" in ""|\#*) continue ;; esac
    if ! modprobe "$module" 2>/dev/null && ! find /lib/modules -name "${module}.ko*" 2>/dev/null | grep -q .; then
        if [ "$module" = "pppoe" ]; then
            echo "ERROR: the running Alpine kernel does not provide the required PPPoE module." >&2
        else
            echo "ERROR: required kernel module '$module' could not be loaded." >&2
        fi
        exit 1
    fi
done < modules/minimalrouter.conf
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

echo "[2/7] Creating users and private state directories..."
if ! id -u routerd >/dev/null 2>&1; then
    addgroup -S routerd
    adduser -S -D -H -h /var/lib/minimalrouter -s /sbin/nologin -G routerd routerd
fi
if ! id -u dnsmasq >/dev/null 2>&1; then
    echo "ERROR: the installed dnsmasq package did not create its service account" >&2
    exit 1
fi

# Canonical config/auth state belongs only to routerd; DHCP leases use a
# separate persistent directory owned by dnsmasq so persistence never requires
# weakening the 0700 routerd data directory.
install -d -m 0700 -o routerd -g routerd /var/lib/minimalrouter
install -d -m 0750 -o dnsmasq -g dnsmasq /var/lib/minimalrouter-dhcp
if [ ! -e /var/lib/minimalrouter-dhcp/dnsmasq.leases ]; then
    install -m 0640 -o dnsmasq -g dnsmasq /dev/null /var/lib/minimalrouter-dhcp/dnsmasq.leases
else
    chown dnsmasq:dnsmasq /var/lib/minimalrouter-dhcp/dnsmasq.leases
    chmod 0640 /var/lib/minimalrouter-dhcp/dnsmasq.leases
fi
install -d -m 0700 -o root -g root /var/lib/minimalrouter-applyd

# routerd needs read-only live WireGuard statistics, but `wg show ... dump`
# contains interface private and peer preshared keys. Grant exactly the four
# non-secret projections for wg0/wg1 and no other root command or argument.
# The two nft entries expose only the per-device byte counters used by traffic
# accounting; the argument vector is matched in full, so neither can be turned
# into general ruleset access.
install -d -m 0755 -o root -g root /etc/doas.d
cat > /etc/doas.d/50-minimalrouter.conf <<'DOAS_CONFIG'
permit nopass routerd as root cmd /usr/bin/wg args show wg0 endpoints
permit nopass routerd as root cmd /usr/bin/wg args show wg0 allowed-ips
permit nopass routerd as root cmd /usr/bin/wg args show wg0 latest-handshakes
permit nopass routerd as root cmd /usr/bin/wg args show wg0 transfer
permit nopass routerd as root cmd /usr/bin/wg args show wg1 endpoints
permit nopass routerd as root cmd /usr/bin/wg args show wg1 allowed-ips
permit nopass routerd as root cmd /usr/bin/wg args show wg1 latest-handshakes
permit nopass routerd as root cmd /usr/bin/wg args show wg1 transfer
permit nopass routerd as root cmd /usr/sbin/nft args -j list set inet minimalrouter acct_rx
permit nopass routerd as root cmd /usr/sbin/nft args -j list set inet minimalrouter acct_tx
DOAS_CONFIG
chmod 0400 /etc/doas.d/50-minimalrouter.conf

echo "[3/7] Installing bootstrap payload and stable command dispatcher..."
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
install -d -m 0755 -o root -g root /etc/ppp/ip-up.d /etc/ppp/ip-down.d
install -d -m 0755 -o root -g root /etc/dnsmasq.d /etc/modules-load.d /etc/logrotate.d
install -m 0755 ip-up.d-minimalrouter-qos /etc/ppp/ip-up.d/minimalrouter-qos

install -m 0755 "bin/routerd-${BIN_ARCH}" "/usr/libexec/minimalrouter/bootstrap/bin/routerd-${BIN_ARCH}"
install -m 0755 "bin/router-applyd-${BIN_ARCH}" "/usr/libexec/minimalrouter/bootstrap/bin/router-applyd-${BIN_ARCH}"
install -m 0750 "bin/router-recovery-${BIN_ARCH}" "/usr/libexec/minimalrouter/bootstrap/bin/router-recovery-${BIN_ARCH}"
install -m 0750 "bin/router-update-${BIN_ARCH}" "/usr/libexec/minimalrouter/bootstrap/bin/router-update-${BIN_ARCH}"
install -m 0755 slot-exec /usr/libexec/minimalrouter/slot-exec
install -m 0644 -o root -g root compatibility.json /etc/minimalrouter/compatibility.json

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
# Web UI prati aktuelni A/B slot (web/dist u slotu, ili web/ za starije sloteve),
# uz bootstrap kao pad za prvi boot pre aktivacije slota.
WEB_TARGET="/var/lib/minimalrouter-update/current/web/dist"
[ -d "$WEB_TARGET" ] || WEB_TARGET="/var/lib/minimalrouter-update/current/web"
if [ -d "$WEB_TARGET" ] && [ -f "$WEB_TARGET/index.html" ]; then
    ln -s "$WEB_TARGET" /usr/share/minimalrouter/web
else
    ln -s /usr/libexec/minimalrouter/bootstrap/web/dist /usr/share/minimalrouter/web
fi

echo "[5/7] Installing service and kernel configuration..."
cp init.d/routerd /etc/init.d/routerd
cp init.d/router-applyd /etc/init.d/router-applyd
cp init.d/pppoe-wan /etc/init.d/pppoe-wan
cp sysctl/99-minimalrouter.conf /etc/sysctl.d/99-minimalrouter.conf
cp modules/minimalrouter.conf /etc/modules-load.d/minimalrouter.conf
cp logrotate/minimalrouter /etc/logrotate.d/minimalrouter
chmod 0755 /etc/init.d/router-applyd /etc/init.d/routerd /etc/init.d/pppoe-wan
chmod 0644 /etc/sysctl.d/99-minimalrouter.conf /etc/modules-load.d/minimalrouter.conf /etc/logrotate.d/minimalrouter

echo "[6/7] Preparing kernel/module configuration..."
if [ "$IMAGE_BUILD" != "1" ]; then
while IFS= read -r module; do
    case "$module" in ""|\#*) continue ;; esac
    grep -qxF "$module" /etc/modules 2>/dev/null || printf '%s\n' "$module" >> /etc/modules
    # Required modules were preflighted before root runtime replacement. Load
    # again here after persistence so the installed state and current kernel
    # are proven together.
    if ! modprobe "$module" 2>/dev/null && [ -z "$(find /lib/modules -name "${module}.ko*" 2>/dev/null | head -1)" ]; then
        echo "ERROR: required kernel module '$module' could not be loaded or found in bundled modules" >&2
        exit 1
    fi
done < modules/minimalrouter.conf

sysctl -p /etc/sysctl.d/99-minimalrouter.conf >/dev/null
[ "$(sysctl -n net.ipv4.ip_forward)" = "0" ] || {
    echo "ERROR: first-run IPv4 forwarding did not remain disabled" >&2
    exit 1
}
[ "$(sysctl -n net.ipv4.conf.all.rp_filter)" = "2" ] || {
    echo "ERROR: loose reverse-path filtering did not activate" >&2
    exit 1
}
if [ "$(sysctl -n net.netfilter.nf_conntrack_max)" != "131072" ]; then
    # nf_conntrack se ucitava tek na ciljnom sistemu (live kernel nema module);
    # provera da modul postoji u bundle-ovanim modulima umesto da se zahteva live.
    if [ -z "$(find /lib/modules -name "nf_conntrack.ko*" 2>/dev/null | head -1)" ]; then
        echo "ERROR: conntrack state ceiling did not activate and nf_conntrack is unavailable" >&2
        exit 1
    fi
fi

fi

# MinimalRouter owns every WAN/LAN/tunnel interface: router-applyd assigns the
# LAN address, pppd owns the WAN, and wg(8) owns the tunnels. A distribution
# /etc/network/interfaces that still carries "iface eth0 inet dhcp" competes
# with all three -- it delays boot on `need net`, can hand the WAN interface an
# unexpected DHCP lease, and can re-run ifup against an address the helper has
# already installed. The isolated lab has always installed this file by hand
# (scripts/lab/payloads/mr-install.sh); shipping it here is what makes a normal
# setup-alpine host match the tested configuration.
if [ -f /etc/network/interfaces ] && [ ! -f /etc/network/interfaces.minimalrouter-backup ]; then
    cp -p /etc/network/interfaces /etc/network/interfaces.minimalrouter-backup
    echo "Saved previous network configuration to /etc/network/interfaces.minimalrouter-backup"
fi
install -d -m 0755 -o root -g root /etc/network
{
    echo "# Managed by minimalrouter. Physical/tunnel interfaces are configured by router-applyd/pppd/wg."
    echo "auto lo"
    echo "iface lo inet loopback"
    if [ "$IMAGE_BUILD" != "1" ]; then
        echo ""
        for managed_interface_path in /sys/class/net/*; do
            [ -e "$managed_interface_path" ] || continue
            managed_interface=${managed_interface_path##*/}
            case "$managed_interface" in lo|ppp*|wg*|ifb*|veth*|docker*|br-*) continue ;; esac
            echo "iface $managed_interface inet manual"
        done
    fi
} > /etc/network/interfaces
chmod 0644 /etc/network/interfaces

# cloud-init re-applies its own network configuration on every boot and will
# undo the file above on cloud images.
if rc-service --exists cloud-init >/dev/null 2>&1; then
    rc-service cloud-init stop >/dev/null 2>&1 || true
    rc-update del cloud-init default >/dev/null 2>&1 || true
fi

echo "[7/7] Enabling services..."
# MinimalRouter owns every router interface. dhcpcd must not race
# router-applyd/pppd for DHCP addresses or default routes at boot.
for svc in dhcpcd dropbear telnetd httpd miniupnpd upnpd rpcbind; do
    rc-service "$svc" stop >/dev/null 2>&1 || true
    rc-update del "$svc" default >/dev/null 2>&1 || true
done
rc-update add chronyd default
rc-update add sshd default
rc-update add router-applyd default
rc-update add routerd default

# Commit the first A/B rollback target only after every critical kernel/sysctl
# check and OpenRC registration has succeeded. A failed full installer may
# leave newly copied files for the operator to rerun, but it must never advance
# current/state.json to a baseline that was not proven installable.
BASELINE_HASH="$({ \
    sha256sum "/usr/libexec/minimalrouter/bootstrap/bin/routerd-${BIN_ARCH}"; \
    sha256sum "/usr/libexec/minimalrouter/bootstrap/bin/router-applyd-${BIN_ARCH}"; \
    sha256sum /usr/libexec/minimalrouter/bootstrap/web/dist/index.html; \
} | sha256sum | cut -c1-16)"
BASELINE_VERSION="0.0.0+bootstrap.${BASELINE_HASH}"
BASELINE_SLOT="/var/lib/minimalrouter-update/slots/${BASELINE_VERSION}"
rm -rf "$BASELINE_SLOT"
install -d -m 0755 -o root -g root "$BASELINE_SLOT/bin" "$BASELINE_SLOT/web/dist"
install -m 0755 "/usr/libexec/minimalrouter/bootstrap/bin/routerd-${BIN_ARCH}" "$BASELINE_SLOT/bin/routerd-${BIN_ARCH}"
install -m 0755 "/usr/libexec/minimalrouter/bootstrap/bin/router-applyd-${BIN_ARCH}" "$BASELINE_SLOT/bin/router-applyd-${BIN_ARCH}"
install -m 0750 "/usr/libexec/minimalrouter/bootstrap/bin/router-recovery-${BIN_ARCH}" "$BASELINE_SLOT/bin/router-recovery-${BIN_ARCH}"
install -m 0750 "/usr/libexec/minimalrouter/bootstrap/bin/router-update-${BIN_ARCH}" "$BASELINE_SLOT/bin/router-update-${BIN_ARCH}"
cp -R /usr/libexec/minimalrouter/bootstrap/web/dist/. "$BASELINE_SLOT/web/dist/"
chown -R root:root "$BASELINE_SLOT"
chmod -R a+rX "$BASELINE_SLOT/web"

OLD_CURRENT_TARGET="$(readlink /var/lib/minimalrouter-update/current 2>/dev/null || true)"
OLD_CURRENT_VERSION=""
case "$OLD_CURRENT_TARGET" in
    slots/*) OLD_CURRENT_VERSION="${OLD_CURRENT_TARGET#slots/}" ;;
esac

rm -f /var/lib/minimalrouter-update/.current-new
ln -s "slots/${BASELINE_VERSION}" /var/lib/minimalrouter-update/.current-new
# BusyBox mv follows a destination symlink to a directory unless -T is used.
# Without it, a reinstall can leave current pointing at the old slot while
# state.json claims the new baseline is active.
mv -fT /var/lib/minimalrouter-update/.current-new /var/lib/minimalrouter-update/current

if [ -n "$OLD_CURRENT_VERSION" ] && [ "$OLD_CURRENT_VERSION" != "$BASELINE_VERSION" ] && \
   [ -d "/var/lib/minimalrouter-update/slots/${OLD_CURRENT_VERSION}" ]; then
    rm -f /var/lib/minimalrouter-update/.previous-new
    ln -s "slots/${OLD_CURRENT_VERSION}" /var/lib/minimalrouter-update/.previous-new
    mv -fT /var/lib/minimalrouter-update/.previous-new /var/lib/minimalrouter-update/previous
else
    rm -f /var/lib/minimalrouter-update/previous
    OLD_CURRENT_VERSION=""
fi

STATE_TMP="/var/lib/minimalrouter-update/.state-install-$$"
printf '{"current":"%s","previous":"%s","pending":""}\n' "$BASELINE_VERSION" "$OLD_CURRENT_VERSION" > "$STATE_TMP"
chmod 0644 "$STATE_TMP"
mv -f "$STATE_TMP" /var/lib/minimalrouter-update/state.json
sync

echo "=== Installation complete ==="
echo "Reboot now to complete the first-run setup: the installed system finalizes Minimal Router OS on boot."
echo "Or reboot once; all three services are enabled for the default runlevel."
LAN_IP="$(ip -4 addr show 2>/dev/null | grep -o 'inet [0-9.]*' | grep -v '127.0.0.1' | head -1 | cut -d' ' -f2)"
[ -n "$LAN_IP" ] && echo "Current management candidate: https://${LAN_IP}:8443"
echo "Default first-run management address after router-applyd setup reconciliation: https://192.168.1.1:8443"
