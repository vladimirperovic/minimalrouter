#!/bin/sh
# Minimal Router OS — Alpine Linux Installer Script (Pinned to Alpine v3.22 stable)
set -e

ALPINE_VERSION="v3.22"
echo "=== Installing Minimal Router OS on Alpine Linux ($ALPINE_VERSION) ==="

# 1. Pin repositories.
if ! grep -q "$ALPINE_VERSION" /etc/apk/repositories 2>/dev/null; then
    echo "https://dl-cdn.alpinelinux.org/alpine/$ALPINE_VERSION/main" > /etc/apk/repositories
    echo "https://dl-cdn.alpinelinux.org/alpine/$ALPINE_VERSION/community" >> /etc/apk/repositories
fi

# 2. Install system dependencies from the pinned repository.
apk update
apk add --no-cache nftables ppp ppp-pppoe dnsmasq iproute2 iputils-ping ca-certificates openssh-server \
    wireguard-tools-wg squid hostapd hostapd-openrc iw inadyn inadyn-openrc \
    chrony chrony-openrc logrotate

# A real Proxmox PPPoE pilot on 2026-08-01 exposed that the candidate
# linux-virt kernel did not provide the PPPoE kernel module required by pppd.
# linux-lts supplied the module and completed the real WAN test. Validate the
# running kernel capability rather than silently completing an unusable install.
if ! modprobe pppoe >/dev/null 2>&1 && ! find /lib/modules -name "pppoe.ko*" 2>/dev/null | grep -q .; then
    echo "ERROR: running Alpine kernel does not provide the required pppoe module." >&2
    echo "On the validated Proxmox path, install/boot Alpine linux-lts, confirm 'modprobe pppoe', then rerun the installer." >&2
    exit 1
fi

# The router depends on accurate time for TOTP, TLS, schedules, audit ordering,
# and signed-update checks. Chrony is deliberately client-only and exposes no
# LAN/WAN NTP listener or remote command socket.
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

# 3. Create unprivileged routerd user/group
if ! id -u routerd >/dev/null 2>&1; then
    addgroup -S routerd
    adduser -S -D -H -h /var/lib/minimalrouter -s /sbin/nologin -G routerd routerd
fi
if ! id -u dnsmasq >/dev/null 2>&1; then
    echo "ERROR: the installed dnsmasq package did not create its service account" >&2
    exit 1
fi

# 4. Create required runtime directories
install -d -m 0700 -o routerd -g routerd /var/lib/minimalrouter
install -d -m 0750 -o dnsmasq -g dnsmasq /var/lib/minimalrouter-dhcp
if [ ! -e /var/lib/minimalrouter-dhcp/dnsmasq.leases ]; then
    install -m 0640 -o dnsmasq -g dnsmasq /dev/null /var/lib/minimalrouter-dhcp/dnsmasq.leases
else
    chown dnsmasq:dnsmasq /var/lib/minimalrouter-dhcp/dnsmasq.leases
    chmod 0640 /var/lib/minimalrouter-dhcp/dnsmasq.leases
fi
install -d -m 0700 -o root -g root /var/lib/minimalrouter-applyd
install -d -m 0750 -o root -g routerd /run/minimalrouter
install -d -m 0750 -o root -g inadyn /etc/inadyn
install -d -m 0755 -o root -g root /usr/share/minimalrouter/web /etc/minimalrouter
install -d -m 0700 -o root -g root /etc/ppp/peers /etc/hostapd
install -d -m 0755 -o root -g root /etc/dnsmasq.d /etc/modules-load.d /etc/logrotate.d

# Binaries are architecture-specific so a stale artifact from another target
# can never be installed accidentally.
case "$(uname -m)" in
    x86_64) BIN_ARCH="amd64"; BUILD_TARGET="build-linux" ;;
    aarch64) BIN_ARCH="arm64"; BUILD_TARGET="build-linux-arm64" ;;
    *) echo "ERROR: Unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac
[ -f "bin/routerd-linux-${BIN_ARCH}" ] || {
    echo "ERROR: Run make ${BUILD_TARGET} first." >&2
    exit 1
}
install -m 0755 -o root -g root "bin/routerd-linux-${BIN_ARCH}" /usr/bin/routerd
install -m 0755 -o root -g root "bin/router-applyd-linux-${BIN_ARCH}" /usr/sbin/router-applyd
if [ -f web/dist/index.html ]; then
    cp -R web/dist/. /usr/share/minimalrouter/web/
    chown -R root:root /usr/share/minimalrouter/web
else
    echo "ERROR: static appliance dashboard is missing (web/dist/index.html)." >&2
    exit 1
fi

# 5. Copy init scripts
cp packaging/alpine/router-applyd.initd /etc/init.d/router-applyd
cp packaging/alpine/routerd.initd /etc/init.d/routerd
cp packaging/alpine/pppoe-wan.initd /etc/init.d/pppoe-wan
cp packaging/alpine/99-minimalrouter.conf /etc/sysctl.d/99-minimalrouter.conf
cp packaging/alpine/minimalrouter.modules /etc/modules-load.d/minimalrouter.conf
cp packaging/alpine/minimalrouter.logrotate /etc/logrotate.d/minimalrouter
chmod 0755 /etc/init.d/router-applyd /etc/init.d/routerd /etc/init.d/pppoe-wan
chmod 0644 /etc/sysctl.d/99-minimalrouter.conf /etc/modules-load.d/minimalrouter.conf /etc/logrotate.d/minimalrouter

