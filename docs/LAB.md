# Minimal Router — Isolated Test Lab (2026-08)

Source of truth for the Proxmox lab used to validate Minimal Router against
*realistic ISP behavior*. **The lab is fully isolated from production** — no
lab VM shares an L2 segment with the production network. A second AI agent or
human can pick up this document and run the lab blind.

## Host

- Proxmox host: `root@192.168.1.2` (pve-manager 9.2.6), ~1.8 GB free RAM (keep
  lab VMs small).
- Production router: pfSense VM **106** (`192.168.1.1`). Never touch it.
- Production bridge `vmbr0` (`192.168.1.0/24`). Lab must not appear on it.

## Lab topology

```
             +------------------ Proxmox host -------------------+
             |  vmbr0 (production)  <-- host NAT (masquerade) <--+
             |                                                      |
             |  vmbr-lab-uplink  10.255.0.1/24  (host NAT out)      |
             +-----+------------------+----------------------------+
                   |                  |
          eth0 (uplink)        eth1 (ISP segment)
       10.255.0.2/24        10.250.0.1/24
   +------------------+   +----------------------+
   | 150 ISP-LAB      |   |                      |
   | Debian 13, agent |   |   pppoe-server       |
   +--------+---------+   |   (CHAP or PAP)      |
            |             +----------+-----------+
            | pppoe       vmbr-lab-wan
            |
   +--------+---------+   vmbr-lab-lan      vmbr-lab-extra (future)
   | 108 MR under     |      |                     |
   | test             |   +--+---------------+     |
   +------------------+   | 154 LAN-CLIENT2  |     |
                          | Debian 13, agent |     |
                          +------------------+     |
```

| VM | Role | net0 | net1 | net2 | Agent |
|---|---|---|---|---|---|
| **106** PFSense | production | vmbr1 | vmbr0 | — | no — **DO NOT TOUCH** |
| **108** MR-under-test | Minimal Router appliance | vmbr-lab-lan | vmbr-lab-wan | vmbr-lab-extra | yes (cloud-init removed from runlevel, boot ~150 s) |
| **150** ISP-LAB | ISP/CPE simulator | vmbr-lab-uplink | vmbr-lab-wan | — | yes (root via cloud-init) |
| **151** MR-TEST | legacy test router | — | — | — | stopped, conflicts with 108 — do not start |
| **152** LAN-CLIENT | legacy | — | — | — | disk is empty (0 B), unusable |
| **153** SIM-LAB | future simulators | vmbr-lab-wan | vmbr-lab-office | vmbr-lab-extra | running, no agent |
| **154** LAN-CLIENT2 | lab LAN client | vmbr-lab-lan | — | — | yes (full clone of 150) |

### Bridges

- `vmbr-lab-wan` — ISP WAN segment: ISP-LAB eth1 (10.250.0.1) <-> MR eth1 (PPPoE)
- `vmbr-lab-lan` — MR LAN segment: MR eth0 (192.168.1.1) <-> LAN-CLIENT2
- `vmbr-lab-extra`, `vmbr-lab-office` — unused segments for future tests
- `vmbr-lab-uplink` — isolated uplink: ISP-LAB eth0 (10.255.0.2) <-> host
  (10.255.0.1). Internet leaves via **host NAT** to vmbr0 (`nft` table
  `inet labnat`, masquerade `oifname "vmbr0"`), `net.ipv4.ip_forward=1`
  persisted in `/etc/sysctl.d/99-lab-forward.conf`. This is the ONLY path
  from lab to the internet, and it never puts a lab MAC on the production L2.

## Key facts per VM

### 108 (Minimal Router under test)

- LAN: eth0 `192.168.1.1/24`; WAN: eth1 (PPPoE only, no IP); extra: eth2.
- Services: `router-applyd` (privileged helper, unix socket
  `/run/minimalrouter/apply.sock`), `routerd` (API, `:8443`), dnsmasq on
  `127.0.0.1:53` + `192.168.1.1:53` + `10.6.0.1:53` (wg0), squid `3128`,
  wg0 `10.6.0.1/24`, chronyd.
- PPPoE: `/usr/sbin/pppd call wan nodetach` (`persist`, `noauth`,
  `mtu/mru 1492`). Client answers PAP or CHAP — whatever the peer demands.
- applyd generates both `/etc/ppp/chap-secrets` **and** `/etc/ppp/pap-secrets`
  (0600) from one credential bundle.
