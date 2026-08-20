#!/usr/bin/env bash
# Run the existing MinimalRouter torture suite on a GitHub-hosted runner.
#
# Topology (all QEMU uses TCG, never nested KVM/Proxmox):
#   Debian ISP/PPPoE  <--- br-lab-wan --->  MinimalRouter  <--- br-lab-lan ---> Debian client
#          |                                     |
#          +----------- Debian SIM -------------+--- br-lab-extra
#
# The existing 153 scenario scripts are copied unchanged into a shadow worktree.
# github-backend.sh is appended only to the shadow lib.sh, replacing Proxmox
# transport with SSH/QEMU controls while preserving every scenario assertion.
set -Eeuo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT"
STATE="${LAB_GITHUB_STATE:-$ROOT/build/github-torture}"
STATE="$(mkdir -p "$STATE" && cd "$STATE" && pwd)"
export LAB_GITHUB_STATE="$STATE"
export LAB_GITHUB_KEY="${LAB_GITHUB_KEY:-$STATE/id_ed25519}"
export LAB_RECOVERY_PW="${LAB_RECOVERY_PW:-MRciRecovery-2026!}"
export LAB_ADMIN_PW="${LAB_ADMIN_PW:-MinimalRouterCI-2026!}"
export LAB_BACKEND=github
export LAB_TEMP_LIMIT=200
export LAB_SIGNED_ROOT="${LAB_SIGNED_ROOT:-/tmp/lab24sig}"

mkdir -p "$STATE" "$STATE/bin" "$STATE/logs" "$STATE/results"
printf '%s\n' "$(ip route show default | head -1)" > "$STATE/default-route"

need() { command -v "$1" >/dev/null 2>&1 || { echo "missing required command: $1" >&2; exit 1; }; }
for c in qemu-system-x86_64 qemu-img cloud-localds curl ssh scp sshpass ip jq python3 base64 sha256sum; do need "$c"; done

scenario_count="$(find scripts/lab/scenarios -maxdepth 1 -type f -name '[0-9]*.sh' | wc -l | tr -d ' ')"
echo "GitHub torture inventory: $scenario_count scenario files"
[[ "$scenario_count" == 153 ]] || { echo "expected exactly 153 scenarios, found $scenario_count" >&2; exit 1; }

if [[ ! -f "$LAB_GITHUB_KEY" ]]; then
  ssh-keygen -q -t ed25519 -N '' -f "$LAB_GITHUB_KEY"
fi
PUBKEY="$(cat "$LAB_GITHUB_KEY.pub")"

# Install the qm compatibility command used by a handful of existing scenarios.
install -m 0755 scripts/lab/github-qm.sh "$STATE/bin/qm"

qmpids() {
  for vm in 150 151 153 154; do
    pf="$STATE/vm-$vm.pid"
    [[ -s "$pf" ]] && cat "$pf"
  done
}

cleanup() {
  set +e
  for vm in 150 151 153 154; do
    LAB_GITHUB_STATE="$STATE" LAB_GITHUB_KEY="$LAB_GITHUB_KEY" LAB_RECOVERY_PW="$LAB_RECOVERY_PW" PATH="$STATE/bin:$PATH" qm stop "$vm" >/dev/null 2>&1 || true
  done
  for tap in tap-mr-wan tap-mr-lan tap-mr-extra tap-isp-wan tap-sim-wan tap-sim-extra tap-lan-lan; do
    ip link del "$tap" >/dev/null 2>&1 || true
  done
  for br in br-lab-wan br-lab-lan br-lab-extra; do
    ip link del "$br" >/dev/null 2>&1 || true
  done
}
trap cleanup EXIT INT TERM
cleanup

mkbridge() {
  local br="$1"
  ip link add "$br" type bridge
  ip link set "$br" up
}
mktap() {
  local tap="$1" br="$2"
  ip tuntap add dev "$tap" mode tap
  ip link set "$tap" master "$br"
  ip link set "$tap" up
}

