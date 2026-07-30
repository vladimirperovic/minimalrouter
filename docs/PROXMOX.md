# Proxmox VE deployment and existing VM pilot

Minimal Router OS targets a QEMU/KVM virtual machine on Proxmox VE. An
unprivileged LXC container is not a supported router boundary because it shares
the host kernel and cannot provide the same nftables, interface, and device
isolation.

The owner has already created a candidate Proxmox VM. Its node, VM ID, bridge
names, addresses, and credentials are intentionally not stored in Git.

Another AI agent or engineer must use the private operational handoff before
starting or changing the VM:

- [`PROXMOX_AI_HANDOFF.md`](PROXMOX_AI_HANDOFF.md)

Also read [`CURRENT_VALIDATION.md`](CURRENT_VALIDATION.md),
[`INSTALLATION.md`](INSTALLATION.md), [`TESTING.md`](TESTING.md), and
[`RECOVERY.md`](RECOVERY.md).

## Current status

There is no signed stable ISO. Automated clean-Alpine, update/rollback,
crash/fuzz, ARM64, security, network-namespace, and performance validation passes,
but target-host evidence is still missing.

The current recommendation is a guarded Proxmox pilot with:

- console access;
- an isolated LAN;
- a test/NAT WAN during initial testing;
- pfSense ready for immediate rollback.

## Existing VM rule

Do not recreate or rewire the VM before read-only discovery. Identify exactly one
candidate with:

```sh
pvesh get /cluster/resources --type vm --output-format json-pretty
qm list
qm status <VMID>
qm config <VMID>
```

Proceed only when the candidate and the purpose of both NIC bridges are
unambiguous. Never publish raw `qm config` output because it may expose real
network identifiers.

## Recommended VM baseline

- Alpine Linux 3.22 x86_64;
- QEMU/KVM VM, not LXC;
- 1 vCPU to begin, CPU type `host` on a fixed homelab node;
- 1 GiB RAM;
- 8 GiB reliable virtual disk;
- two VirtIO NICs;
- QEMU Guest Agent installed and enabled;
- reliable host and guest time synchronization.

The measured 512 MiB minimum is test evidence, not a production sizing promise.

## Network boundary

During initial tests:

- candidate WAN connects to a test/NAT bridge;
- candidate LAN connects to an isolated bridge;
- candidate LAN does not share the active pfSense LAN;
- only one DHCP server exists on each broadcast domain;
- the production ISP is not connected until isolated validation and rollback
  rehearsal pass.

Guest interface numbering must not be assumed from Proxmox `net0`/`net1`. Confirm
roles using bridge mapping, carrier, addresses, and routes.

## Preserve rollback

Before any update or destructive test:

1. Confirm pfSense can be restored or started independently.
2. Confirm no configuration commit-confirm or firmware operation is pending.
3. Export an encrypted application backup when possible.
4. Record the installed commit and `router-update status` privately.
5. Shut down gracefully before taking a known-good snapshot unless guest-agent
   filesystem freeze has been explicitly verified.

```sh
qm shutdown <VMID> --timeout 60
qm status <VMID>
qm snapshot <VMID> pre-test-YYYYMMDD-HHMM --description "Known-good Minimal Router pilot state"
```

Do not use `qm stop` as routine shutdown. Use it only for an explicitly planned
power-loss test after safe scenarios pass.

## Safe boot baseline

Start only the identified VM and open its console:

```sh
qm start <VMID>
qm status <VMID>
```

Inside the guest:

```sh
cat /etc/alpine-release
uname -a
ip -brief link
ip -brief address
ip route
findmnt /
df -h
df -i
rc-service router-applyd status
rc-service routerd status
rc-update show | grep -E 'routerd|router-applyd'
router-update status
readlink -f /var/lib/minimalrouter-update/current 2>/dev/null || true
readlink -f /var/lib/minimalrouter-update/previous 2>/dev/null || true
```

Confirm WAN/LAN mapping, management boundary, time, storage, service health, and
absence of pending operations before applying changes.

## Bring the VM to the current private build

Build an exact private-repository commit on a trusted machine:

```sh
git clone <trusted-private-repository-url>
cd minimalrouterhome
git checkout main
git pull --ff-only
git rev-parse HEAD
pnpm --dir web install --frozen-lockfile
make dist-amd64
cd build
sha256sum -c minimalrouter-linux-amd64.tar.gz.sha256
```

Verify the checksum again after transfer. Follow `INSTALLATION.md` for a fresh
installation. Do not overwrite live binaries manually.

For an already-installed system, use the signed A/B path only:

```sh
router-update status
router-update stage --dir <EXTRACTED_SIGNED_PAYLOAD> --manifest <SIGNED_MANIFEST>
router-update activate --version <VERSION> --confirm ACTIVATE-UPDATE
```

After health verification, retain the previous slot. Explicit rollback is:

```sh
router-update rollback --confirm ROLLBACK-UPDATE
```

A locally generated development signing key must not be presented as an
owner-trusted production key.

## Required test order

1. Repeated graceful boot and service reconciliation.
2. DHCP, DNS, HTTPS management, NAT, and default-deny WAN checks on isolated
   topology.
3. Unconfirmed LAN-change rollback and recovery-console access.
4. Update activation, reboot, health verification, and rollback.
5. Target-host CPU, RAM, disk, throughput, packet rate, latency, jitter, loss,
   and management responsiveness.
6. Backup restore into a fresh VM.
7. Controlled service, disk-pressure, read-only, corrupt-state, and power-loss
   tests on a disposable target.
8. Real PPPoE only during a maintenance window with pfSense rollback proven.
9. External IPv4/IPv6 scan and WireGuard from an unrelated network.
10. At least seven days of stable operation before guarded production use.

The full command order, stop conditions, redaction rules, and report format are in
`PROXMOX_AI_HANDOFF.md`.

## Evidence

Create a new private dated report such as:

```text
docs/PROXMOX_TEST_REPORT_YYYY-MM-DD.md
```

Include exact commits, versions, resources, synthetic topology labels, commands,
measurements, failures, recovery steps, limitations, and a final recommendation.
Do not commit VM IDs, hostnames, bridge inventory, MAC addresses, public or
household addresses, credentials, tokens, keys, or backups.

## Production boundary

Do not remove pfSense solely because the VM boots or a synthetic test is fast.
Real Proxmox, PPPoE, external scan, restore, sustained operation, power-loss, and
recovery evidence remain mandatory.
