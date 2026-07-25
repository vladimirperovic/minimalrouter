# Proxmox VE Deployment & Optimization Guide

Minimal Router OS runs efficiently as a QEMU/KVM virtual machine or unprivileged LXC container on Proxmox VE.

## 1. Automated Setup Script

Run the automated Proxmox guest optimization script inside your Minimal Router OS instance:

```bash
sh /usr/local/bin/proxmox-setup.sh
```

Or run directly from GitHub:

```bash
curl -sSL https://raw.githubusercontent.com/vladimirperovic/minimalrouter/main/scripts/proxmox-setup.sh | sh
```

## 2. Included Optimizations

- **QEMU Guest Agent (`qemu-guest-agent`)**: Enables Proxmox VE Web UI to display active router IP addresses, memory usage, and execute graceful VM reboots/shutdowns.
- **Host Time Synchronization (`chrony`)**: Prevents Real-Time Clock (RTC) drift when Proxmox VE hosts pause, snapshot, or live-migrate the VM.
- **VirtIO Net Offloading**: Tunes Linux Kernel `rp_filter` to `2` (loose mode) to ensure VirtIO Linux bridges (`vmbr0`, `vmbr1`) route packets cleanly without dropping asymmetrical VLAN packets.