- Config state: `/var/lib/minimalrouter-applyd/last-good.json` (revision 30),
  runtime `/run/minimalrouter/`. Logs: `/var/log/router-applyd.{log,err}`,
  `/var/log/routerd.{log,err}`.
- **Known slow reboot** (~4 min to full function): boot ~150 s + startup
  reconciliation; `start_post` in `router-applyd.initd` waits up to 90 s for
  the apply.sock. `/etc/network/interfaces` still has `eth0 inet dhcp` (lab
  artifact) which costs a DHCP timeout at boot — MR applies its own static
  LAN config later. Root-causing the boot delay is an open item.

### 150 (ISP-LAB)

- eth0 = uplink 10.255.0.2/24, gw 10.255.0.1 (host NAT). eth1 = ISP segment
  10.250.0.1/24.
- **Kernel**: standard `linux-image-amd64` 6.12.100+deb13-amd64 (cloud kernel
  has no PPP — purged; `GRUB_DEFAULT="0>2"`).
- PPPoE server (started manually, not a service):
  `pppoe-server -I eth1 -L 10.250.0.1 -R 10.250.0.100 -N 100 -u /etc/ppp/chap-secrets -O /etc/ppp/pppoe-server-options -k`
- Secrets (`/etc/ppp/chap-secrets`, same content also used for PAP):
  `HWB6470EFA7@xdsl.isp.telekom.yu * iavjaaJAftZs85PS 10.250.0.2` (MR),
  `mruser * mrpass 10.250.0.3`.
- Auth mode is in `/etc/ppp/pppoe-server-options`: `require-chap` or
  `require-pap` (currently **require-pap**; switch by sed + restart:
  `kill $(pgrep pppoe-server)` then rerun the pppoe-server command above).
- NAT/forwarding (nftables): `oifname eth0 masquerade`, forward accept,
  `ip_forward=1` in `/etc/sysctl.d/99-forward.conf`.
- `qemu-guest-agent` works: `qm guest exec 150 -- ...`.

### 154 (LAN-CLIENT2)

- Full clone of 150; netplan forced to DHCP on eth0; gets `192.168.1.236`
  from MR dnsmasq (range 192.168.1.218-237). Agent works.
- All client tests run from here (ping/curl/DNS through MR NAT).

## Deploying a new build to 108 (current, signed-update path)

The disk-mount hot-swap below is obsolete. Builds are now deployed through the
product's own signed-update lifecycle (`firmware-sign` on the runner +
`router-update stage/activate` on 108), which is also exactly what scenarios
24/25 exercise:

```sh
# runner: build + sign (lab release key, see scripts/lab/lib.sh + scenario 24)
make build-linux-amd64
cp bin/routerd-linux-amd64 build/dist/minimalrouter-linux-amd64/bin/routerd-amd64
# ... same for router-applyd / router-update / router-recovery
cp /tmp/lab24sig/release.pub build/dist/minimalrouter-linux-amd64/firmware-signing.pub
tar czf build/minimalrouter-linux-amd64.tar.gz -C build/dist minimalrouter-linux-amd64
go run ./cmd/firmware-sign --dir build/dist/minimalrouter-linux-amd64 \
  --version 9.9.10 --commit <desc> --key /tmp/lab24sig/release.key \
  --output /tmp/lab24sig/lab-update-9.9.10.manifest.json

# runner: stage + activate via guest agent (scenario 24's stage_activate)
# IMPORTANT: 108's root umask is 077 — extraction leaves the payload 0700 and
# the staged slot binaries become unexecutable by the unprivileged routerd,
# which then silently falls back to bootstrap while root applyd runs the slot:
# mixed runtime. Always `chmod -R 0755 minimalrouter-linux-amd64` after unpack.
```

## Lab harness prerequisites (power-loss scenarios 23+)

- Fault hooks: `/etc/conf.d/router-applyd` and `/etc/conf.d/routerd` export
  `MINIMALROUTER_FAULT_HOOK_DIR=/run/minimalrouter-fault`. Services must be
  (re)started after the conf is added — env is read at process start.
- **doas must be setuid**: hook phases that fire inside routerd run as the
  unprivileged routerd user and call `doas /sbin/poweroff -f`
  (`permit nopass routerd as root cmd /sbin/poweroff` in `/etc/doas.conf`).
  Alpine's doas package installs without the setuid bit, so
  `chmod u+s /usr/bin/doas` is required or the hook fails with
  `doas: not installed setuid` and the VM never halts.
- Post-reboot lab restores: `rc-service router-applyd restart` +
  `rc-service routerd restart` after any service-env change.