modprobe tun >/dev/null 2>&1 || true
mkbridge br-lab-wan
mkbridge br-lab-lan
mkbridge br-lab-extra
ip addr add 192.168.1.254/24 dev br-lab-lan
mktap tap-mr-wan br-lab-wan
mktap tap-mr-lan br-lab-lan
mktap tap-mr-extra br-lab-extra
mktap tap-isp-wan br-lab-wan
mktap tap-sim-wan br-lab-wan
mktap tap-sim-extra br-lab-extra
mktap tap-lan-lan br-lab-lan

BASE="$STATE/debian-13-genericcloud-amd64.qcow2"
if [[ ! -s "$BASE" ]]; then
  curl -fL --retry 4 --retry-delay 3 \
    -o "$BASE.tmp" \
    https://cloud.debian.org/images/cloud/trixie/latest/debian-13-genericcloud-amd64.qcow2
  mv "$BASE.tmp" "$BASE"
fi

make_seed() {
  local vm="$1" netcfg="$2"
  local ud="$STATE/user-$vm.yaml" nc="$STATE/net-$vm.yaml" seed="$STATE/seed-$vm.img"
  cat > "$ud" <<EOF
#cloud-config
users:
  - name: lab
    groups: [sudo]
    sudo: ALL=(ALL) NOPASSWD:ALL
    shell: /bin/bash
    lock_passwd: true
    ssh_authorized_keys:
      - $PUBKEY
ssh_pwauth: false
disable_root: true
package_update: false
EOF
  printf '%s\n' "$netcfg" > "$nc"
  cloud-localds --network-config="$nc" "$seed" "$ud"
}

ISP_NET='version: 2
ethernets:
  mgmt:
    match: {macaddress: "52:54:00:15:00:00"}
    set-name: eth0
    dhcp4: true
  labwan:
    match: {macaddress: "52:54:00:15:00:01"}
    set-name: eth1
    addresses: [10.250.0.1/24]'
SIM_NET='version: 2
ethernets:
  mgmt:
    match: {macaddress: "52:54:00:53:00:00"}
    set-name: eth0
    dhcp4: true
  labwan:
    match: {macaddress: "52:54:00:53:00:01"}
    set-name: eth1
    addresses: [10.250.0.10/24]
  extra:
    match: {macaddress: "52:54:00:53:00:02"}
    set-name: eth2
    addresses: [10.78.0.10/24]'
LAN_NET='version: 2
ethernets:
  mgmt:
    match: {macaddress: "52:54:00:54:00:00"}
    set-name: eth0
    dhcp4: true
  lan:
    match: {macaddress: "52:54:00:54:00:01"}
    set-name: eth1
    dhcp4: false
    optional: true'

make_seed 150 "$ISP_NET"
make_seed 153 "$SIM_NET"
make_seed 154 "$LAN_NET"

for vm in 150 153 154; do
  qemu-img create -q -f qcow2 -F qcow2 -b "$BASE" "$STATE/disk-$vm.qcow2"
done

cat > "$STATE/start-150.sh" <<EOF
#!/bin/sh
exec qemu-system-x86_64 -machine pc,accel=tcg -cpu max -smp 1 -m 768 \\
  -drive file='$STATE/disk-150.qcow2',format=qcow2,if=virtio \\
  -drive file='$STATE/seed-150.img',format=raw,if=virtio,readonly=on \\
  -netdev user,id=mgmt,hostfwd=tcp:127.0.0.1:2250-:22 \\
  -device virtio-net-pci,netdev=mgmt,mac=52:54:00:15:00:00 \\
  -netdev tap,id=wan,ifname=tap-isp-wan,script=no,downscript=no \\
  -device virtio-net-pci,netdev=wan,mac=52:54:00:15:00:01 \\
  -display none -serial file:'$STATE/logs/isp-serial.log' -daemonize -pidfile '$STATE/vm-150.pid'
