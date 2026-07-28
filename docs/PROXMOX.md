# Proxmox VE Deployment & Optimization Guide

Minimal Router OS targets a QEMU/KVM virtual machine on Proxmox VE. An
unprivileged LXC container is not a supported router boundary because it
shares the host kernel and cannot provide the same nftables, interface, and
device isolation.

## 1. Current test deployment

There is no signed Minimal Router release ISO yet. Do not use
`packaging/proxmox/create-vm.sh` until a reviewed release publishes both the
ISO and its independently verifiable SHA-256.

For a lab trial, create the VM manually:

1. Install Alpine Linux 3.22 x86_64 in a QEMU VM.
2. Allocate 1 vCPU, 1 GiB RAM, and an 8 GiB disk.
3. Add two VirtIO NICs. Connect `net0` to a test WAN/NAT bridge and `net1` to
   an isolated LAN bridge such as `vmbr1`.
4. On a development computer, check out the exact Git commit, run
   `pnpm --dir web install --frozen-lockfile`, then `make dist-amd64`.
5. Copy `build/minimalrouter-linux-amd64.tar.gz` to the Alpine VM, verify its
   checksum, extract it, and run `sh install.sh` from the extracted directory.
6. Start `router-applyd` and `routerd`, then complete setup from a client
   attached only to the isolated LAN bridge.

Do not pipe a mutable network response into a root shell. Keep the current
pfSense router available for rollback and do not connect this VM directly to
the production ISP/LAN during the first trial.

## 2. Included Optimizations

- **QEMU Guest Agent (`qemu-guest-agent`)**: Enables Proxmox VE Web UI to display active router IP addresses, memory usage, and execute graceful VM reboots/shutdowns.
- **Host Time Synchronization (`chrony`)**: Prevents Real-Time Clock (RTC) drift when Proxmox VE hosts pause, snapshot, or live-migrate the VM.
- **VirtIO networking**: Keeps `rp_filter=1` (strict) to preserve anti-spoofing.
  A deployment that genuinely needs asymmetric routing requires a documented
  threat review before relaxing this.

Use 1 GiB RAM for production headroom; 512 MiB is the measured test minimum.
The repository helper is retained for the future signed-ISO release and is not
the current installation path.
