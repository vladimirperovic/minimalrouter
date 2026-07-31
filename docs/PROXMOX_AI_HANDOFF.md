# Proxmox VM continuation guide for an AI operator

This document is an operational handoff for another AI agent or engineer who has
access to the owner's Proxmox host and must continue testing the **existing**
Minimal Router VM safely.

It is intentionally stored only in the private `minimalrouterhome` repository.
Do not copy hostnames, VM IDs, bridge names, MAC addresses, public addresses,
credentials, tokens, backups, packet captures, or real household inventory into
this file or into a public issue.

## Mission

Continue from the already-created Proxmox VM. Do not recreate, delete, clone,
rewire, or promote the VM to production until its identity and current network
connections have been confirmed read-only.

The immediate goal is a controlled pilot, not an unattended pfSense replacement.
Keep the existing pfSense VM/appliance available for immediate rollback.

## Non-negotiable safety rules

1. **Inventory first.** Never guess the Proxmox node, VM ID, VM name, disk, WAN
   bridge, LAN bridge, or guest interface order.
2. **Do not start every VM.** Identify exactly one candidate Minimal Router VM.
3. **Do not attach the candidate WAN directly to the ISP during the first test.**
   Use the existing test/NAT WAN path until basic validation passes.
4. **Keep the candidate LAN isolated.** It must not share the production LAN with
   the active pfSense DHCP server.
5. **Never run two active DHCP servers on the same LAN.**
6. **Keep Proxmox console access open** before changing networking, applying an
   update, rebooting, or testing rollback.
7. **Do not paste secrets into chat, GitHub, logs, or reports.** PPPoE credentials,
   administrator passwords, WireGuard keys, Cloudflare tokens, session values,
   public addresses, MAC addresses, and backups remain local.
8. **Do not use `qm stop` as a normal shutdown.** Use a graceful guest shutdown;
   force-stop only as an explicitly recorded power-loss test after a known-good
   snapshot/backup and with pfSense ready.
9. **Do not delete the previous update slot** until the new slot has survived
   service restart, guest reboot, management login, DHCP/DNS/NAT validation, and
   an explicit rollback rehearsal.
10. **Stop on ambiguity.** If more than one VM looks like the candidate, or bridge
    purpose is unclear, do not change anything.

## Known project state

At the time of this handoff:

- an existing Proxmox VM has already been created by the owner;
- the exact Proxmox node, VM ID, VM name, bridges, guest addresses, and installed
  commit are intentionally not stored in Git;
- the repository automated suites pass on the current branch baseline;
- crash-safe A/B update journaling, deep validation, fuzzing, ARM64 smoke tests,
  isolated WAN-router-LAN tests, and performance baselines exist;
- real Proxmox, real PPPoE, real NIC, external scan, long-duration, and target-host
  recovery evidence are still required.

Read [`CURRENT_VALIDATION.md`](CURRENT_VALIDATION.md), [`PROXMOX.md`](PROXMOX.md),
[`TESTING.md`](TESTING.md), and [`RECOVERY.md`](RECOVERY.md) before execution.

## Phase 0 — establish access without exposing secrets

The operator needs:

- read/write access to the correct Proxmox node through a trusted shell or API;
- Proxmox console access to the candidate guest;
- access to this private repository;
- a separate LAN test client or disposable VM;
- the existing pfSense rollback path.

Credentials must be entered directly into the trusted execution environment. Do
not request that the owner paste them into GitHub documentation or a public chat.

## Phase 1 — read-only Proxmox inventory

Run read-only commands first:

```sh
pvesh get /cluster/resources --type vm --output-format json-pretty
qm list
```

For each plausible candidate, inspect without changing it:

```sh
qm status <VMID>
qm config <VMID>
```

Record locally, outside Git:

- Proxmox node and VM ID;
- VM name and notes/tags;
- current state;
- CPU type and vCPU count;
- RAM and ballooning state;
- disk/storage and boot order;
- guest-agent setting;
- each NIC model, bridge, firewall flag, VLAN tag, and link state;
- whether any passthrough device exists.

