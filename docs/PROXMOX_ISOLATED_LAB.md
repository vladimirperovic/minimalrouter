# Minimal Router OS — isolated Proxmox torture lab

This document defines the destructive integration test matrix for a Minimal
Router build before it is allowed onto a real WAN. The lab must be isolated
from the production LAN except for the ISP simulator's ordinary upstream/NAT
connection. No MinimalRouter test NIC may be attached to a production bridge.

## Reference topology

```text
production LAN / Internet
        |
   ISP-LAB upstream NIC
        |
     ISP-LAB
  PPPoE AC + NAT + netem
        |
  vmbr-lab-wan (no physical port)
        |
     MR-TEST
        |
  vmbr-lab-lan (no physical port)
        |
    LAN-CLIENT

Optional internal bridges:
  vmbr-lab-office -> wg1 remote peer
  vmbr-lab-extra  -> isolated ExtraLAN service
```

Use local/private VM IDs and addresses. Do not commit production addresses,
credentials, MAC addresses, Proxmox inventory, DDNS names, or WireGuard keys.

## Global invariants

Every scenario, including a deliberately failed one, must leave or recover to
all applicable invariants below:

1. `inet minimalrouter` never becomes fail-open while IPv4 forwarding is on.
2. Before first setup commits, IPv4 forwarding is `0` and the setup LAN is the
   only management plane.
3. After setup, wired LAN management, DHCP, and local DNS do not depend on ISP,
   DDNS, Squid, Wi-Fi, ExtraLAN, QoS, or wg1 availability.
4. An unrelated configuration save does not depend on a live PPPoE session.
5. SQLite canonical configuration and helper `last-good.json` converge to the
   same revision before a transaction is reported `Committed`.
6. A transaction is never reported `Committed` while the final runtime is a
   hybrid of old and new LAN/WireGuard state.
7. An optional subsystem failure is reported degraded; it does not set global
   RecoveryRequired unless the core or only management path cannot be proven.
8. Removing/renaming a WireGuard interface leaves no old interface, old route,
   key/listener, or peer state in the kernel.
9. A failed A/B activation restores the previous current slot and restarts both
   `router-applyd` and `routerd` from the same slot.
10. A test failure must never affect the production router or production LAN.

For every failure capture, sanitize, and retain:

```sh
ip -br link
ip -4 addr
ip -4 route
nft list table inet minimalrouter
rc-service router-applyd status || true
rc-service routerd status || true
rc-service dnsmasq status || true
rc-service pppoe-wan status || true
wg show || true
router-update status || true
```

Also capture the API engine state and the current canonical revision. Never
publish secrets from generated configs or WireGuard dumps.

---

## A. Fresh install and first setup

### LAB-001 — pristine first boot

Setup:
- fresh Alpine 3.22 VM;
- empty `/var/lib/minimalrouter`;
- no helper last-good;
- no administrator credential.

Assertions:
- setup management address is reachable on the configured default LAN NIC;
- `net.ipv4.ip_forward = 0`;
- `inet minimalrouter` exists;
- dnsmasq is active for setup LAN;
- PPPoE, wg0, wg1, hostapd, inadyn, and Squid are not required;
- LAN client cannot traverse MR-TEST to ISP-LAB before setup commits.

### LAB-002 — first setup success

Complete the wizard with the ISP-LAB PPPoE credentials.

Assertions:
- the network config and administrator credential commit atomically;
- helper last-good appears only after canonical commit;
- forwarding becomes `1` only with the verified firewall active;
- DHCP client obtains a lease and resolves through MinimalRouter;
- PPPoE authenticates and Internet traffic NATs through ISP-LAB.

### LAB-003 — power loss after provisional helper apply

Hard-stop MR-TEST after provisional network activation but before SQLite/admin
commit. Use a fault hook or pause point; do not simulate this only with a clean
process exit.

After reboot:
- no administrator is considered configured;
- first-run runtime returns;
- forwarding is `0`;
- provisional pending state is discarded only after the setup runtime is safe;
- no candidate helper last-good survives.

### LAB-004 — power loss after SQLite/admin commit, before helper ack

Hard-stop after canonical setup commit but before helper canonical ack.

