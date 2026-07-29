# Proxmox VE lab deployment

Minimal Router OS targets a QEMU/KVM virtual machine on Proxmox VE. An
unprivileged LXC container is not a supported router boundary because it shares
the host kernel and cannot provide the same nftables, interface, and device
isolation.

Read [INSTALLATION.md](INSTALLATION.md) before using this guide.

## Current test deployment

There is no signed Minimal Router release ISO yet. Do not use
`packaging/proxmox/create-vm.sh` until a reviewed release publishes both the ISO
and an independently verifiable SHA-256 checksum.

For a controlled lab trial, create the VM manually:

1. Install Alpine Linux 3.22 x86_64 in a QEMU VM.
2. Allocate 1 vCPU, 1 GiB RAM, and an 8 GiB disk.
3. Add two VirtIO NICs. Connect `net0` to a test WAN/NAT bridge and `net1` to an
   isolated LAN bridge such as `vmbr1`.
4. Do not connect the isolated LAN bridge to the normal household/office LAN and
   do not attach the candidate WAN directly to the ISP during the first trial.
5. On a trusted development computer, check out the exact Git commit, run
   `pnpm --dir web install --frozen-lockfile`, then `make dist-amd64`.
6. Copy both `build/minimalrouter-linux-amd64.tar.gz` and its `.sha256` file to
   the Alpine VM and verify the checksum on the target.
7. Extract the archive and run `sh install.sh` from the extracted directory.
8. Start `router-applyd` and `routerd`, then complete setup from a client attached
   only to the isolated LAN bridge. The first-run management address is normally
   `https://192.168.1.1:8443` after interface reconciliation.

Do not pipe a mutable network response into a root shell. Keep the existing
router available for rollback and keep the Proxmox console open during every
network change.

## Proxmox configuration notes

- **Machine type:** use a current QEMU machine type supported by the Proxmox host.
- **CPU:** `host` is appropriate for a fixed homelab host; use a migration-safe CPU
  model only when live migration is an actual requirement and has been tested.
- **NICs:** use VirtIO and document the Proxmox bridge connected to each NIC.
- **Disk:** use a normal virtual disk with reliable host storage. Do not depend on
  volatile storage for the canonical state or snapshots.
- **Boot order:** ensure the Alpine system disk is first after installation.
- **Console:** keep the Proxmox console available until the appliance has survived
  setup, rollback, and reboot tests.
- **Snapshots:** take snapshots only from a known consistent state. A hypervisor
  snapshot is not a substitute for an encrypted Minimal Router backup.

## Included optimizations

- **QEMU Guest Agent (`qemu-guest-agent`):** allows Proxmox to display guest
  addresses and perform graceful lifecycle actions when installed and enabled in
  the guest.
- **Host time synchronization (`chrony`):** reduces clock drift after host pause,
  snapshot, or migration. Correct time is important for TLS, TOTP, WireGuard
  diagnostics, and audit events.
- **VirtIO networking:** provides efficient virtual networking. Minimal Router
  keeps `rp_filter=1` (strict) to preserve anti-spoofing. A deployment that
  genuinely needs asymmetric routing requires a documented threat review before
  relaxing this.

Use 1 GiB RAM for comfortable lab headroom; 512 MiB is the measured test minimum,
not a production sizing promise. The repository VM helper is retained for a
future signed-image workflow and is not the current installation path.

## Validation before broader testing

Before moving beyond an isolated VM test, verify:

- WAN and LAN roles remain correct after reboot;
- management is reachable only from the intended LAN or WireGuard path;
- an unconfirmed disruptive change rolls back;
- DHCP, DNS, NAT, and firewall behavior survive reboot reconciliation;
- backup export and restore work with synthetic data;
- the existing router can be restored immediately.

See [TROUBLESHOOTING.md](TROUBLESHOOTING.md) for safe diagnostics.