EOF
cat > "$STATE/start-153.sh" <<EOF
#!/bin/sh
exec qemu-system-x86_64 -machine pc,accel=tcg -cpu max -smp 1 -m 768 \\
  -drive file='$STATE/disk-153.qcow2',format=qcow2,if=virtio \\
  -drive file='$STATE/seed-153.img',format=raw,if=virtio,readonly=on \\
  -netdev user,id=mgmt,hostfwd=tcp:127.0.0.1:2253-:22 \\
  -device virtio-net-pci,netdev=mgmt,mac=52:54:00:53:00:00 \\
  -netdev tap,id=wan,ifname=tap-sim-wan,script=no,downscript=no \\
  -device virtio-net-pci,netdev=wan,mac=52:54:00:53:00:01 \\
  -netdev tap,id=extra,ifname=tap-sim-extra,script=no,downscript=no \\
  -device virtio-net-pci,netdev=extra,mac=52:54:00:53:00:02 \\
  -display none -serial file:'$STATE/logs/sim-serial.log' -daemonize -pidfile '$STATE/vm-153.pid'
EOF
cat > "$STATE/start-154.sh" <<EOF
#!/bin/sh
exec qemu-system-x86_64 -machine pc,accel=tcg -cpu max -smp 1 -m 640 \\
  -drive file='$STATE/disk-154.qcow2',format=qcow2,if=virtio \\
  -drive file='$STATE/seed-154.img',format=raw,if=virtio,readonly=on \\
  -netdev user,id=mgmt,hostfwd=tcp:127.0.0.1:2254-:22 \\
  -device virtio-net-pci,netdev=mgmt,mac=52:54:00:54:00:00 \\
  -netdev tap,id=lan,ifname=tap-lan-lan,script=no,downscript=no \\
  -device virtio-net-pci,netdev=lan,mac=52:54:00:54:00:01 \\
  -display none -serial file:'$STATE/logs/lan-serial.log' -daemonize -pidfile '$STATE/vm-154.pid'
EOF

MR_DISK="${LAB_MR_DISK:-$ROOT/build/iso/full-install-disk.raw}"
[[ -s "$MR_DISK" ]] || { echo "missing installed MinimalRouter disk: $MR_DISK" >&2; exit 1; }
cat > "$STATE/start-151.sh" <<EOF
#!/bin/sh
exec qemu-system-x86_64 -machine pc,accel=tcg -cpu max -smp 2 -m 1536 \\
  -drive file='$MR_DISK',format=raw,if=virtio \\
  -netdev tap,id=wan,ifname=tap-mr-wan,script=no,downscript=no \\
  -device virtio-net-pci,netdev=wan,mac=52:54:00:51:00:00 \\
  -netdev tap,id=lan,ifname=tap-mr-lan,script=no,downscript=no \\
  -device virtio-net-pci,netdev=lan,mac=52:54:00:51:00:01 \\
  -netdev tap,id=extra,ifname=tap-mr-extra,script=no,downscript=no \\
  -device virtio-net-pci,netdev=extra,mac=52:54:00:51:00:02 \\
  -display none -serial file:'$STATE/logs/mr-serial.log' -daemonize -pidfile '$STATE/vm-151.pid'
EOF
chmod 0755 "$STATE"/start-*.sh

export PATH="$STATE/bin:$PATH"
for vm in 150 153 154 151; do
  qm start "$vm"
done

wait_aux() {
  local vm="$1" port="$2" i=0
  while (( i < 240 )); do
    if ssh -i "$LAB_GITHUB_KEY" -o BatchMode=yes -o StrictHostKeyChecking=no \
      -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR -o ConnectTimeout=2 \
      -p "$port" lab@127.0.0.1 true >/dev/null 2>&1; then return 0; fi
    sleep 2; i=$((i+2))
  done
  echo "aux VM $vm did not become SSH-ready" >&2
  tail -100 "$STATE/logs/$(case "$vm" in 150) echo isp;;153) echo sim;;154) echo lan;;esac)-serial.log" >&2 || true
  return 1
}
wait_aux 150 2250
wait_aux 153 2253
wait_aux 154 2254
qm wait 151

