#!/bin/sh
# SIM-LAB provisioning: simulated internet + wg0 peer + wg1 office peer +
# ExtraLAN service. Runs as root inside SIM-LAB (Debian 13). Idempotent.
# MR peer public keys are added later by lab-mr-configure.sh.

set -eu
export DEBIAN_FRONTEND=noninteractive

echo "== packages =="
apt-get update -qq
apt-get install -y -qq wireguard-tools nginx chrony python3 qemu-guest-agent openssl curl >/dev/null
systemctl enable --now qemu-guest-agent >/dev/null 2>&1 || true

echo "== wireguard keys =="
mkdir -p /root/lab-wg-keys
[ -f /root/lab-wg-keys/sim_wg0.key ] || { wg genkey > /root/lab-wg-keys/sim_wg0.key; wg pubkey < /root/lab-wg-keys/sim_wg0.key > /root/lab-wg-keys/sim_wg0.pub; }
[ -f /root/lab-wg-keys/sim_wg1.key ] || { wg genkey > /root/lab-wg-keys/sim_wg1.key; wg pubkey < /root/lab-wg-keys/sim_wg1.key > /root/lab-wg-keys/sim_wg1.pub; }
chmod 600 /root/lab-wg-keys/*.key

echo "== wg0 peer (external endpoint 10.250.0.10:51820, tunnel 10.6.0.10/32) =="
cat > /etc/wireguard/wg0.conf <<EOF
[Interface]
Address = 10.6.0.10/32
ListenPort = 51820
PrivateKey = $(cat /root/lab-wg-keys/sim_wg0.key)
EOF
systemctl enable wg-quick@wg0 >/dev/null 2>&1
systemctl restart wg-quick@wg0 || true

echo "== wg1 office peer (10.79.0.2:51821, office LAN 10.79.1.0/24 behind) =="
cat > /etc/wireguard/wg1.conf <<EOF
[Interface]
Address = 10.79.0.2/24
ListenPort = 51821
PrivateKey = $(cat /root/lab-wg-keys/sim_wg1.key)
PostUp = ip addr add 10.79.1.1/24 dev wg1 2>/dev/null || true
PostDown = ip addr del 10.79.1.1/24 dev wg1 2>/dev/null || true
EOF
systemctl enable wg-quick@wg1 >/dev/null 2>&1
systemctl restart wg-quick@wg1 || true

echo "== HTTP/HTTPS markers on the internet side (10.250.0.10) =="
mkdir -p /var/www/lab
cat > /var/www/lab/index.html <<'EOF'
torture-lab-ok
EOF
cat > /var/www/lab/marker.txt <<'EOF'
torture-lab-marker-ok
EOF
cat > /etc/nginx/sites-available/lab <<'EOF'
server {
    listen 10.250.0.10:80 default_server;
    root /var/www/lab;
    server_name _;
    location / { try_files $uri /index.html; }
    location /marker.txt { }
}
server {
    listen 10.250.0.10:443 ssl;
    root /var/www/lab;
    server_name _;
    ssl_certificate /etc/ssl/lab-selfsigned.crt;
    ssl_certificate_key /etc/ssl/lab-selfsigned.key;
    location / { try_files $uri /index.html; }
}
server {
    listen 10.79.1.1:80;
    root /var/www/lab;
    server_name _;
    location / { try_files $uri /index.html; }
}
EOF
rm -f /etc/nginx/sites-enabled/default
ln -sf /etc/nginx/sites-available/lab /etc/nginx/sites-enabled/lab
if [ ! -f /etc/ssl/lab-selfsigned.crt ]; then
  openssl req -x509 -nodes -newkey rsa:2048 -days 730 \
    -keyout /etc/ssl/lab-selfsigned.key -out /etc/ssl/lab-selfsigned.crt \
    -subj "/CN=sim.lab.test" >/dev/null 2>&1
fi
systemctl enable nginx >/dev/null 2>&1
systemctl restart nginx

echo "== ExtraLAN service (10.78.0.10:8080) =="
mkdir -p /var/www/extralan
cat > /var/www/extralan/index.html <<'EOF'
extralan-service-ok
EOF
cat > /etc/systemd/system/extralan-http.service <<'EOF'
[Unit]
Description=ExtraLAN lab service
After=network-online.target
[Service]
ExecStart=/usr/bin/python3 -m http.server 8080 --bind 10.78.0.10 --directory /var/www/extralan
Restart=always
RestartSec=2
[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload
systemctl enable extralan-http >/dev/null 2>&1
systemctl restart extralan-http

echo "== NTP (10.250.0.10:123) =="
sed -i 's/^#allow 192.168.0.0\/16/allow 10.250.0.0\/24/' /etc/chrony/chrony.conf
grep -q '^allow 10.250.0.0/24' /etc/chrony/chrony.conf || echo 'allow 10.250.0.0/24' >> /etc/chrony/chrony.conf
systemctl enable chrony >/dev/null 2>&1
systemctl restart chrony

echo "== SIM-LAB ready =="
wg show wg0 2>/dev/null | head -3
wg show wg1 2>/dev/null | head -3
ip -brief addr show | grep -E "10\.(6|79|78)\."
