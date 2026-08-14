#!/bin/sh
# MinimalRouter torture lab — Proxmox host-side creation script.
# Idempotent: safe to re-run. Creates the four isolated lab bridges (if
# missing) and the four lab VMs. NEVER touches vmbr0/vmbr1/nic0/nic1 or any
# existing VM other than the lab IDs below.
#
# VM ID plan (documented in docs/TEST-LAB.md):
#   150 ISP-LAB      Debian 13, upstream on vmbr0 + ISP side on vmbr-lab-wan
#   151 MR-TEST      MinimalRouter Alpine (clone base from VM111, pristine wipe)
#   152 LAN-CLIENT   Debian 13, DHCP client on vmbr-lab-lan only
#   153 SIM-LAB      Debian 13, internet/wg/extra-lan simulators
#
# Usage: sh lab-create.sh [--host HOST]   (default: the documented Proxmox host)

set -eu

HOST="${LAB_HOST:-root@proxmox.example}"
SSHOPTS="-o BatchMode=yes -o ConnectTimeout=10"
KEY="${LAB_SSH_KEY:-${HOME:-/root}/.ssh/lab_id_ed25519}"
KNOWN_HOSTS="${LAB_KNOWN_HOSTS:-${HOME:-/root}/.ssh/known_hosts}"
H() { ssh $SSHOPTS -i "$KEY" -o UserKnownHostsFile="$KNOWN_HOSTS" "$HOST" "$@"; }
PKEY="$(cat "$(dirname "$0")/../../private/secrets/lab_id_ed25519.pub")"

echo "== [0/5] boot safety (production first, lab stays down) =="
# If the host loses power and comes back, pfSense must come up and the lab
# must stay down: production internet is never hostage to a lab VM boot.
H 'set -e
qm set 106 --onboot 1 >/dev/null 2>&1 || true
for vm in 108 150 151 152 153 154; do qm set $vm --onboot 0 >/dev/null 2>&1 || true; done
echo "  pfSense 106 onboot=1; lab VMs 108,150-154 onboot=0"'

echo "== [1/5] lab bridges =="
H 'set -e
if [ ! -f /etc/network/interfaces.d/lab ]; then
  cat > /etc/network/interfaces.d/lab <<LABEOF
auto vmbr-lab-wan
iface vmbr-lab-wan inet manual
	bridge-ports none
	bridge-stp off
	bridge-fd 0

auto vmbr-lab-lan
iface vmbr-lab-lan inet static
	address 10.77.0.2/24
	bridge-ports none
	bridge-stp off
	bridge-fd 0

auto vmbr-lab-extra
iface vmbr-lab-extra inet manual
	bridge-ports none
	bridge-stp off
	bridge-fd 0

auto vmbr-lab-office
iface vmbr-lab-office inet manual
	bridge-ports none
	bridge-stp off
	bridge-fd 0
LABEOF
fi
for b in vmbr-lab-wan vmbr-lab-lan vmbr-lab-extra vmbr-lab-office; do
  ip link show "$b" >/dev/null 2>&1 || ifup "$b"
done
echo "bridges ready"'

echo "== [2/5] Debian cloud image =="
H '[ -f /var/lib/vz/template/iso/debian-13-genericcloud-amd64.qcow2 ] || \
  curl -sL -o /var/lib/vz/template/iso/debian-13-genericcloud-amd64.qcow2 \
    https://cloud.debian.org/images/cloud/trixie/latest/debian-13-genericcloud-amd64.qcow2
ls -la /var/lib/vz/template/iso/debian-13-genericcloud-amd64.qcow2'

makedeb() {
  # makedeb <vmid> <name> <memMB> <diskGB> <net specs...> "<ipconfig...>"
  vmid="$1"; name="$2"; mem="$3"; disk="$4"; nets="$5"; ipseg="$6"
  if H "qm config $vmid >/dev/null 2>&1"; then
    echo "  VM $vmid exists, skipping"
    return
  fi
  H "qm create $vmid --name $name --ostype l26 --memory $mem --cores 1 --cpu host \
     --scsihw virtio-scsi-single --agent 1 --serial0 socket --vga serial0 \
     --net0 $nets
     [ -f /var/lib/vz/template/iso/debian-13-genericcloud-amd64.qcow2 ]"
  H "qm importdisk $vmid /var/lib/vz/template/iso/debian-13-genericcloud-amd64.qcow2 local-lvm >/dev/null"
  H "qm set $vmid --scsi0 local-lvm:vm-$vmid-disk-0,discard=on,ssd=1 --ide2 local:cloudinit \
     --boot order=scsi0 --ciuser labadmin --sshkeys /tmp/lab_pub.key --nameserver 10.250.0.1 \
     $ipseg --ipconfig0 ip=dhcp --onboot 0"
  echo "  VM $vmid created"
}

