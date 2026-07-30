# Failure scenario matrix

This document records the expected behavior when configuration, networking,
storage, power, or process failures interrupt the router. It is a design and test
contract, not a claim that every physical failure has already been reproduced.

Status values:

- **Automated** — covered by deterministic unit/integration/CI tests.
- **Guarded by code** — the code has an explicit fail-closed or rollback path,
  but target-host evidence is still required.
- **Proxmox required** — must be reproduced on the owner test VM or real network.

## Transaction and IPC failures

| Scenario | Required outcome | Status |
|---|---|---|
| Request fails before `router-applyd` receives it | Retry the identical request and transaction ID; do not advance canonical configuration without a verified response. | Automated |
| Helper completes apply but IPC response is lost | Retry the identical ID so the helper returns its persisted idempotent result. | Automated |
| Confirmation completes but its response is lost | Retry the same confirmation ID; commit only after a verified response. | Automated |
| Both privileged attempts have an unknown outcome | Report `RecoveryRequired`, keep the old SQLite configuration canonical, and rely on console recovery or boot reconciliation. Never report `RolledBack`. | Automated |
| Helper returns success without verification | Reject the transaction, report `RecoveryRequired`, and do not update canonical state. | Automated |
| Helper explicitly reports rollback could not be verified | Propagate `RecoveryRequired`; do not present a successful rollback. | Automated |
| New request arrives while commit-confirm or rollback recovery is pending | Reject the new transaction to prevent overlapping network states. | Automated |
| Duplicate request has the same ID and same payload | Return the persisted helper result without reapplying side effects. | Existing automated coverage |
| Duplicate request ID has different payload | Reject as transaction-ID reuse. | Existing automated coverage |
| Trailing or oversized IPC JSON is received | Reject before privileged execution. | Existing automated coverage |

## Commit-confirm and administrator reachability

| Scenario | Required outcome | Status |
|---|---|---|
| LAN address or CIDR changes | Apply provisionally, require explicit confirmation, and restore the old LAN after timeout. | Automated |
| Wi-Fi bridge topology changes | Require confirmation because the management path can move between physical LAN and bridge. | Automated |
| Management changes to `wireguard_only` before WireGuard is already enabled | Reject; WireGuard must be enabled and verified in a separate transaction first. | Automated |
| WireGuard private key changes while management is WireGuard-only | Apply provisionally and require confirmation through the new working tunnel. | Automated |
| WireGuard listen port changes while management is WireGuard-only | Apply provisionally and require confirmation. | Automated |
| WireGuard tunnel address, peer, or allowed route changes while management is WireGuard-only | Apply provisionally and require confirmation. | Automated |
| Ordinary WireGuard maintenance while LAN management remains available | Do not add an unnecessary confirmation gate. | Automated |
| Confirmation deadline expires and rollback succeeds | Mark `RolledBack`, retain old canonical state, and clear pending state. | Automated |
| First automatic rollback attempt fails | Keep candidate access and pending state, reject new changes, schedule another rollback, and use a fresh rollback ID. | Automated |
| Repeated rollback failure | Continue reporting the pending recovery condition; never silently clear it or claim rollback. | Guarded by code; Proxmox service-failure exercise required |
| Power fails while a change is awaiting confirmation | Unconfirmed state must not enter SQLite; boot reconciliation must reapply the previous canonical configuration before management starts. | Automated model; Proxmox power-cut required |

## Persistence and boot failures

| Scenario | Required outcome | Status |
|---|---|---|
| SQLite commit fails after helper verification | Attempt a privileged rollback; report `RolledBack` only if the rollback response is successful and verified, otherwise `RecoveryRequired`. | Automated |
| Power fails after helper apply but before SQLite commit | On boot, `routerd` loads the old durable revision and reconciles it before exposing management. | Guarded by code; Proxmox power-cut required |
| Power fails after SQLite commit | The committed revision is canonical and boot reconciliation must reproduce it. | Guarded by code; Proxmox power-cut required |
| SQLite WAL recovery after abrupt host stop | Database integrity check must pass or router startup must fail closed. | Existing VM evidence; repeat on target Proxmox |
| SQLite file is corrupt | Refuse normal startup rather than initialize a fresh default router over damaged state. | Guarded by code; fault injection required |
| Disk becomes full or filesystem becomes read-only during snapshot/commit | Abort before canonical advancement; preserve console access and record the exact failed stage. | Proxmox required |
| `routerd` crashes while helper continues | Canonical state remains the authority; restart must reconcile before management is exposed. | Guarded by code; process-kill test required |
| `router-applyd` crashes during apply | `routerd` must not commit without a verified response; restart/reboot reconciliation restores canonical state. | Guarded by code; process-kill test required |