After reboot:
- routerd loads the committed canonical revision and credential;
- helper reconciliation converges to that revision;
- no setup-only state remains;
- management login works.

### LAB-005 — setup storage failure

Make SQLite commit fail (`ENOSPC` or controlled read-only storage) after the
provisional runtime was successfully applied.

Assertions:
- HTTP setup reports failure, not success;
- no administrator credential is half-committed;
- reboot deterministically returns to setup-only or the fully previous state;
- forwarding is never left enabled with an uncommitted setup.

---

## B. PPPoE / ISP fault isolation

### LAB-010 — ISP unavailable at boot

Stop `pppoe-server` before MR-TEST boots.

Assertions:
- LAN address, dashboard, DHCP, DNS cache/local records and firewall boot;
- no global RecoveryRequired solely due to PPPoE absence;
- when PPPoE server returns, normal reconnect restores WAN without reboot.

### LAB-011 — ISP disappears during unrelated local Save

With PPPoE down, change only a static DNS record or DHCP static lease.

Assertions:
- Save commits successfully;
- PPPoE remains degraded/disconnected;
- LAN management stays reachable;
- no rollback occurs solely because `ppp0` has no address/default route.

### LAB-012 — WAN credentials changed while ISP unavailable

Stop PPPoE server, then change PPPoE credentials.

Assertions:
- WAN-changing transaction requires WAN verification;
- candidate does not silently commit without a working PPPoE data plane;
- previous canonical configuration remains/restores safely;
- LAN stays reachable throughout.

### LAB-013 — PPPoE flap under traffic

Run continuous LAN-client TCP/UDP traffic. Repeatedly stop/start the PPPoE AC
and flap ISP-LAB's PPPoE-facing link.

Assertions:
- LAN services do not restart unnecessarily;
- pppd reconnects;
- no duplicate `router-applyd --reapply-qos` worker exists;
- QoS reapply, if enabled, is serialized through the long-running helper.

### LAB-014 — latency/loss/jitter

Use `tc netem` on ISP-LAB for representative faults:
- 200 ms delay + 50 ms jitter;
- 1%, 5%, 20%, 100% loss;
- reorder/duplicate packets.

Assertions:
- slow/broken WAN never becomes a core LAN recovery condition;
- after netem removal WAN recovers without config mutation.

### LAB-015 — PPPoE MTU/MSS

Exercise at least 1492, 1480, and 1400-byte effective paths.

Assertions:
- TCP MSS clamp avoids large-flow stalls;
- DNS, HTTPS, and WireGuard traffic behave consistently with configured MTU;
- no fragmentation-related dashboard false-positive is reported as core failure.

---

## C. Optional-service isolation

### LAB-020 — Squid dead during DHCP/DNS save

Enable Squid, then kill/disable its process. Change an unrelated static lease.

PASS:
- save succeeds;
- core remains healthy;
- Squid remains/report degraded;
- no RecoveryRequired solely for Squid.

### LAB-021 — DDNS dead during firewall/local save

Enable DDNS, stop inadyn or blackhole provider HTTPS, then perform an unrelated
local configuration change.

PASS:
- save succeeds;
- DDNS does not block canonical commit;
- no credential/token appears in errors or diagnostics.

### LAB-022 — Wi-Fi radio removed

With Wi-Fi configured, remove/detach the lab radio or make the interface
unavailable, then perform an unrelated wired-LAN save.

PASS:
- wired LAN remains management-safe;
- Wi-Fi is degraded;
- unrelated save is not rolled back because hostapd/radio is absent.

### LAB-023 — ExtraLAN NIC absent

Remove the ExtraLAN NIC before boot and during an unrelated save.

PASS:
- main LAN/firewall/DHCP/DNS remain active;
- ExtraLAN remains unavailable/degraded;
- no stale ExtraLAN route appears on another interface.

### LAB-024 — optional outage at boot reconcile

Repeat boot with each optional subsystem independently unavailable: wg1,
Wi-Fi, DDNS, Squid, ExtraLAN.

PASS:
- only the affected feature is degraded;
- core does not enter RecoveryRequired.

---

## D. WireGuard

### LAB-030 — cold boot with wg0 DNS