# Alpine's OpenRC modules service reads /etc/modules. Keep this fallback for
# installed systems that do not process modules-load.d directly.
while IFS= read -r kernel_module; do
    case "$kernel_module" in
        ""|\#*) continue ;;
    esac
    grep -qxF "$kernel_module" /etc/modules 2>/dev/null || printf '%s\n' "$kernel_module" >> /etc/modules
done < packaging/alpine/minimalrouter.modules

# MinimalRouter owns every WAN/LAN/tunnel interface: router-applyd assigns the
# LAN address, pppd owns the WAN, and wg(8) owns the tunnels. A distribution
# /etc/network/interfaces that still carries "iface eth0 inet dhcp" competes
# with all three -- it can launch BusyBox udhcpc, hand the physical PPPoE WAN an
# unexpected RFC1918 lease/default route, or overwrite resolver state.
if [ -f /etc/network/interfaces ] && [ ! -f /etc/network/interfaces.minimalrouter-backup ]; then
    cp -p /etc/network/interfaces /etc/network/interfaces.minimalrouter-backup
    echo "Saved previous network configuration to /etc/network/interfaces.minimalrouter-backup"
fi
install -d -m 0755 -o root -g root /etc/network
{
    echo "# Managed by MinimalRouter. Interfaces are owned by router-applyd,"
    echo "# pppd and wg(8); do not add DHCP/static addresses here."
    echo "auto lo"
    echo "iface lo inet loopback"
    echo ""
    for managed_interface_path in /sys/class/net/*; do
        [ -e "$managed_interface_path" ] || continue
        managed_interface=${managed_interface_path##*/}
        case "$managed_interface" in
            lo|ppp*|wg*|ifb*|veth*|docker*|br-*) continue ;;
        esac
        echo "iface $managed_interface inet manual"
    done
} > /etc/network/interfaces
chmod 0644 /etc/network/interfaces

# Defense in depth for machines converted from a normal Alpine/cloud install.
# dhcpcd is intentionally not in the Golden image, but if it is present now or
# appears later it is forbidden from owning every MinimalRouter interface.
cat > /etc/dhcpcd.conf <<'DHCPCD_CONFIG'
# Managed by MinimalRouter. WAN=pppd, LAN=router-applyd, tunnels=wg(8).
denyinterfaces *
DHCPCD_CONFIG
chmod 0644 /etc/dhcpcd.conf

# BusyBox udhcpc remains present as an Alpine base applet. It is never launched
# automatically because /etc/network/interfaces contains no DHCP stanza. If an
# operator invokes it manually, at least do not allow it to replace the router's
# resolver configuration.
install -d -m 0755 -o root -g root /etc/udhcpc
cat > /etc/udhcpc/udhcpc.conf <<'UDHCPC_CONFIG'
# Managed by MinimalRouter. Automatic udhcpc use is unsupported.
RESOLV_CONF="no"
UDHCPC_CONFIG
chmod 0644 /etc/udhcpc/udhcpc.conf

# cloud-init's documented fallback is to generate DHCP on a first interface when
# no explicit network data is available. Disable both network rendering and
# activation, in addition to removing all Alpine cloud-init phases from OpenRC.
install -d -m 0755 -o root -g root /etc/cloud/cloud.cfg.d
cat > /etc/cloud/cloud.cfg.d/99-minimalrouter-network.cfg <<'CLOUD_INIT_NETWORK'
network:
  config: disabled
disable_network_activation: true
CLOUD_INIT_NETWORK
chmod 0644 /etc/cloud/cloud.cfg.d/99-minimalrouter-network.cfg
for cloud_service in cloud-init cloud-init-local cloud-config cloud-final; do
    rc-service "$cloud_service" stop >/dev/null 2>&1 || true
    rc-update del "$cloud_service" boot >/dev/null 2>&1 || true
    rc-update del "$cloud_service" default >/dev/null 2>&1 || true
done

# 6. Enable services in OpenRC default runlevel.
# MinimalRouter owns every router interface. No competing client/manager may be
# auto-started on either boot or default runlevels.
for unused_service in dhcpcd networkmanager NetworkManager connman iwd wpa_supplicant \
    dropbear telnetd httpd miniupnpd upnpd rpcbind; do
    rc-service "$unused_service" stop >/dev/null 2>&1 || true
    rc-update del "$unused_service" boot >/dev/null 2>&1 || true
    rc-update del "$unused_service" default >/dev/null 2>&1 || true
done
rc-update add chronyd default
rc-update add sshd default
rc-update add router-applyd default
rc-update add routerd default

echo "=== Installation complete on Alpine $ALPINE_VERSION ==="
echo "Reboot now to complete the first-run setup: the installed system finalizes Minimal Router OS on boot."