Do not publish raw `qm config` output because it may contain real network
identifiers.

### Candidate acceptance rule

Proceed only when one VM is unambiguously the Minimal Router candidate and it has
exactly the expected router boundary:

- one intended WAN NIC;
- one intended LAN NIC;
- a test/NAT WAN bridge during initial testing;
- an isolated LAN bridge;
- no accidental connection that would place a second DHCP server on production
  LAN.

## Phase 2 — preserve rollback before boot

Before changing or updating the guest:

1. Confirm pfSense can be restored or started without depending on the candidate.
2. Confirm the candidate is not in the middle of a configuration commit-confirm
   window or firmware activation.
3. Export an encrypted Minimal Router backup through the dashboard when available.
4. Record the current repository commit and installed update-slot state.
5. Take a Proxmox snapshot only from a known consistent state. Prefer graceful
   shutdown before the snapshot unless guest-agent filesystem freeze has been
   explicitly verified.
6. Do not treat a Proxmox snapshot as a substitute for the application backup.

Useful host actions:

```sh
qm shutdown <VMID> --timeout 60
qm status <VMID>
qm snapshot <VMID> pre-test-YYYYMMDD-HHMM --description "Known-good state before controlled Minimal Router tests"
```

If shutdown does not complete, inspect the console and guest-agent state. Do not
force-stop automatically.

## Phase 3 — safe start and guest baseline

Start only the identified candidate:

```sh
qm start <VMID>
qm status <VMID>
```

Open the Proxmox console immediately. In the guest, run read-only baseline checks:

