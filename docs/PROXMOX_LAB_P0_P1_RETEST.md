# Minimal Router OS — mandatory P0/P1 lab retest

Run this short matrix on the new build **before** the longer
`PROXMOX_ISOLATED_LAB.md` suite. These scenarios target defects found during the
2026-08-06 deep audit. Use only isolated Proxmox lab bridges; never attach the
MR-TEST WAN/LAN/ExtraLAN interfaces to the production router LAN.

For every failed scenario capture, after removing secrets:

```sh
sysctl net.ipv4.ip_forward
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
cat /run/minimalrouter/routerd.ready 2>/dev/null || true
```

## P0-001 — helper startup failure is genuinely fail-closed

Induce a core `router-applyd` startup reconciliation failure after a previously
working configuration exists. A disposable lab build with a deliberate core
nft/dnsmasq activation fault is acceptable.

PASS only if:

- `net.ipv4.ip_forward = 0`;
- `routerd` is not serving management traffic;
- `inet minimalrouter` still exists and its INPUT and FORWARD base chains have
  `policy drop`;
- WAN/LAN forwarding is impossible;
- the failure does **not** delete the last firewall and expose kernel default
  ACCEPT behavior;
- local Proxmox console recovery remains possible.

## P0-002 — first-run power loss cannot create a router without an admin

On an empty install, hard-stop MR-TEST after provisional first-setup network
activation but before SQLite/admin commit. Boot it again.

PASS only if:

- no administrator credential is considered configured;
- setup-only LAN returns;
- forwarding remains `0`;
- PPPoE, wg0/wg1 and optional services are not required for setup recovery;
- provisional helper pending state cannot become canonical by itself.

## P1-001 — local `set-lan` cannot expose stale-canonical management

Start from a configured system, then from Proxmox console run recovery `set-lan`
to a different subnet. Reboot immediately so helper last-good and SQLite begin
from deliberately different revisions.

PASS only if:

- `routerd` does not report ready while helper/runtime still represents the old
  canonical LAN;
- `/run/minimalrouter/routerd.ready` stays empty until canonical reconciliation
  succeeds;
- after convergence the new LAN is active, DHCP/DNS work and login works;
- the old management address is not left as an unintended permanent address;
- if canonical reconcile cannot succeed, routerd refuses startup instead of
  serving a management policy for a runtime it never proved.

## P1-002 — recovery gateway inside `.100-.200` DHCP preference

From console execute recovery LAN changes using at least:

- `10.20.30.100/24`
- `10.20.30.150/24`
- `10.20.30.200/24`

PASS only if each accepted recovery config has a valid contiguous DHCP range
that excludes the router gateway. Repeat with a pre-existing static lease that
would overlap the newly selected recovery pool; the conflicting static lease
must be dropped rather than making emergency LAN recovery fail.

## P1-003 — A/B candidate changes bootstrap-stable code

Create a disposable **properly signed** lab candidate that differs only in one
of these components:

- `slot-exec`;
- `router-update-<arch>`;
- `router-recovery-<arch>`.

PASS only if activation is rejected before `current` moves and before either
router daemon is stopped. The message must require the full distribution
installer. The appliance must continue executing the old verified slot.

## P1-004 — A/B activation waits for real routerd readiness

Use a disposable compatible signed build in which routerd deliberately delays
its readiness publication after process start while remaining below the bounded
OpenRC startup window.

PASS only if:

- `rc-service routerd start` does not report success merely because
  `supervise-daemon` exists;
- updater does not declare activation successful before
  `/run/minimalrouter/routerd.ready` becomes non-empty;
- after the marker appears, both routerd and router-applyd are from the same
  current slot and management TLS is reachable.

Then repeat with a build that never publishes readiness. PASS only if activation
fails and automatically rolls back to the previous slot.

## P1-005 — full installer failure cannot advance A/B state

Record before install:

```sh
readlink /var/lib/minimalrouter-update/current || true
cat /var/lib/minimalrouter-update/state.json || true
```

In a disposable VM/kernel where a required module (especially `pppoe`) is
unavailable, run the full installer and let its kernel preflight fail.

PASS only if:

- failure occurs before root runtime replacement proceeds;
- `current` and `state.json` are unchanged;
- no new baseline becomes authoritative;
- after booting the correct kernel and rerunning the installer, baseline commit
  occurs only after kernel/sysctl/OpenRC checks succeed.

## P1-006 — optional outage cannot poison an unrelated Save

With a valid canonical config, independently break each optional subsystem:
Squid, DDNS, Wi-Fi radio, ExtraLAN NIC and existing wg1 remote peer. While each
is broken, change only a local DNS record or DHCP reservation.

PASS only if:

- canonical local Save succeeds;
- LAN management, DHCP, local DNS and firewall stay healthy;
- only the affected subsystem remains degraded;
- no global RecoveryRequired is caused solely by the optional outage.

## P1-007 — WAN outage cannot poison an unrelated local Save

Drop PPPoE entirely, confirm `ppp0` has no address/default route, then change a
local DNS record.

PASS only if the local Save commits. Next change PPPoE credentials while the ISP
is still absent; this second transaction must **not** silently commit because it
actually changes WAN and therefore requires WAN verification.

## P1-008 — WireGuard rename removes every stale kernel object

Rename wg0 and wg1 separately in the lab.

For each rename PASS only if:

- old interface is absent from `ip link` and `wg show`;
- no route references the old interface;
- new interface owns the intended address/routes/config;
- failure while creating the new interface restores the old verified runtime
  rather than leaving both or neither half-configured.

## P1-009 — management trust cannot self-lock the active admin

From a LAN client whose source IP is currently trusted:

1. attempt to save `trusted_networks` without that source;
2. attempt snapshot restore with a candidate excluding that source;
3. attempt backup/pfSense import apply excluding that source;
4. attempt WireGuard-only management before the candidate WG path is proven.

PASS only if every operation is rejected before lockout, or requires successful
confirmation through the candidate management path. The old path must not be
silently removed first.

## P1-010 — installer/update/reboot convergence

After all tests above, perform:

- 10 clean guest reboots;
- 3 Proxmox hard power cycles;
- one compatible signed A/B activate;
- one explicit rollback;
- one compatible activate again.

After every cycle verify:

- SQLite canonical revision and helper last-good converge;
- `routerd.ready` is non-empty only for a usable management process;
- forwarding is never on without the verified `minimalrouter` firewall;
- `current`, `previous`, `state.json` and operation journal agree;
- routerd and router-applyd always execute from the same selected slot.

## Gate

Do not make the lab build the production router until all ten scenarios pass.
After that, run the full `PROXMOX_ISOLATED_LAB.md` matrix and the soak test. A
single fail-open firewall, false A/B success, unknown canonical/runtime state or
management lockout is a release blocker.