aux_exec() {
  local port="$1" cmd="$2" b64
  b64="$(printf '%s' "$cmd" | base64 -w0)"
  ssh -i "$LAB_GITHUB_KEY" -o BatchMode=yes -o StrictHostKeyChecking=no \
    -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR -o ConnectTimeout=8 \
    -p "$port" lab@127.0.0.1 "echo '$b64' | base64 -d | sudo -n sh"
}
aux_copy_run() {
  local port="$1" file="$2"
  scp -q -i "$LAB_GITHUB_KEY" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
    -P "$port" "$file" lab@127.0.0.1:/tmp/lab-provision.sh
  aux_exec "$port" 'chmod 755 /tmp/lab-provision.sh && /tmp/lab-provision.sh'
}
mr_exec() {
  local cmd="$1" b64
  b64="$(printf '%s' "$cmd" | base64 -w0)"
  sshpass -p "$LAB_RECOVERY_PW" ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
    -o LogLevel=ERROR -o ConnectTimeout=8 root@192.168.1.1 "echo '$b64' | base64 -d | sh"
}

# Provision the three Debian peers using the same payloads as the Proxmox lab.
aux_copy_run 2250 scripts/lab/payloads/isp-provision.sh
aux_copy_run 2253 scripts/lab/payloads/sim-provision.sh
aux_copy_run 2254 scripts/lab/payloads/client-provision.sh

# Add the public-simulator aliases that the historical torture suite uses.
aux_exec 2253 'ip addr add 11.250.0.10/32 dev eth1 2>/dev/null || true; ip addr add 11.255.0.2/32 dev eth1 2>/dev/null || true; sed -i "/listen 10.250.0.10:80/a\\    listen 11.255.0.2:80;" /etc/nginx/sites-available/lab; systemctl restart nginx'
aux_exec 2250 'ip route replace 11.250.0.10/32 dev eth1; ip route replace 11.255.0.2/32 dev eth1'