echo "== [3/5] ISP-LAB (150) =="
H "echo '$PKEY' > /tmp/lab_pub.key"
H "qm create 150 --name ISP-LAB --ostype l26 --memory 1024 --cores 2 --cpu host \
   --scsihw virtio-scsi-single --agent 1 --serial0 socket --vga serial0 \
   --net0 virtio,bridge=vmbr0 \
   --net1 virtio,bridge=vmbr-lab-wan" || true
H "qm importdisk 150 /var/lib/vz/template/iso/debian-13-genericcloud-amd64.qcow2 local-lvm >/dev/null 2>&1" || true
H "qm set 150 --scsi0 local-lvm:vm-150-disk-0,discard=on,ssd=1 --ide2 local:cloudinit \
   --boot order=scsi0 --ciuser labadmin --sshkeys /tmp/lab_pub.key \
   --ipconfig0 ip=dhcp --ipconfig1 ip=10.250.0.1/24 --onboot 0" 2>&1 || echo "  (ISP-LAB may already exist)"

echo "== [4/5] LAN-CLIENT (152) and SIM-LAB (153) =="
H "qm create 152 --name LAN-CLIENT --ostype l26 --memory 768 --cores 1 --cpu host \
   --scsihw virtio-scsi-single --agent 1 --serial0 socket --vga serial0 \
   --net0 virtio,bridge=vmbr-lab-lan \
   --scsi0 local-lvm:8,discard=on,ssd=1 --ide2 local:cloudinit \
   --boot order=scsi0 --ciuser labadmin --sshkeys /tmp/lab_pub.key \
   --ipconfig0 ip=dhcp --onboot 0" 2>&1 || echo "  (LAN-CLIENT may already exist)"
H "qm create 153 --name SIM-LAB --ostype l26 --memory 1024 --cores 2 --cpu host \
   --scsihw virtio-scsi-single --agent 1 --serial0 socket --vga serial0 \
   --net0 virtio,bridge=vmbr-lab-wan \
   --net1 virtio,bridge=vmbr-lab-office \
   --net2 virtio,bridge=vmbr-lab-extra \
   --scsi0 local-lvm:8,discard=on,ssd=1 --ide2 local:cloudinit \
   --boot order=scsi0 --ciuser labadmin --sshkeys /tmp/lab_pub.key \
   --ipconfig0 ip=10.250.0.10/24,gw=10.250.0.1 \
   --ipconfig1 ip=10.79.0.2/24 \
   --ipconfig2 ip=10.78.0.10/24 --onboot 0" 2>&1 || echo "  (SIM-LAB may already exist)"

echo "== [5/5] MR-TEST (151) from VM111 base clone =="
if H "qm config 151 >/dev/null 2>&1"; then
  echo "  VM 151 exists, skipping clone"
else
  H "qm clone 111 151 --name MR-TEST --full true --storage local-lvm >/dev/null 2>&1" || {
    H "qm set 111 --protection 0"
    H "qm clone 111 151 --name MR-TEST --full true --storage local-lvm >/dev/null"
    H "qm set 111 --protection 1"
  }
  # fresh MACs, lab bridges, no production ISOs/firewall
  H "qm set 151 --net0 virtio,bridge=vmbr-lab-wan \
     --net1 virtio,bridge=vmbr-lab-lan \
     --net2 virtio,bridge=vmbr-lab-extra \
     --delete ide0,ide2,sata0 \
     --memory 512 --cores 2 --cpu host --agent 1 --serial0 socket --vga serial0 \
     --nameserver 10.250.0.1 --onboot 0"
fi

echo "== done =="
H "qm list | grep -E 'ISP-LAB|MR-TEST|LAN-CLIENT|SIM-LAB' || true"
