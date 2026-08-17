from pathlib import Path

p = Path('packaging/alpine/build-iso.sh')
s = p.read_text()
old = 'REQUIRED_PACKAGES="alpine-base alpine-conf linux-lts linux-firmware-none e2fsprogs grub grub-efi syslinux dosfstools util-linux nftables ppp ppp-pppoe dnsmasq iproute2 iputils-ping iputils-arping ca-certificates openssh-server wireguard-tools-wg doas squid hostapd hostapd-openrc iw inadyn inadyn-openrc chrony chrony-openrc logrotate"'
new = 'REQUIRED_PACKAGES="alpine-base alpine-conf linux-lts linux-firmware-none e2fsprogs grub grub-efi syslinux dosfstools util-linux nftables ppp ppp-pppoe dnsmasq iproute2 iputils-ping iputils-arping ca-certificates openssl openssh-server wireguard-tools-wg doas squid hostapd hostapd-openrc iw inadyn inadyn-openrc chrony chrony-openrc logrotate"'
if old not in s:
    raise SystemExit('REQUIRED_PACKAGES marker not found')
s = s.replace(old, new, 1)
old = '''                  nftables ppp ppp-pppoe dnsmasq iproute2 iputils-ping iputils-arping ca-certificates openssh-server \\
                  wireguard-tools-wg doas squid hostapd hostapd-openrc iw inadyn inadyn-openrc \\
'''
new = '''                  nftables ppp ppp-pppoe dnsmasq iproute2 iputils-ping iputils-arping ca-certificates openssl openssh-server \\
                  wireguard-tools-wg doas squid hostapd hostapd-openrc iw inadyn inadyn-openrc \\
'''
if old not in s:
    raise SystemExit('apk fetch package list marker not found')
s = s.replace(old, new, 1)
old = 'apk fetch --no-network --recursive --output /tmp/mr-fetch                   alpine-base e2fsprogs linux-lts openssl syslinux >/dev/null'
new = 'apk fetch --no-network --recursive --output /tmp/mr-fetch                   alpine-base e2fsprogs linux-firmware-none linux-lts openssl syslinux >/dev/null'
if old not in s:
    raise SystemExit('offline validation fetch marker not found')
s = s.replace(old, new, 1)
old = 'for pkg in alpine-base e2fsprogs linux-lts openssl syslinux; do'
new = 'for pkg in alpine-base e2fsprogs linux-firmware-none linux-lts openssl syslinux; do'
if old not in s:
    raise SystemExit('offline validation package loop marker not found')
s = s.replace(old, new, 1)
p.write_text(s)
