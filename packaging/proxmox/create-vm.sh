#!/usr/bin/env bash
# Minimal Router OS — Automated Proxmox VE VM Installer Helper Script
# Run from a verified release checkout on a Proxmox VE node.

set -euo pipefail

echo "========================================================"
echo "   Minimal Router OS — Proxmox VE Appliance Installer  "
echo "========================================================"

# 1. Detect next available VM ID or use default 100
VMID=$(pvesh get /cluster/nextid)
VM_NAME="Minimal-Router-OS"
RAM_MB=1024
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

# 2. Download and verify the pinned Minimal Router OS ISO
ISO_NAME="minimalrouter-0.1.0-x86_64.iso"
ISO_PATH="/var/lib/vz/template/iso/$ISO_NAME"
ISO_URL="https://github.com/vladimirperovic/minimalrouter/releases/download/v0.1.0/$ISO_NAME"
EXPECTED_SHA256="${MINIMALROUTER_ISO_SHA256:-}"
if ! printf '%s' "$EXPECTED_SHA256" | grep -Eq '^[0-9a-fA-F]{64}$'; then
    echo "ERROR: Set MINIMALROUTER_ISO_SHA256 from independently verified release metadata." >&2
    exit 1
fi
EXPECTED_SHA256="$(printf '%s' "$EXPECTED_SHA256" | tr 'A-F' 'a-f')"

if [ ! -f "$ISO_PATH" ]; then
    echo "[2/5] Downloading Minimal Router OS ISO..."
    TMP_ISO="$(mktemp "${ISO_PATH}.download.XXXXXX")"
    trap 'rm -f "$TMP_ISO"' EXIT
    curl --proto '=https' --tlsv1.2 --fail --location --silent --show-error \
        --output "$TMP_ISO" "$ISO_URL"
    ACTUAL_SHA256="$(sha256sum "$TMP_ISO" | awk '{ print $1 }')"
    [ "$ACTUAL_SHA256" = "$EXPECTED_SHA256" ] || {
        echo "ERROR: ISO SHA-256 verification failed." >&2
        exit 1
    }
    install -m 0644 "$TMP_ISO" "$ISO_PATH"
    rm -f "$TMP_ISO"
    trap - EXIT
else
    echo "[2/5] Verifying existing Minimal Router OS ISO..."
    ACTUAL_SHA256="$(sha256sum "$ISO_PATH" | awk '{ print $1 }')"
    [ "$ACTUAL_SHA256" = "$EXPECTED_SHA256" ] || {
        echo "ERROR: Existing ISO is not the verified release artifact." >&2
        exit 1
    }
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

# 4. Create VM with VirtIO network interfaces.
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
echo "   - Storage:              Standard Alpine root filesystem"
echo ""
echo " To start VM in Proxmox console:"
echo "   qm start $VMID"
echo "========================================================"
