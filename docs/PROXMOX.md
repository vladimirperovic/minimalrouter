# Proxmox VE Deployment & Optimization Guide

Minimal Router OS targets a QEMU/KVM virtual machine on Proxmox VE. An
unprivileged LXC container is not a supported router boundary because it
shares the host kernel and cannot provide the same nftables, interface, and
device isolation.

## 1. Automated Setup Script

Run the reviewed local script from a verified checkout inside the guest:

```bash
sh /usr/local/bin/proxmox-setup.sh
```

Do not pipe a mutable network response into a root shell. For VM creation,
provide an independently verified ISO digest:

```bash
export MINIMALROUTER_ISO_SHA256='<verified 64-hex digest>'
bash packaging/proxmox/create-vm.sh
```

## 2. Included Optimizations

- **QEMU Guest Agent (`qemu-guest-agent`)**: Enables Proxmox VE Web UI to display active router IP addresses, memory usage, and execute graceful VM reboots/shutdowns.
- **Host Time Synchronization (`chrony`)**: Prevents Real-Time Clock (RTC) drift when Proxmox VE hosts pause, snapshot, or live-migrate the VM.
- **VirtIO networking**: Keeps `rp_filter=1` (strict) to preserve anti-spoofing.
  A deployment that genuinely needs asymmetric routing requires a documented
  threat review before relaxing this.

The helper only creates an empty virtual disk. Install Alpine normally inside
the VM. The current helper allocates 1 GiB RAM for production headroom; 512 MiB
is the measured test minimum.