## Torture-lab harness pitfalls (fixed 2026-08-08)

- Scenario stage checks that `grep -q staged` are false-positive on the error
  text `release slot is not staged` — match `verified and staged|already staged`.
- Deleting `slots/<current>` breaks the current pointer and every stage/activate
  refuses (`release slot is not staged`); recover with
  `rm -f /var/lib/minimalrouter-update/state.json /var/lib/minimalrouter-update/current /var/lib/minimalrouter-update/previous`.
- Routerd/applyd processes run via `slot-exec` — `ps | grep -E 'routerd|applyd'`
  matches nothing; the real binaries appear as
  `slots/<v>/bin/router*-amd64` or `bootstrap/bin/router*-amd64`.

## Validated in the lab (evidence, 2026-08-06)

## Running checks

```sh
# guest exec with simple quoting
ssh root@192.168.1.2 'qm guest exec 108 -- /bin/sh -c "ip -4 addr show ppp0 | head -2"'

# complex commands: base64-pipe inside the guest shell (avoids quote hell)
ssh root@192.168.1.2 "qm guest exec 150 -- /bin/sh -c \"echo '$B64' | base64 -d | /bin/sh\""
```

Health check sequence after any change:

```sh
qm guest exec 108 -- /bin/sh -c "ip -4 addr show ppp0 | head -2"                 # PPPoE up
qm guest exec 108 -- /bin/sh -c "ping -c 2 -W 3 1.1.1.1 2>&1 | tail -1"          # egress
qm guest exec 108 -- /bin/sh -c "nslookup example.com 127.0.0.1 | tail -2"       # DNS
qm guest exec 108 -- /bin/sh -c "nft list chain inet minimalrouter output | grep fib"
qm guest exec 154 -- /bin/sh -c "curl -s -m 10 -o /dev/null -w '%{http_code}' https://example.com"
```

Changing the ISP auth mode (test PAP vs CHAP without touching MR):

```sh
# PAP:
qm guest exec 150 -- /bin/sh -c "sed -i s/require-chap/require-pap/ /etc/ppp/pppoe-server-options; \
  kill \$(pgrep pppoe-server); sleep 1; \
  pppoe-server -I eth1 -L 10.250.0.1 -R 10.250.0.100 -N 100 -u /etc/ppp/chap-secrets \
  -O /etc/ppp/pppoe-server-options -k"
# then force MR reconnect: qm guest exec 108 -- /bin/sh -c "kill \$(pgrep pppd)"
# (pppd has persist; it reconnects within ~30 s)
```

## Validated in the lab (evidence, 2026-08-06)

| Scenario (ISP side) | Router behavior | Result |
|---|---|---|
| PPPoE with CHAP | auto-negotiated via `noauth` client | PASS |
| PPPoE with PAP | same client path, PAP secrets from same bundle | PASS |
| ISP assigns private/CGNAT WAN IP (10.250.0.2) | egress firewall must not block router's own traffic | PASS after fix |
| Session drop (ISP server killed) | pppd survives, reconnects on server return (~30-40 s) | PASS |
| Full reboot | everything back: nftables, dnsmasq, PPPoE, routerd, ping | PASS (~4 min) |
| LAN client e2e | DHCP lease, NAT, ping 1.1.1.1 ~2 ms, HTTPS 200 | PASS |

## Repository changes proven in the lab (all merged in working tree)

1. **`internal/services/nftables.go`** — output-chain anti-leak is fib-based,
   not a static private-range list:
   `oifname "ppp*" fib saddr type != local drop`
   IMPORTANT: `fib saddr . iif oif missing` is **rejected by the kernel in the
   output hook** ("Not supported") and previously made applyd fail closed on
   every boot. The forward chain keeps `fib saddr . iif oif` (valid there).
2. **`internal/services/pppd.go`** — `PPPoEConfigBundle` gained `PapSecrets`
   (same material as chap).
3. **`cmd/router-applyd/main.go`** — new `pap` artifact
   (`/etc/ppp/pap-secrets`, 0600) added to the artifacts map and to
   `restoreArtifacts`.
4. **`packaging/alpine/pppoe-wan.initd`** — preflight checkpath also for
   pap-secrets.
5. Earlier boot-reliability fixes already live on 108: startup reconcile
   order wg0 -> dnsmasq -> wg1, 8 s wg1 preflight + 15 s activate timeouts,
   `start_post` 360 attempts (see `startup_reconcile.go`, `main.go`,
   `router-applyd.initd`).