## Firewall, DHCP, DNS, PPPoE, and WireGuard runtime

| Scenario | Required outcome | Status |
|---|---|---|
| nftables candidate is invalid | Preflight rejects it before replacing the active ruleset. | Existing automated coverage |
| nftables load fails after files are written | Restore the saved runtime snapshot; WAN remains default-deny. | Guarded by code; namespace fault injection required |
| dnsmasq syntax is invalid | Reject during preflight. | Existing automated coverage |
| dnsmasq restart or verification fails | Restore previous files and service state; do not commit candidate configuration. | Guarded by code; service-failure injection required |
| DHCP range no longer belongs to LAN subnet | Reject during typed validation. | Existing automated coverage |
| PPPoE credentials/configuration are invalid | Do not commit unless the expected PPP interface, address, and default route are verified. | Guarded by code; real ISP/test concentrator required |
| WAN cable or upstream disappears after an already committed PPPoE setup | Keep configuration durable, allow service reconnect, and keep LAN management available. | Proxmox/physical network required |
| WireGuard activation deletes/replaces an existing interface and later fails | Clean the failed interface and restore the previous runtime snapshot; report recovery if restoration is not verified. | Guarded by code; namespace fault injection required |
| WireGuard is committed, then power fails | Boot reconciliation must recreate interface, address, peers, routes, and firewall policy from canonical state. | Proxmox required |
| WireGuard peer is remote during reboot | Remote client must reconnect without exposing web management on WAN. | External-network Proxmox test required |
| DNS upstream becomes unavailable | Local management and DHCP must remain reachable; DNS failure must not broaden firewall policy. | Proxmox required |

## Update and recovery lifecycle

| Scenario | Required outcome | Status |
|---|---|---|
| Power fails before update pointer switch | Durable operation journal restores or retains the old slot. | Automated |
| Power fails after pointer switch but before journal cleanup | Recovery completes the new slot consistently. | Automated |
| Rollback is interrupted | Journal recovery selects a consistent old or new slot from the runtime pointer. | Automated |
| Update package is unsigned, altered, oversized, contains unsafe paths, symlinks, or hooks | Reject before staging/activation. | Existing automated coverage |
| Candidate slot starts but router health is bad | Operator or recovery path must restore the previous verified slot. | Clean-Alpine automated; Proxmox reboot rehearsal required |
| Both update state and active pointer are corrupt | Fail closed and require local recovery; never guess a slot. | Automated state validation; recovery-media rehearsal required |

## Backup and restore

| Scenario | Required outcome | Status |
|---|---|---|
| Encrypted backup export is interrupted | No router configuration changes; incomplete export is discarded by the caller. | Guarded by code; client interruption test required |
| Backup password or authentication is wrong | Reject without exposing plaintext or changing canonical state. | Existing automated/API coverage |
| Backup JSON, checksum, schema, or encrypted envelope is corrupt | Reject before restore/apply. | Existing validation coverage; expand corpus over time |
| Restore applies a configuration that breaks management | Use the same validation, snapshot, apply, verify, commit-confirm, and rollback path as an ordinary change. | Guarded by architecture; fresh-VM restore rehearsal required |
| Power fails during restore | Old durable configuration must remain canonical unless the restored revision was fully committed; boot reconciliation follows canonical state. | Proxmox required |
| Backup is restored into a fresh VM | Credentials, configuration, snapshots, and runtime services must be verified without importing transient sessions or unsafe host identity. | Proxmox required |

## Mandatory target-Proxmox sequence

The owner VM must still execute these tests in order, with pfSense available as
rollback:

1. Record VMID, exact commit, bridge/NIC mapping, snapshot, and backup.
2. Reboot guest and Proxmox repeatedly; verify stable WAN/LAN identity.
3. Kill `routerd`, kill `router-applyd`, and interrupt a commit-confirm change.
4. Disconnect WAN during PPPoE establishment and after a stable session.
5. Test WireGuard from an unrelated external network, including reboot recovery.
6. Inject read-only filesystem and low-disk conditions on disposable storage.
7. Force-stop the VM during apply, confirmation, update activation, rollback, and restore.
8. Run external IPv4/IPv6 scans and sustained throughput/latency tests.
9. Restore an encrypted backup into a new VM.
10. Run at least seven days while recording memory, disk, logs, reconnects, and errors.

Record results in a private dated report. A scenario is not considered physically
qualified until the report includes the exact command, expected result, observed
result, and recovery action.