Boot with wg0 enabled and dnsmasq configured to listen on the WG address.

Assertions:
- dnsmasq starts even if interface creation timing changes;
- wg0 becomes active;
- DNS works from LAN and an authenticated wg0 peer;
- dnsmasq never widens listening to ExtraLAN/WAN.

### LAB-031 — enable/disable wg0

Enable wg0 from a working LAN, test DNS/HTTPS from the peer, then disable it.

Assertions:
- no dnsmasq startup ordering failure;
- disable removes the interface and associated routes;
- LAN management remains.

### LAB-032 — rename wg0

Change the configured server interface name in the lab.

PASS only if after commit:
- old name is absent from `ip link` and `wg show`;
- no route references old name;
- new name owns the expected address/key/listener/peers;
- failed new-interface creation restores old runtime completely.

### LAB-033 — rename wg1

Same as LAB-032 for outbound WireGuard.

### LAB-034 — wg1 outage during unrelated confirmation

Keep an already-configured office wg1 unreachable. Perform a connectivity
confirmation for a change that did not modify wg1.

PASS:
- unrelated confirmation does not require office handshake;
- wg1 remains degraded only.

### LAB-035 — changed wg1 cannot handshake

Change wg1 endpoint/key to a deliberately invalid peer.

PASS:
- the changed tunnel is not accepted as healthy;
- candidate is not silently committed;
- core LAN remains available.

### LAB-036 — handshake is not application reachability

Have wg1 handshake successfully while firewalling the remote test service.

Expected semantics:
- UI/API may report interface/handshake recent;
- it must not claim the remote application/site is proven reachable unless an
  explicit end-to-end health target is implemented.

---

## E. LAN management continuity

### LAB-040 — same-subnet gateway address change

Change only the router host address inside the same IPv4 prefix.

Assertions:
- transaction enters confirmation;
- old management path remains temporarily usable;
- confirmation is accepted only through the candidate address;
- final reconcile succeeds before transaction says `Committed`;
- old address disappears afterward.

### LAB-041 — failed final LAN reconcile

Inject helper transport/process failure during final LAN reconciliation after
canonical helper acknowledgement.

PASS:
- transaction does not falsely report `Committed`;
- RecoveryRequired explains that canonical config committed but runtime
  finalization remains incomplete;
- retry/reboot reconciliation converges to canonical config.

### LAB-042 — cross-subnet change rejected

Attempt e.g. `192.168.1.0/24 -> 192.168.2.0/24` through API/dashboard.

PASS:
- rejected before privileged helper apply;
- current LAN and DHCP leases are untouched;
- message directs operator to local recovery console.

---

## F. A/B update and rollback

### LAB-050 — full installer seeds baseline

Fresh full distribution install.

Assertions:
- `router-update status` reports a non-empty current synthetic bootstrap slot;
- that slot contains executable routerd/applyd and web bundle;
- first future A/B update therefore has a rollback target.

### LAB-051 — signed but incomplete payload

Create/sign a lab manifest missing one of: applyd, web index, PPP hook,
compatibility metadata, or an init file.

PASS:
- staging rejects it;
- current pointer does not move.

### LAB-052 — bootstrap ABI mismatch

Modify candidate `compatibility.json` and sign the lab release.

PASS:
- activation is rejected before stopping services or changing current pointer;
- message requires full distribution installer.

### LAB-053 — system integration mismatch

Use a signed candidate whose OpenRC/sysctl/PPP integration differs from the
installed files.

PASS:
- activation is rejected before daemon restart;
- no mixed old-system/new-binary runtime occurs.

### LAB-054 — normal activation

Activate a compatible signed slot.

Observe exact service order:
1. stop routerd;
2. restart router-applyd from new current slot;
3. start routerd from same slot;
4. verify both service states.

PASS:
- current and previous pointers are correct;
- API works after restart;
- no old/new RPC pair overlaps.

### LAB-055 — new helper fails to start

Use a disposable signed lab build whose helper exits on startup.

PASS:
- updater automatically restores previous slot;
- both services restart from previous slot;
- command reports failed activation, never false success.

### LAB-056 — power loss during slot journal

