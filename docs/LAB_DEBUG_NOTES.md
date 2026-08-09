# Lab debug findings — 2026-08-08 session

Hard-won findings from debugging the lab on the LXC (opencode 160). Read this
before touching the lab; it saves hours.

## Deploy / configure

1. **gx base64 vs raw**: qemu-guest-agent returns `out-data` as PLAIN TEXT on
   these VMs (not base64). `lab-deploy.sh` and `lab-mr-configure.sh` gx() must
   try base64 then fall back to raw. lib.sh already does this. If gx returns
   empty, check the decode first.

2. **lab-mr-configure.sh MR_API**: must be `https://192.168.1.1:8443`
   (MR-TEST on vmbr-lab-lan), NOT 10.77.0.1. The host reaches it via the
   pre-existing host route `192.168.1.1 dev vmbr-lab-lan`.

3. **Wizard apply (POST /api/v1/setup/apply)**:
   - `lan_ip_address` MUST equal the default (192.168.1.1). Changing it in the
     wizard returns 422 "Complete first-run setup on the default LAN address".
   - The apply runs ~25-60s and drops the HTTP connection; verify completion
     via applyd log + setup/status, not the HTTP response.

4. **Wizard verification "PPPoE interface has no assigned IPv4 address"**:
   the applyd waits 20s for ppp0 to get an IP. Root cause in practice was the
   ISP-LAB chap-secrets missing the lab user. Fix: ISP-LAB /etc/ppp/chap-secrets
   must contain: `"mr-test" * "<redacted>" 10.250.0.50`.

5. **ISP-LAB pppoe-server**: MUST run via the systemd unit created by
   `isp-provision.sh` (ExecStart `pppoe-server -I eth1 -T 60 -C lab-isp
   -S lab-isp`). A manually spawned pppoe-server holds eth1 and makes the
   systemd unit crash-loop ("Deactivated successfully" + restart counter).
   Fix: `killall pppoe-server; systemctl restart pppoe-server`.
   isp-provision.sh is idempotent — re-run it from
   /projekti/minimalrouter/scripts/lab/payloads/isp-provision.sh.

6. **routerd under OpenRC dies silently** unless the readiness marker
   `/run/minimalrouter/routerd.ready` exists. It gets deleted by applyd
   rollbacks. Fix: `touch /run/minimalrouter/routerd.ready; chown
   routerd:routerd; chmod 0600`. If routerd still won't listen, run it
   manually inside the VM with **setsid** (nohup dies with the qm guest exec
   session): `setsid sh -c '... /usr/bin/routerd > /tmp/r.log 2>&1 </dev/null &'`

7. **MR-TEST pristine image assigns 192.168.1.1 to BOTH eth0 and eth1** →
   API replies go out the wrong NIC and the host times out. Fix: add a host
   route on the router `ip route add 192.168.1.254/32 dev eth1` so replies to
   the Proxmox host leave via the lab bridge. The host itself already routes
   `192.168.1.1 dev vmbr-lab-lan`.

8. **build tarball**: /projekti/minimalrouter/build is gitignored/empty. The
   deploy needs `build/minimalrouter-linux-amd64.tar.gz` — copy it from
   /projekti/minimalrouterhome/build/. The Aug 6 home build lacks
   iputils-arping for offline install; install it first on MR-TEST
   (`apk add iputils-arping` — VM has repo access).

## Nightly loop (lab-nightly.timer every 4h: 00/04/08/12/16/20 UTC)

- nightly-lab.sh skips if lab-run.sh is already running (no duplicates).
- Skips if the user chatted in the last 30 min.
- The agent runs scenarios ONE BY ONE (resume: skip rc=0 results, retry rc=1),
  monitors /tmp/lab-stats.json, diagnoses+fixes failures, writes
  /root/nightly-report-<date>.md.
- Report file: /root/nightly-report-*.md; log: /root/nightly-lab.log.

## Stats / monitoring

- lab-stats.service: every 15s writes /tmp/lab-stats.json (CPU load, real host
  temp via thermal_zone1, RAM, disk, NVMe, VM states, opencode tokens, current
  scenario from /tmp/lab-current.json). /tmp is tmpfs — no disk wear.