## Torture-lab evidence (2026-08-08, scenarios 18–30)

Full scenario suite lives in `scripts/lab/scenarios/` (run via `lab-run.sh`).
All runs on the isolated lab (VM 108 = router, 150 = ISP sim, 154 = LAN client).

| Scenario | What it proves | Result |
|---|---|---|
| 18 wg0 / 19 wg1 | tunnels recover after endpoint blackhole; keepalive keeps handshake fresh | PASS |
| 20 extraLAN isolation | 10.78.0.0/24 zone isolation; office web via wg1 | PASS |
| 21 router reboot | full cold restart: LAN/DHCP/DNS/PPPoE/firewall back, not hybrid | PASS |
| 22 service crash | kill routerd+applyd; initd respawn, session survives | PASS |
| 23 power loss ×5 | hard stop at each fault-hook phase (pre-privileged-apply … pre-canonical-ack); cold boot converges, policy-drop kept | PASS |
| 24 signed update+rollback | signed stage, activate 9.9.8→9.9.9, verify, rollback, converge | PASS |
| 25 incomplete update | `poweroff -f` mid-activate; cold boot to last-good, no brick | PASS |
| 26 ENOSPC | fill root fs — blocked: 7.5 G root, static 400 MB fill insufficient (needs dynamic fill) | FAIL (harness) |
| 27 interface rename | API change eth0→eth2 — **product rejects by design** ("live LAN interface changes unsupported") | FAIL (scenario outdated) |
| 28 LAN IP change | scenario patches ip_address only, cidr mismatch → validation rejects | FAIL (scenario bug) |
| 29 squid | **product bug**: squid responses to LAN clients dropped by output chain | FAIL (fixed, see below) |
| 30 DDNS unreachable provider | scenario expects save to fail; current build validates format only, save succeeds | FAIL (scenario outdated) |

### 2026-08-08 fixes applied to the repository

1. **Squid responses dropped (product bug).** The generated output chain had
   `meta skuid squid oifname "<lan>" drop` *before*
   `ct state established,related accept` — a deliberate isolation cut for
   Squid-*initiated* egress, but it also killed the responses to LAN clients
   that dial the proxy (feature unusable; request was logged TCP_MISS/200 while
   the client timed out). Fix: accept the reply direction first —
   `meta skuid squid oifname "<lan>" ct original ip daddr <lan-ip> accept`
   (nft syntax validated on the 108 kernel) then keep the zone deny. Unit test
   updated in `internal/services/scenario_security_test.go`. **Not yet
   deployed to 108** (signed payload 9.9.10 staged/activation pending).
2. **Slot staging is umask-sensitive (hardening note).** `copyRegularFile` uses
   `os.OpenFile(..., mode&0755)` and `os.MkdirAll(..., 0755)`, both of which are
   masked by the process umask; with root umask 077 every staged slot binary
   becomes 0700 and the unprivileged routerd falls back to bootstrap (mixed
   runtime). Alpine's default 022 hides this; production hardening should
   `Chmod` explicitly after create.
3. **Scenario harness:** mr_put gained an optional mode (default 0755); 24/25
   chmod the extracted payload and match positive stage markers.

### Lab recovery incident (2026-08-07/08, brick)

Scenario 25's interrupted activation left 108 in a boot loop: routerd fell back
to bootstrap (**new** code, fib-based anti-leak) while applyd ran the **old**
staged code (static private-range anti-leak) → nftables artifacts diverged →
trust-boundary reconcile failed closed. Two lab artifacts caused it: (a) a
stale dist whose binaries predated the fib fix, and (b) umask 077 making slot
binaries unexecutable for routerd. Rebuilt both routerd+applyd from current
main (identical code in bootstrap and slots), re-signed, re-staged — 24/25
then passed. Evidence captured in `scripts/lab/results/postmortem.txt`.

## Lab rules

- **Never touch pfSense (106) or vmbr0 devices.**
- **Never clone/pause/restart ISP-LAB (150) or reboot 108 while the owner is
  using the internet** — even with host NAT this is a shared single uplink;
  announce lab work first.
- 151 and 152 are dead (conflict / empty disk) — use 154 for client tests.
- If ISP-LAB loses uplink: check host NAT (`nft list table inet labnat`),
  `sysctl net.ipv4.ip_forward`, and `ping 10.255.0.1` from 150.
- If MR loses PPPoE after ISP-LAB restart: `qm guest exec 108 -- /bin/sh -c "kill \$(pgrep pppd)"`.
