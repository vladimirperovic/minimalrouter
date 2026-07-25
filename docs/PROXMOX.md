# 📦 Proxmox VE & Homelab Deployment Guide

Minimal Router OS is engineered to run seamlessly as **VM #100** on Proxmox VE hypervisors (e.g. Intel N100 / dual 2.5G Intel NIC Mini PCs).

---

## ⚡ 1-Line Automated Proxmox VM Setup

Run this single command directly in your **Proxmox VE Node Shell**:

```bash
bash <(curl -sSL https://raw.githubusercontent.com/vladimirperovic/minimalrouter/main/packaging/proxmox/create-vm.sh)
```

### What this script automates:
1. **Creates VM #100** (`Minimal-Router-OS`) with 512 MB RAM, 1 vCPU (host architecture), and VirtIO SCSI storage.
2. **Configures High Boot Priority** (`onboot: 1`, `order=1`) so Minimal Router OS starts **first before any other VMs**.
3. **Verifies Proxmox Network Bridges**:
   - `net0` → `vmbr0` (Physical WAN / Internet NIC)
   - `net1` → `vmbr1` (Internal Virtual Private LAN Bridge for all your other Proxmox VMs & containers)
4. **Pre-configures LUKS Full Disk Encryption Support** on the boot volume.

---

## 🛡️ LUKS Full Disk Encryption Setup

To protect your PPPoE credentials, WireGuard private keys, and routing tables against physical theft:

1. During initial setup, select **LUKS Disk Encryption** (`DISKOPTS="-m sys -e luks"`).
2. Enter your strong disk encryption passphrase.
3. Every system boot will verify disk integrity before launching `routerd` and `router-applyd`.

---

## 🌐 Networking Architecture on Proxmox VE

```
+-------------------------------------------------------------------+
|                        PROXMOX VE HOST                            |
|                                                                   |
|   +-----------------------------------------------------------+   |
|   |         VM #100: MINIMAL ROUTER OS (512 MB RAM)           |   |
|   |                                                           |   |
|   |   [net0: VirtIO] <---------> vmbr0 <---> WAN / Modem      |   |
|   |   [net1: VirtIO] <---------> vmbr1 <---> Private LAN      |   |
|   |                                               |           |   |
|   +-----------------------------------------------+-----------+   |
|                                                   |               |
|            +--------------------------------------+-------+       |
|            |                      |                       |       |
|    +---------------+      +---------------+      +------------+  |
|    |  VM #101:     |      |  VM #102:     |      | LXC #103:  |  |
|    |  Home Assistant|     |  Plex / NAS   |      | Nextcloud  |  |
|    +---------------+      +---------------+      +------------+  |
|                                                                   |
+-------------------------------------------------------------------+
```

---

## 🔧 Manual VM Creation Settings (Alternative)

If you prefer manually creating the VM in the Proxmox Web GUI:

- **OS**: Linux (kernel 6.x)
- **System**: SCSI Controller = `VirtIO SCSI Single`, Qemu Agent = `Enabled`
- **Disks**: `8 GB SCSI`, SSD emulation = `Enabled`, Discard = `Enabled`
- **CPU**: `1 Core`, Type = `host`
- **Memory**: `512 MB`
- **Network**:
  - Net 0: `vmbr0` (WAN)
  - Net 1: `vmbr1` (LAN)