- Temp watchdog: >95C kills lab-run.sh (hardware limit 105C; throttles ~100C).
- "📊 lab status" session is fed by plugin .opencode/plugins/lab-stats-status.ts
  (noReply messages, recycled every 20 msgs so the browser never chokes).
- Host cooling: cpu-cool.service = powersave governor + turbo enabled (idle
  ~800MHz, boosts on demand). Undervolt is not possible on the N150 (locked).
- Host disk: backups keep-last tuned (monthly guests + opencode nightly 3).
  Inode-filler scenario 32 was left running on VM 108 once — always check
  `top`/`pct exec` for stray lab-run/filler processes.

## Connectivity (phone/laptop from anywhere)

- pfSense (VM 106, the backup router at 192.168.1.1) port-forwards on its
  WireGuard interface (10.6.0.1): 4080→192.168.1.161:4080 (opencode),
  22→192.168.1.161:22 (SSH), 8006→192.168.1.2:8006 (Proxmox).
- Phone WG AllowedIPs: `10.6.0.0/24, 192.168.1.2/32, 192.168.1.161/32,
  10.0.10.0/24` — /32s avoid hotel-WiFi subnet conflicts (many hotels use
  192.168.1.0/24). On iOS the app shows no handshake row; check the tunnel
  key icon instead.
- LXC root password was reset to <redacted> (PermitRootLogin yes added).
- opencode server on LXC 160: `opencode web` on :4080, project
  /projekti/minimalrouter (WebUI needs "Add project" → /projekti → minimalrouter
  on first open). Server model: use `opencode/deepseek-v4-flash-free`
  (nvidia/z-ai/glm-5.2 returned AI_APICallError: Not Found).
- Mac/laptop to the lab: WireGuard up + route; if the laptop has no route, use
  `sudo route add 192.168.1.0/24 10.6.0.1`; or SSH tunnel
  `ssh -L 14080:192.168.1.161:4080` via BindAddress 10.6.0.3.

## 2026-08-08 late session — lab bring-up final fixes (all applied on LXC)
## 2026-08-08 late session — lab bring-up final fixes (all applied on LXC)

9. **Wizard+profile flow works now**: wizard (setup/apply) → PPPoE up → login →
   profile PUT (full config from GET, merged) → confirm. Committed rev 3.
10. **pppd 2.5.2** rejects rp-pppoe options `local-ip`/`remote-ip` in
    pppoe-server-options — server pppd died instantly ("Child pppd process
    terminated"). Remove those lines (IPs are passed positionally anyway).
11. **ISP chap-secrets needs `"mr-test" * "minimalrouter-lab-pppoe"
    10.250.0.50`** and pppoe-server must run as ONE instance (systemd unit from
    isp-provision.sh; a manual setsid instance crashes the unit loop).
12. **Every apply/confirm wipes the host route `192.168.1.254/32 dev eth1` on
    MR-TEST** — restore it via gx (inside the VM) after every API-affecting
    step, and in the API wait loop.
13. **Confirm needs the wg_client tunnel verified** (status check when
    wg_client.enabled) — until SIM-side WG is fully plumbed, the profile keeps
    wg_client disabled so the lab commits (wg1 scenarios to be revisited).
14. **MR has no NIC on vmbr-lab-office** (SIM wg1 endpoint 10.79.0.2) — hotplug
    `qm set 151 --net3 virtio=...,bridge=vmbr-lab-office`, then on the MR:
    `ip addr add 10.79.0.3/24 dev eth3; ip route add 10.79.0.0/24 dev eth3`.
    Also `ip route add 10.250.0.0/24 dev eth0` for wg0's endpoint (SIM eth0).
15. **WG endpoints must be REAL addresses** (not tunnel IPs): SIM wg0 endpoint =
    10.250.0.50:51820 (MR PPPoE), MR wg0 endpoint = 10.250.0.10:51820 (SIM
    eth0). With keepalive on the initiator side.
16. **Pending/confirm contract**: GET /transactions/pending →
    {"pending":true,"id":...,"state":"AwaitingConfirmation"} (compact JSON, no
    spaces); confirm = POST /transactions/{id}/confirm with X-CSRF-Token.
    Window is 90s; retry loop + route restore required.
17. **Scenario 01 baseline fails left**: firewall policy-drop check and
    LAN->internet + PPPoE-drop symptom — next debugging round (agent's job in
    the nightly loop).
