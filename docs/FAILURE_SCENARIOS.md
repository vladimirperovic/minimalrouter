# Failure scenario matrix

This document records the expected behavior when configuration, networking,
storage, power, or process failures interrupt the router. It is a design and test
contract, not a claim that every physical failure has already been reproduced.

Status values:

- **Automated** — covered by deterministic unit/integration/CI tests.
- **Guarded by code** — the code has an explicit fail-closed or rollback path,
  but target-host evidence is still required.
- **Proxmox required** — must be reproduced on the owner test VM or real network.

A configuration transaction may end only as:

- `Committed` — runtime, SQLite canonical state, and helper recovery metadata are
  consistent and verified;
- `RolledBack` — restoration of the previous configuration was positively
  verified; or
- `RecoveryRequired` — the outcome cannot be proven, new mutations are blocked,
  and canonical reconciliation or local recovery is required.

Unknown state must never be reported as successful commit or rollback.

## Transaction journal and IPC failures

| Scenario | Required outcome | Status |
|---|---|---|
| Request fails before `router-applyd` receives it | Retry the identical request and transaction ID; do not advance canonical configuration without a verified response. | Automated |
| Helper cannot write the pre-operation intent journal | Reject before any privileged side effect. | Automated |
| Helper crashes or power fails after intent persistence but before completed-result persistence | The incomplete intent is durable; the same or a different mutation returns `RecoveryRequired` and does not repeat side effects. Only canonical `RECONCILE` may supersede it. | Automated model; process/power test required |
| Helper completes apply but IPC response is lost | Retry the identical ID so the helper returns its persisted idempotent result. | Automated |
| Helper completes an operation but cannot persist its result | Return `RecoveryRequired`; keep the in-memory result for same-process retries and block later mutation until reconciliation. | Automated |
| Helper restarts after result-journal write failure | Durable state is not sufficient to prove the outcome; canonical reconciliation is required before new mutations. | Guarded by code; process-kill test required |
| Runtime confirmation completes but its response is lost | Retry the same runtime-confirmation ID; do not repeat a logically new confirmation phase. | Automated |
| Final helper commit transport response is lost | Retry the same final-commit ID within that attempt so the helper replays its recorded result. | Automated |
| Final helper commit explicitly fails and the administrator retries | Use a fresh final-commit ID so a transient storage failure is not replayed forever from the idempotency cache. Do not repeat runtime confirmation or SQLite commit. | Automated |
| Both privileged attempts have an unknown outcome | Report `RecoveryRequired`, keep or reload SQLite canonical state, and rely on canonical reconciliation or console recovery. Never report `RolledBack`. | Automated |
| Helper returns success without verification | Reject the transaction, report `RecoveryRequired`, and do not update canonical state. | Automated |
| Helper explicitly reports rollback could not be verified | Propagate `RecoveryRequired`; do not present a successful rollback. | Automated |
| New request arrives while commit-confirm, rollback recovery, or an unresolved helper journal is pending | Reject the new transaction to prevent overlapping network states. | Automated |
| Duplicate request has the same ID and same payload | Return the persisted helper result without reapplying side effects. | Automated |
| Duplicate request ID has different payload | Reject as transaction-ID reuse. | Automated |
| Trailing or oversized IPC JSON is received | Reject before privileged execution. | Automated |
| RPC outcome contains contradictory flags such as success plus rollback or recovery | Reject as an invalid helper outcome and require recovery. | Automated |

## Privileged metadata failures

| Scenario | Required outcome | Status |
|---|---|---|
| `last-transaction.json` contains invalid JSON or an invalid structure | Fail closed with `RecoveryRequired`; do not start another mutation. | Automated |
| Transaction record has an invalid ID, fingerprint, timestamps, response ID, or contradictory response | Reject the record and require canonical reconciliation. | Automated |
| `pending-confirmation.json` is corrupt, invalid, or its hash does not match the complete configuration | Do not confirm or clear it; report `RecoveryRequired`. | Automated |
| Pending confirmation is absent when no change is pending | Return an ordinary side-effect-free rejection, not rollback or recovery. | Automated |
| `last-good.json` is unreadable or invalid | Refuse normal mutation and require canonical reconciliation. | Automated for corrupt/read error; target storage test required |
| Result journal is writable but later cannot be read | Block mutation and require reconciliation rather than treating it as a fresh device. | Automated |

## Commit-confirm and administrator reachability

