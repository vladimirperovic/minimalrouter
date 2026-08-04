# Proxmox VM108 pilot plan — fresh Minimal Router deployment

Sanitized deployment plan for a fresh Minimal Router pilot VM on the owner's
Proxmox node. Contains no live credentials, addresses, MAC addresses, bridge
names, VM IDs (other than the planned pilot identity), tokens or household
inventory. Read first:

- [`PROXMOX.md`](PROXMOX.md)
- [`PROXMOX_AI_HANDOFF.md`](PROXMOX_AI_HANDOFF.md)
- [`PROXMOX_TEST_REPORT_2026-08-01.md`](PROXMOX_TEST_REPORT_2026-08-01.md)
- [`INSTALLATION.md`](INSTALLATION.md)
- [`RECOVERY.md`](RECOVERY.md)

## Objective

Stand up **VM108** as a fresh, fully managed Minimal Router and validate the
hardened pilot (this repository, current `main`) before any production cutover.
The 2026-08-01 pilot proved PPPoE, forwarding, WireGuard and dashboard on the
existing VM; this plan re-proves the same on a clean VM with the full hardening
test matrix, then executes a reversible cutover and fallback.

## Deployment steps

1. **Create VM108** from the current validated Alpine image (Alpine stable,
   `linux-lts` kernel is the only validated PPPoE-capable kernel).
2. Assign two NICs with planned WAN/LAN roles; record the mapping privately.
   LAN bridge membership and MAC addresses stay out of the repository.
3. Install per [`INSTALLATION.md`](INSTALLATION.md); boot; run the guest
   baseline checks (see `PROXMOX_AI_HANDOFF.md` Phase 2), including:

   ```sh
   cat /etc/alpine-release
   uname -a
   modprobe pppoe
   ```

   `modprobe pppoe` must succeed, else stop and reinstall with `linux-lts`.
4. Configure via dashboard: LAN, DHCP/DNS, WAN (PPPoE), WireGuard, optional
   outbound tunnel (wg1), extra LANs, Squid, backups. Export and keep an
   encrypted backup before every subsequent change.
5. Take a known-good Proxmox snapshot of VM108 from consistent state.
6. Record the deployed commit privately (`router-update status`).

## Hardening test matrix (maps to the current hardening list)

Each row: evidence required before the next row starts. Stop at the first
unexplained failure and restore known-good state.

| # | Area | Test |
|---|------|------|
| 1 | Reboot repeatability | Five graceful guest reboots, two Proxmox shutdown/start cycles; WAN/LAN identity stable; `modprobe pppoe` succeeds; core services healthy each boot. |
| 2 | wg1 reboot restore (P0.3) | Enable wg1, reboot; `/run/minimalrouter/wg1.runtime.conf` present; `wg show wg1` up; health shows connected. Repeat with endpoint unreachable: core routing/management must stay up and health must report the tunnel degraded. |
| 3 | wg1 confirmation (P1.5) | Change wg1 endpoint/keys/address; apply must enter the 90-second confirmation window and auto-rollback when unconfirmed. |
| 4 | ExtraLAN isolation (P0.2/P1.6/P1.7) | Enable an extra LAN (e.g. media) with `RouterAddress`; verify from the segment: no LAN/WAN/wg0/wg1 egress, ICMP to router only, and the allowed LAN/WireGuard sources reach the configured service port; reboot and verify reconstruction. |
| 5 | Full-tunnel rejection (P1.8) | Dashboard must reject wg1 `0.0.0.0/0`, `/1` split tricks and subnets overlapping LAN/wg0/extra LANs. |
| 6 | DNS records (P2.12) | `.local` records rejected; `.home.arpa` records resolve; duplicates rejected. |
| 7 | Squid restricted IPs (P1.11) | Enable a restricted IP with an open direct flow; the flow must be cut immediately (deny precedes established accept); only Squid egress remains. |
| 8 | WOL (P2.17) | Invalid MAC returns 400; send failure returns 5xx instead of a silent 204. |
| 9 | Diagnostics privacy (P0.1) | Export diagnostics bundle; verify it contains no SSID, peer/device names, lease hostnames/MACs, DDNS hostname or topology names; canonical config unchanged after bundle generation. |
| 10 | Backup limits (P2.16) | Import a crafted backup with extreme Argon2 parameters; must be rejected without resource exhaustion. |
| 11 | Health semantics (P2.13) | wg1 interface up with no handshake and with stale handshake both report degraded, not healthy; recent handshake reports healthy. |
| 12 | Keepalive semantics (P2.14) | Set keepalive 0 in dashboard; generated runtime contains `PersistentKeepalive = 0`. |
| 13 | Update/rollback | Signed update stage/activate, reboot, rollback rehearsal to previous slot. |
| 14 | Backup restore | Encrypted backup restores into a fresh VM; secrets intact; validation passes. |
| 15 | Storage pressure | Fill thresholds; HTTP 507 behavior; read-only/full-disk failure modes only on disposable state. |
| 16 | External scan | IPv4 (and IPv6 if present) scan: only the WireGuard listener on WAN; default-deny forward/input confirmed. |
| 17 | Soak | Seven continuous days: stable memory, logs/WAL/history, services; no unexplained packet loss or crashes. |

## Cutover plan

1. Prove fallback first: document the current known-good router restore path
   (2026-08-01 pilot fell back to pfSense in ~93 seconds) and re-verify it with
   the current host state.
2. Take a fresh Proxmox snapshot of the current router and of VM108.
3. Arm an independent host-local failsafe before removing the known-good router
   from the WAN path.
4. Switch WAN/LAN bridge memberships to VM108; verify from the LAN side
   (DHCP/DNS/NAT, dashboard) and from an external WireGuard client.
5. On any failure of the matrix or cutover steps: restore the known-good router
   using the preserved snapshot/failsafe, then stop and report.

## Stop conditions

Identical to `PROXMOX_AI_HANDOFF.md` "Stop conditions", plus: any wg1 boot
that fails closed the core router (P0.3 regression), any extra LAN that can
reach a non-configured network, or any restricted-IP flow surviving an
established connection.

## Evidence

Record in `docs/PROXMOX_TEST_REPORT_YYYY-MM-DD.md` with sanitized labels only.
Never commit Proxmox inventory, credentials, MACs, addresses or live configs.
