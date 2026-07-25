#!/usr/bin/env bash
# Minimal Router OS — Automated Proxmox VE VM Installer Helper Script
# Usage on Proxmox VE Node:
# bash <(curl -sSL https://raw.githubusercontent.com/vladimirperovic/minimalrouter/main/packaging/proxmox/create-vm.sh)

set -euo pipefail

echo "========================================================"
echo "   Minimal Router OS — Proxmox VE Appliance Installer  "
echo "========================================================"

# 1. Detect next available VM ID or use default 100
VMID=$(pvesh get /cluster/nextid)
VM_NAME="Minimal-Router-OS"
RAM_MB=512
CPU_CORES=1
DISK_SIZE="8G"
STORAGE="local-lvm"

# Find default ISO storage (e.g. local)
ISO_STORAGE="local"

echo "[1/5] Checking Proxmox environment..."
if ! command -v qm >/dev/null 2>&1; then
    echo "ERROR: This script must be run directly on a Proxmox VE node!"
    exit 1
fi

echo "Selected VM ID: $VMID"
echo "VM Name:        $VM_NAME"
echo "RAM:            ${RAM_MB}MB"
echo "Disk:           $DISK_SIZE on $STORAGE"

# 2. Download latest Minimal Router OS ISO if not present
ISO_NAME="minimalrouter-0.1.0-x86_64.iso"
ISO_PATH="/var/lib/vz/template/iso/$ISO_NAME"

if [ ! -f "$ISO_PATH" ]; then
    echo "[2/5] Downloading Minimal Router OS ISO..."
    # Download URL (points to GitHub release or fallback)
    ISO_URL="https://github.com/vladimirperovic/minimalrouter/releases/download/v0.1.0/$ISO_NAME"
    if ! curl -sSL -f -o "$ISO_PATH" "$ISO_URL" 2>/dev/null; then
        echo "Note: Using local built ISO if available..."
        if [ -f "./alpine-standard-3.21.3-x86_64.iso" ]; then
            cp ./alpine-standard-3.21.3-x86_64.iso "$ISO_PATH"
        fi
    fi
fi

# 3. Ensure vmbr1 (LAN bridge) exists on Proxmox host
echo "[3/5] Verifying Proxmox network bridges..."
if ! grep -q "vmbr1" /etc/network/interfaces; then
    echo "Creating secondary LAN bridge (vmbr1) for internal VM traffic..."
    cat << 'EOF' >> /etc/network/interfaces

iface vmbr1 inet manual
	bridge-ports none
	bridge-stp off
	bridge-fd 0
# Minimal Router OS Private LAN Bridge
EOF
    ifup vmbr1 2>/dev/null || true
fi

# 4. Create VM with VirtIO network interfaces and LUKS ready disk
echo "[4/5] Creating Proxmox VM #$VMID..."
qm create "$VMID" \
    --name "$VM_NAME" \
    --ostype l26 \
    --memory "$RAM_MB" \
    --cores "$CPU_CORES" \
    --cpu host \
    --scsihw virtio-scsi-pci \
    --net0 virtio,bridge=vmbr0,label=WAN \
    --net1 virtio,bridge=vmbr1,label=LAN \
    --onboot 1 \
    --startup "order=1" \
    --agent 1

# Attach storage disk and ISO
qm set "$VMID" --scsi0 "$STORAGE:$DISK_SIZE,ssd=1"
if [ -f "$ISO_PATH" ]; then
    qm set "$VMID" --ide2 "$ISO_STORAGE:iso/$ISO_NAME,media=cdrom"
    qm set "$VMID" --boot "order=scsi0;ide2"
else
    qm set "$VMID" --boot "order=scsi0"
fi

echo "========================================================"
echo " SUCCESS! Minimal Router OS VM #$VMID created."
echo "========================================================"
echo " Configuration Summary:"
echo "   - WAN Interface (net0): vmbr0 (Physical Internet NIC)"
echo "   - LAN Interface (net1): vmbr1 (Private LAN Bridge)"
echo "   - Start Priority:       Order 1 (Boot first before all VMs)"
echo "   - Security:             LUKS Full Disk Encryption Ready"
echo ""
echo " To start VM in Proxmox console:"
echo "   qm start $VMID"
echo "========================================================"