| Scenario | Required outcome | Status |
|---|---|---|
| LAN address or CIDR changes | Apply provisionally, require explicit confirmation, and restore the old LAN after timeout before canonical commit. | Automated |
| Wi-Fi bridge topology changes | Require confirmation because the management path can move between physical LAN and bridge. | Automated |
| Management changes to `wireguard_only` before WireGuard is already enabled | Reject; WireGuard must be enabled and verified in a separate transaction first. | Automated |
| WireGuard private key changes while management is WireGuard-only | Apply provisionally and require confirmation through the new working tunnel. | Automated |
| WireGuard listen port changes while management is WireGuard-only | Apply provisionally and require confirmation. | Automated |
| WireGuard tunnel address, peer, or allowed route changes while management is WireGuard-only | Apply provisionally and require confirmation. | Automated |
| Ordinary WireGuard maintenance while LAN management remains available | Do not add an unnecessary confirmation gate. | Automated |
| Administrator confirms a disruptive change | Execute in order: verify candidate runtime, commit the exact revision to SQLite, verify runtime again, record helper `last-good`, then clear pending state. | Automated |
| SQLite commit fails after runtime confirmation | Keep pending state and old helper `last-good`; report `RecoveryRequired`. A later retry may repeat confirmation and canonical commit. | Automated |
| Helper `last-good` commit fails after SQLite commit | Candidate remains canonical, timeout rollback is disabled, pending state remains, and a later retry performs only the final helper commit with a fresh ID. | Automated |
| Confirmation deadline expires and rollback succeeds before SQLite commit | Mark `RolledBack`, retain old canonical state, and clear pending state. | Automated |
| First automatic rollback attempt fails | Keep candidate access and pending state, reject new changes, schedule another rollback, and use a fresh rollback ID. | Automated |
| Repeated rollback failure | Continue reporting the pending recovery condition; never silently clear it or claim rollback. | Guarded by code; Proxmox service-failure exercise required |
| Power fails while a change is awaiting confirmation before SQLite commit | Unconfirmed state must not become canonical; boot reconciliation must reapply the previous SQLite configuration before management starts. | Automated model; Proxmox power-cut required |
| Power fails after SQLite commit but before helper `last-good` acknowledgement | Candidate is canonical. Boot reconciliation must apply and verify the candidate, repair helper recovery metadata, and must not roll back to the older helper file. | Automated protocol model; Proxmox power-cut required |

## Persistence and boot failures

| Scenario | Required outcome | Status |
|---|---|---|
| SQLite commit fails after a non-disruptive helper apply | Attempt a privileged rollback; report `RolledBack` only if the rollback response is successful and verified, otherwise `RecoveryRequired`. | Automated |
| Power fails after helper apply but before SQLite commit | On boot, `routerd` loads the old durable revision and reconciles it before exposing management. | Automated model; Proxmox power-cut required |
| Power fails after SQLite commit | The committed revision is canonical and boot reconciliation must reproduce it. | Guarded by code; Proxmox power-cut required |
| Canonical reconcile encounters an incomplete or recovery helper journal | `RECONCILE` may supersede the journal only by applying the SQLite canonical configuration and verifying runtime. | Automated |
| Canonical reconcile fails | Keep management readiness failed or recovery-blocked; do not accept ordinary mutations. | Guarded by code; boot fault injection required |
| SQLite WAL recovery after abrupt host stop | Database integrity check must pass or router startup must fail closed. | Existing VM evidence; repeat on target Proxmox |
| SQLite file is corrupt | Refuse normal startup rather than initialize a fresh default router over damaged state. | Guarded by code; fault injection required |
| Disk becomes full or filesystem becomes read-only during journal, snapshot, canonical commit, or helper metadata commit | Abort at the exact stage, preserve console access, and never infer success from a missing durable marker. | Proxmox required |
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
| Power fails after pointer switch but before journal cleanup | Recovery completes the selected slot consistently. | Automated |
| Rollback is interrupted | Journal recovery selects a consistent old or new slot from the runtime pointer. | Automated |
| Update package is unsigned, altered, oversized, contains unsafe paths, symlinks, or hooks | Reject before staging/activation. | Automated |
| Candidate slot starts but router health is bad | Operator or recovery path must restore the previous verified slot. | Clean-Alpine automated; Proxmox reboot rehearsal required |
| Both update state and active pointer are corrupt | Fail closed and require local recovery; never guess a slot. | Automated state validation; recovery-media rehearsal required |

## Backup and restore

| Scenario | Required outcome | Status |
|---|---|---|
| Encrypted backup export is interrupted | No router configuration changes; incomplete export is discarded by the caller. | Guarded by code; client interruption test required |
| Backup password or authentication is wrong | Reject without exposing plaintext or changing canonical state. | Existing automated/API coverage |
| Backup JSON, checksum, schema, or encrypted envelope is corrupt | Reject before restore/apply. | Existing validation coverage; expand corpus over time |
| Restore applies a configuration that breaks management | Use the same validation, snapshot, durable intent, apply, verify, two-phase commit-confirm, and rollback path as an ordinary change. | Guarded by architecture; fresh-VM restore rehearsal required |
| Power fails during restore | Old durable configuration must remain canonical unless the restored revision was fully committed; boot reconciliation follows canonical state. | Proxmox required |
| Backup is restored into a fresh VM | Credentials, configuration, snapshots, and runtime services must be verified without importing transient sessions or unsafe host identity. | Proxmox required |

## Mandatory target-Proxmox sequence

The owner VM must still execute these tests in order, with pfSense available as
rollback:

1. Record VMID, exact commit, bridge/NIC mapping, snapshot, and backup.
2. Reboot guest and Proxmox repeatedly; verify stable WAN/LAN identity.
3. Kill `routerd`, kill `router-applyd`, and interrupt apply, runtime confirmation,
   SQLite commit, final helper commit, and canonical reconcile.
4. Corrupt or remove each helper metadata file on a disposable clone and verify
   fail-closed behavior.
5. Disconnect WAN during PPPoE establishment and after a stable session.
6. Test WireGuard from an unrelated external network, including reboot recovery.
7. Inject read-only filesystem, inode exhaustion, and low-disk conditions on
   disposable storage.
8. Force-stop the VM during apply, confirmation, update activation, rollback, and
   restore.
9. Run external IPv4/IPv6 scans and sustained throughput/latency tests.
10. Restore an encrypted backup into a new VM.
11. Run at least seven days while recording memory, disk, logs, reconnects, and
    errors.

Record results in a private dated report. A scenario is not considered physically
qualified until the report includes the exact command, expected result, observed
result, and recovery action.