Hard-stop VM at multiple points of activate/rollback journal handling.

After every reboot/retry:
- `current`, `previous`, `state.json`, and operation journal converge;
- no pointer references an unavailable slot;
- one complete routerd/applyd pair is selected.

### LAB-057 — stale old PPP QoS hook

Deliberately reinstall an old hook that invokes `router-applyd --reapply-qos`.

PASS:
- invocation logs deprecation and exits without changing `tc`;
- running helper remains the sole writer and ordinary apply cannot race it.

---

## G. Storage / crash / corruption

### LAB-060 — ENOSPC on helper state

Fill the filesystem before last-good/pending/transaction persistence.

Assertions:
- no false successful commit;
- firewall/LAN remain in a known state or RecoveryRequired is explicit;
- after space is restored, reconcile can converge.

### LAB-061 — ENOSPC on SQLite canonical commit

Inject storage exhaustion after candidate apply but before canonical commit.

Assertions:
- no helper canonical last-good advances ahead of SQLite;
- rollback/setup recovery path remains deterministic.

### LAB-062 — corrupt pending confirmation

Corrupt only the helper pending-confirmation file.

PASS:
- ordinary mutation fails safe;
- canonical/local recovery can replace corrupt intent;
- no secret bytes are echoed in error text.

### LAB-063 — corrupt transaction journal

Same principle for last-transaction journal.

### LAB-064 — read-only runtime/config filesystem

Test separately for helper state, SQLite data, and generated config targets.

PASS:
- failures are bounded and explicit;
- no partial config is reported committed;
- firewall does not become fail-open.

---

## H. Security and segmentation

### LAB-070 — WAN external scan

From ISP-LAB/Internet simulator scan MR-TEST WAN/PPPoE address.

Expected:
- only explicitly supported WireGuard UDP listener when enabled;
- management HTTPS, DNS, DHCP, Squid, SSH and internal services not exposed;
- IPv6 absent unless a future release explicitly supports it.

### LAB-071 — Squid private-network pivot

From a proxy-authorized LAN client attempt CONNECT/HTTP to:
- main LAN addresses;
- router management;
- wg0/wg1 addresses;
- ExtraLAN;
- RFC1918/CGNAT ranges.

PASS:
- proxy cannot become a firewall bypass;
- Internet proxying still works where configured.

### LAB-072 — ExtraLAN isolation

Verify ExtraLAN can only participate in the exact configured service flow and
cannot initiate toward LAN, WAN, wg0, wg1 or router management.

### LAB-073 — trusted-management lockout

Try to remove the caller's source from trusted networks and try transition to
WireGuard-only management without proving the WireGuard path.

PASS:
- mutation is rejected or requires confirmation through the candidate path;
- no ordinary API request can silently remove the only proven management path.

---

## I. Soak / resource tests

### LAB-080 — 24-hour lab soak

Before a production pilot, run at least 24 hours with:
- continuous ping and DNS queries;
- periodic HTTP/HTTPS downloads;
- PPPoE reconnects;
- API polling;
- periodic local configuration saves that do not change WAN;
- wg0 traffic if configured.

Track:
- RSS of routerd/applyd/dnsmasq/pppd;
- file descriptor counts;
- SQLite WAL size;
- audit/log growth;
- conntrack usage;
- packet loss outside injected fault windows.

No unexplained growth or crash is acceptable.

### LAB-081 — reboot loop

Perform at least 10 guest reboot cycles and 3 Proxmox hard power cycles on a
known-good lab snapshot. Every cycle must converge automatically without
manual interface repair.

---

## Release gate

A build is eligible for the real pilot only when:

- all P0/P1 scenarios applicable to enabled features pass;
- normal CI, race tests, vet, frontend tests, CodeQL, secret scan, performance,
  and service-supervision checks are green;
- no open result shows a fail-open firewall, unknown canonical revision,
  management lockout, stale WireGuard kernel state, or mixed A/B daemon pair;
- failures of optional features are clearly separated from core health;
- a fresh snapshot/backup and known-good fallback router are available.

Record results in a dated sanitized report under `docs/` and reference scenario
IDs (`LAB-001`, `LAB-010`, etc.) so a regression maps back to one invariant.