# Lab-only bootstrap on the installed ISO: out-of-band fault hooks and fresh WG
# keys. Production files/binaries are not replaced.
mr_exec 'set -e
mkdir -p /etc/conf.d /run/minimalrouter-fault /root/lab-wg-keys
chmod 0755 /run/minimalrouter-fault
printf "%s\n" "MINIMALROUTER_FAULT_HOOK_DIR=/run/minimalrouter-fault" > /etc/conf.d/routerd
printf "%s\n" "MINIMALROUTER_FAULT_HOOK_DIR=/run/minimalrouter-fault" > /etc/conf.d/router-applyd
grep -q "export MINIMALROUTER_FAULT_HOOK_DIR" /etc/init.d/routerd || sed -i "/^export MINIMALROUTER_WEB_DIR=/a export MINIMALROUTER_FAULT_HOOK_DIR=\"${MINIMALROUTER_FAULT_HOOK_DIR:-}\"" /etc/init.d/routerd
grep -q "permit nopass routerd as root cmd /sbin/poweroff" /etc/doas.conf || echo "permit nopass routerd as root cmd /sbin/poweroff" >> /etc/doas.conf
[ -f /root/lab-wg-keys/mr_wg0.key ] || { wg genkey > /root/lab-wg-keys/mr_wg0.key; wg pubkey < /root/lab-wg-keys/mr_wg0.key > /root/lab-wg-keys/mr_wg0.pub; }
[ -f /root/lab-wg-keys/mr_wg1.key ] || { wg genkey > /root/lab-wg-keys/mr_wg1.key; wg pubkey < /root/lab-wg-keys/mr_wg1.key > /root/lab-wg-keys/mr_wg1.pub; }
chmod 600 /root/lab-wg-keys/*.key
rc-service routerd restart
rc-service router-applyd restart
'

MR_WG0_KEY="$(mr_exec 'cat /root/lab-wg-keys/mr_wg0.key')"
MR_WG1_KEY="$(mr_exec 'cat /root/lab-wg-keys/mr_wg1.key')"
MR_WG0_PUB="$(mr_exec 'cat /root/lab-wg-keys/mr_wg0.pub')"
MR_WG1_PUB="$(mr_exec 'cat /root/lab-wg-keys/mr_wg1.pub')"
SIM_WG0_PUB="$(aux_exec 2253 'cat /root/lab-wg-keys/sim_wg0.pub')"
SIM_WG1_PUB="$(aux_exec 2253 'cat /root/lab-wg-keys/sim_wg1.pub')"

aux_exec 2253 "cat > /etc/wireguard/wg0.conf <<'EOF'
[Interface]
Address = 10.6.0.10/32
ListenPort = 51820
PrivateKey = \$(cat /root/lab-wg-keys/sim_wg0.key)

[Peer]
PublicKey = $MR_WG0_PUB
AllowedIPs = 10.6.0.1/32
EOF
systemctl restart wg-quick@wg0"
aux_exec 2253 "cat > /etc/wireguard/wg1.conf <<'EOF'
[Interface]
Address = 10.79.0.2/24
ListenPort = 51821
PrivateKey = \$(cat /root/lab-wg-keys/sim_wg1.key)
PostUp = ip addr add 10.79.1.1/24 dev wg1 2>/dev/null || true
PostDown = ip addr del 10.79.1.1/24 dev wg1 2>/dev/null || true

[Peer]
PublicKey = $MR_WG1_PUB
AllowedIPs = 10.79.0.1/32
EOF
systemctl restart wg-quick@wg1"

MR_API=https://192.168.1.1:8443
COOKIE="$STATE/api.cookie"
for _ in $(seq 1 60); do
  if curl -sk --max-time 4 "$MR_API/api/v1/setup/status" | grep -q is_configured; then break; fi
  sleep 2
done
LOGIN="$(curl -sk --max-time 10 -c "$COOKIE" -X POST "$MR_API/api/v1/auth/login" -H 'Content-Type: application/json' -d "{\"password\":\"$LAB_ADMIN_PW\"}")"
CSRF="$(printf '%s' "$LOGIN" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("csrf_token",""))')"
[[ -n "$CSRF" ]] || { echo "could not authenticate to installed MinimalRouter" >&2; echo "$LOGIN" >&2; exit 1; }
CFG="$(curl -sk --max-time 15 -b "$COOKIE" "$MR_API/api/v1/config)"

BODY="$(printf '%s' "$CFG" | python3 -c "import json,sys
c=json.load(sys.stdin)
c['system']['hostname']='mr-test'; c['system']['domain']='lab.test'; c['system']['management_access']='lan_and_wireguard'
c['wan']={'interface':'eth0','enabled':True,'username':'mr-test','password':'minimalrouter-lab-pppoe','mtu':1492}
c['lan']['interface']='eth1'; c['lan']['ip_address']='192.168.1.1'; c['lan']['cidr']='192.168.1.1/24'; c['lan']['netmask']='255.255.255.0'
c['dhcp']={'enabled':True,'dns_enabled':False,'range_start':'192.168.1.100','range_end':'192.168.1.200','lease_time':'12h','dns_servers':['1.1.1.1','1.0.0.1'],'static_leases':[]}
c['dns']={'records':[{'name':'router.home.arpa','ip':'192.168.1.1'},{'name':'client.home.arpa','ip':'192.168.1.100'}]}
c['firewall']['extra_lans']=[{'id':'elab1','name':'lab-extra','interface':'eth2','cidr':'10.78.0.0/24','router_address':'10.78.0.1/24','dst_ip':'10.78.0.10','dst_port':8080,'allow_from':['192.168.1.0/24'],'enabled':True}]
c['wireguard']={'enabled':True,'interface':'wg0','private_key':'$MR_WG0_KEY','listen_port':51820,'address':'10.6.0.1/24','peers':[{'id':'sim-peer','name':'sim-lab','public_key':'$SIM_WG0_PUB','allowed_ips':['10.6.0.10/32'],'endpoint':'11.250.0.10:51820','enabled':True}]}
c['wg_client']={'enabled':True,'interface':'wg1','private_key':'$MR_WG1_KEY','address':'10.79.0.1/32','public_key':'$SIM_WG1_PUB','endpoint':'11.250.0.10:51821','allowed_ips':['10.79.1.0/24','10.79.0.2/32'],'persistent_keepalive':25}
c['trusted_networks']=['192.168.1.0/24','10.6.0.0/24']
print(json.dumps(c))")"

curl -sk --max-time 120 -b "$COOKIE" -X PUT "$MR_API/api/v1/config" \
  -H 'Content-Type: application/json' -H "X-CSRF-Token: $CSRF" -d "$BODY" > "$STATE/config-put.json"

for _ in $(seq 1 15); do
  PEND="$(curl -sk --max-time 10 -b "$COOKIE" "$MR_API/api/v1/transactions/pending" || true)"
  if printf '%s' "$PEND" | grep -q '"pending"[[:space:]]*:[[:space:]]*true'; then
    TXID="$(printf '%s' "$PEND" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')"
    curl -sk --max-time 120 -b "$COOKIE" -X POST "$MR_API/api/v1/transactions/$TXID/confirm" -H "X-CSRF-Token: $CSRF" > "$STATE/config-confirm.json" || true
    break
  fi
  sleep 2
done

# The apply can transiently replace routes/rules; restore the lab host route and
# wait for the real PPPoE session.
for _ in $(seq 1 90); do
  mr_exec 'ip route replace 192.168.1.254/32 dev eth1 2>/dev/null || true' >/dev/null 2>&1 || true
  if mr_exec 'ip -4 -o addr show ppp0 2>/dev/null' | grep -q '10.250.0.50'; then break; fi
  sleep 2
done
mr_exec 'ip -4 -o addr show ppp0' | grep -q '10.250.0.50' || { echo "PPPoE did not come up" >&2; exit 1; }
aux_exec 2253 'ip route replace 10.250.0.50/32 via 10.250.0.1 dev eth1'

# Give the LAN client a real DHCP lease from MinimalRouter. Keep a dhclient
# compatibility command even on Debian images where ISC dhclient is absent.
aux_exec 2254 'export DEBIAN_FRONTEND=noninteractive; apt-get update -qq; apt-get install -y -qq busybox >/dev/null; cat > /usr/local/sbin/lab-udhcpc <<"EOF"
#!/bin/sh
case "$1" in
  bound|renew)
    ip addr flush dev "$interface"
    ip addr add "$ip/24" dev "$interface"
    if [ -n "${router:-}" ]; then set -- $router; ip route replace default via "$1" dev "$interface"; fi
    ;;
esac
EOF
chmod 755 /usr/local/sbin/lab-udhcpc
if ! command -v dhclient >/dev/null 2>&1; then cat > /usr/local/sbin/dhclient <<"EOF"
#!/bin/sh
iface="${@: -1}"
exec busybox udhcpc -i "$iface" -s /usr/local/sbin/lab-udhcpc -n -q -t 8 -T 2
EOF
chmod 755 /usr/local/sbin/dhclient; fi
ip addr flush dev eth1; busybox udhcpc -i eth1 -s /usr/local/sbin/lab-udhcpc -n -q -t 8 -T 2'

# Prepare the exact signed payload shape expected by scenario 24.
rm -rf "$LAB_SIGNED_ROOT"
mkdir -p "$LAB_SIGNED_ROOT"
go run ./cmd/firmware-keygen --private-key "$LAB_SIGNED_ROOT/release.key" --public-key "$LAB_SIGNED_ROOT/release.pub"
for ver in 9.9.8 9.9.9; do
  rm -rf "$LAB_SIGNED_ROOT/release-$ver"
  cp -a build/dist/minimalrouter-linux-amd64 "$LAB_SIGNED_ROOT/release-$ver"
  go run ./cmd/firmware-sign \
    --dir "$LAB_SIGNED_ROOT/release-$ver" \
    --key "$LAB_SIGNED_ROOT/release.key" \
    --version "$ver" --commit github-torture \
    --public-key-output "$LAB_SIGNED_ROOT/release-$ver/firmware-signing.pub" \
    --output "$LAB_SIGNED_ROOT/lab-update-$ver.manifest.json"
done
rm -f "$LAB_SIGNED_ROOT/release.key"
PUB_B64="$(base64 -w0 "$LAB_SIGNED_ROOT/release.pub")"
mr_exec "mkdir -p /etc/minimalrouter; echo '$PUB_B64' | base64 -d > /etc/minimalrouter/firmware-signing.pub; chmod 0644 /etc/minimalrouter/firmware-signing.pub"

# Smoke the topology before handing it to the unchanged torture suite.
mr_exec 'nft list ruleset' | grep -q 'policy drop'
mr_exec 'ip -4 -o addr show ppp0' | grep -q '10.250.0.50'
aux_exec 2254 'ip -4 -o addr show eth1' | grep -q '192.168.1.'
aux_exec 2254 'host router.home.arpa 192.168.1.1' | grep -q '192.168.1.1'
aux_exec 2254 'curl -s --max-time 8 http://11.255.0.2/marker.txt' | grep -q torture-lab

# Shadow copy: scenarios remain byte-for-byte identical; only lib.sh gains the
# GitHub transport overrides. Copy all scripts so relative paths used by the
# signed-update scenario still resolve, and link the real build artifacts.
WORK="$STATE/worktree"
rm -rf "$WORK"
mkdir -p "$WORK"
cp -a scripts "$WORK/scripts"
ln -s "$ROOT/build" "$WORK/build"
cat scripts/lab/github-backend.sh >> "$WORK/scripts/lab/lib.sh"

# If an unexpected scenario failure leaves MR powered off, the next scenario
# must still get an independent baseline instead of cascading 152 false reds.
python3 - "$WORK/scripts/lab/lab-run.sh" <<'PY'
from pathlib import Path
import sys
p=Path(sys.argv[1])
s=p.read_text()
needle='reset_lab() {\n'
insert='reset_lab() {\n  if [ "${LAB_BACKEND:-}" = github ]; then H "qm start $MR_VMID" >/dev/null 2>&1 || true; mr_wait 180 >/dev/null 2>&1 || true; fi\n'
if needle not in s:
    raise SystemExit('reset_lab hook point not found')
p.write_text(s.replace(needle, insert, 1))
PY

set +e
(
  cd "$WORK"
  LAB_GITHUB_STATE="$STATE" LAB_GITHUB_KEY="$LAB_GITHUB_KEY" \
  LAB_RECOVERY_PW="$LAB_RECOVERY_PW" LAB_ADMIN_PW="$LAB_ADMIN_PW" \
  LAB_BACKEND=github LAB_RESULTS="$STATE/results" LAB_SIGNED_ROOT="$LAB_SIGNED_ROOT" \
  PATH="$STATE/bin:$PATH" \
  sh scripts/lab/lab-run.sh all
) 2>&1 | tee "$STATE/torture.log"
rc=${PIPESTATUS[0]}
set -e

pass="$(grep -Ec 'scenario [^ ]+ PASS$' "$STATE/torture.log" || true)"
fail="$(grep -Ec 'scenario [^ ]+ FAILED' "$STATE/torture.log" || true)"
result_files="$(find "$STATE/results" -name result.txt -type f | wc -l | tr -d ' ')"
jq -n \
  --argjson inventory "$scenario_count" --argjson pass "$pass" --argjson fail "$fail" \
  --argjson result_files "$result_files" --argjson runner_rc "$rc" \
  '{inventory:$inventory,scenario_passes:$pass,scenario_failures:$fail,result_files:$result_files,runner_rc:$runner_rc}' \
  > "$STATE/summary.json"
cat "$STATE/summary.json"

[[ "$rc" -eq 0 && "$pass" -eq "$scenario_count" && "$fail" -eq 0 ]] || exit 1