```sh
cat /etc/alpine-release
uname -a
ip -brief link
ip -brief address
ip route
mount
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

Also verify locally without publishing sensitive output:

- WAN and LAN guest interfaces match the intended Proxmox NIC order;
- management listens only on the intended LAN/WireGuard path;
- there is no unexpected default route through the isolated LAN;
- services are not crash-looping;
- system time is correct;
- persistent storage is writable and has adequate free space.

Record kernel, Alpine version, vCPU, RAM, NIC model, bridge type, offload settings,
and exact repository/installed commit in the private test report. Redact real
addresses and identifiers.

## Phase 4 — bring the VM to the current repository build

Use the private repository and an exact commit:

```sh
git clone <trusted-private-repository-url>
cd minimalrouterhome
git fetch --all --tags
git checkout main
git pull --ff-only
git rev-parse HEAD
pnpm --dir web install --frozen-lockfile
make dist-amd64
cd build
sha256sum -c minimalrouter-linux-amd64.tar.gz.sha256
```

Transfer the archive and checksum over a trusted local path. Verify the checksum
again inside the guest before extracting.

For a fresh installation, follow [`INSTALLATION.md`](INSTALLATION.md). For an
already-installed system, do not overwrite live binaries manually. Use the
verified A/B update path only with a signed payload and the pinned public key.

The update CLI is deliberately explicit:

```sh
router-update status
router-update stage --dir <EXTRACTED_SIGNED_PAYLOAD> --manifest <SIGNED_MANIFEST>
router-update activate --version <VERSION> --confirm ACTIVATE-UPDATE
```

After activation, restart the services or reboot, then verify health. To return to
the previous verified slot:

```sh
router-update rollback --confirm ROLLBACK-UPDATE
```

Never fabricate a signing key and call that a trusted production update. A local
unsigned development archive is suitable for a controlled reinstall only, not for
claiming the signed-update release gate passed.

## Phase 5 — test order

Run tests in this order and stop at the first unexplained failure.

### A. Boot and service reconciliation

- five graceful guest reboot cycles;
- at least two Proxmox stop/start lifecycle cycles using graceful shutdown;
- WAN/LAN roles stable after every boot;
- `routerd`, `router-applyd`, dnsmasq, nftables, and enabled services healthy;
- dashboard reachable only from isolated LAN;
- no pending transaction or update operation after boot.

### B. LAN services and management boundary

From an isolated client:

- acquire a DHCP lease;
- resolve DNS through the router;
- log in and log out through HTTPS;
- verify WAN cannot reach dashboard/API ports;
- verify direct WAN-to-LAN unsolicited traffic is denied;
- verify an unconfirmed disruptive LAN change rolls back;
- verify recovery console access remains available.

### C. NAT and forwarding

Using a test WAN, not the production ISP:

- confirm outbound IPv4 through NAT;
- test TCP and UDP in both practical directions permitted by policy;
- test 1, 64, and a larger controlled number of parallel connections;
- record connection failures, retransmits, latency, and packet loss;
- confirm management remains responsive during load.

### D. Target-host performance

Record every command and environment detail. Measure:

- boot-to-forwarding-ready and boot-to-management-ready;
- idle CPU and RAM;
- loaded CPU and RAM;
- LAN-to-WAN and WAN-to-LAN throughput where policy permits;
- packets per second with small and large frames;
- latency and jitter without load and under load;
- VirtIO multiqueue/offload configuration;
- disk growth during tests;
- management API responsiveness during traffic.

Use at least two traffic-generator endpoints. Do not use the Go control plane as
the forwarding path. Treat same-host VirtIO results separately from physical NIC
or passthrough results.

### E. Recovery and failure tests

After a known-good snapshot and backup:

- update activation and explicit rollback;
- reboot after activation and after rollback;
- kill/reboot during controlled update stages only when a rollback path is ready;
- service crash and automatic restart behavior;
- full-disk simulation on a disposable clone or dedicated test disk;
- read-only filesystem simulation only on a disposable test target;
- backup restore into a fresh VM;
- incorrect/unconfirmed LAN change and automatic recovery;
- abrupt power-loss test only after graceful scenarios pass.

Never run destructive disk tests on the only candidate copy.

### F. Real PPPoE maintenance-window test

Run only after A–E pass and the owner has a tested rollback plan:

- disconnect the candidate test WAN;
- ensure pfSense can be restored immediately;
- enter PPPoE credentials locally without logging them;
- establish session;
- record negotiated MTU, route, DNS, reconnect time, CPU, and logs with secrets
  redacted;
- disconnect and reconnect repeatedly;
- reboot the guest and verify automatic reconnection;
- restore pfSense immediately if authentication, MTU, routing, or management
  recovery is unclear.

### G. External validation and soak

- scan IPv4 and IPv6 from an unrelated external host;
- verify no WAN management exposure;
- test WireGuard from an unrelated mobile/external network;
- run at least seven continuous days before any production recommendation;
- monitor memory, CPU, disk, log growth, connection stability, and reconnects;
- repeat update/rollback and backup/restore after the soak period.

## Evidence format

Create a new private dated report, for example:

```text
docs/PROXMOX_TEST_REPORT_YYYY-MM-DD.md
```

The report must include:

- exact repository commit;
- Proxmox version and kernel;
- guest Alpine/kernel version;
- vCPU, RAM, disk, NIC model, bridge mode, and offload settings;
- test topology described with synthetic labels;
- commands used;
- raw measurement summaries or attached private artifacts;
- pass/fail for every gate;
- failures and recovery steps;
- explicit limitations;
- final recommendation: isolated pilot, guarded production pilot, or reject.

Redact VM IDs, hostnames, MAC addresses, public/private household addresses,
credentials, tokens, keys, device names, and backup contents before committing.

## Stop conditions

Stop testing and restore pfSense when any of these occurs:

- WAN/LAN identity changes unexpectedly;
- management becomes unreachable and console recovery is unclear;
- two DHCP servers appear on one LAN;
- default-deny WAN behavior fails;
- rollback does not restore the previous known-good slot/configuration;
- persistent state becomes corrupt;
- unexplained packet loss, CPU saturation, memory growth, disk growth, or service
  restarts occur;
- the operator cannot prove which bridge or NIC is being modified.

## Completion criterion

The handoff is complete only when another operator can reproduce the VM inventory,
boot, update, rollback, DHCP/DNS/NAT, security-boundary, throughput, reboot,
backup/restore, PPPoE, external scan, and soak results from the private dated
report without relying on undocumented chat context.
