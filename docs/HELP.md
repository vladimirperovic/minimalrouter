# Minimal Router — Help & Operator Guide

This guide explains the appliance for people who do not need to know Linux networking. Minimal Router is designed around safe defaults: WAN is closed by default, configuration changes are validated and snapshotted, dangerous changes use timed confirmation/rollback, and firmware updates require a pinned signing key.

## Getting Started

There are two recommended deployment paths.

### Option A — ISO install

Use the ISO when you want the cleanest, most repeatable appliance deployment.

1. Create a new VM in Proxmox.
2. Attach two NICs: one LAN/management bridge and one WAN/ISP bridge.
3. Attach the Minimal Router ISO and boot from it.
4. Install to the VM disk and reboot.
5. Open the Proxmox console and verify which interface is WAN and which is LAN before enabling PPPoE.
6. Open the dashboard from the LAN side and complete the first-run wizard.
7. Enter ISP PPPoE credentials, verify DNS and Internet reachability, then run **Gateway Quality → Diagnose connection**.
8. Create a first known-good snapshot and encrypted backup.

### Option B — AI-assisted VM setup

An AI coding/ops agent can help create and validate the router when it actually has access to the relevant Git/Proxmox/terminal environment. It should never guess interface identity or silently modify an existing production router.

A safe workflow is:

1. Inspect Proxmox bridges, VM IDs and storage first.
2. Create a new isolated VM rather than modifying the working router.
3. Attach LAN and WAN NICs explicitly.
4. Attach the ISO or use the validated Alpine installation path.
5. Install and boot Minimal Router.
6. Verify management, PPPoE, DNS, Internet and WireGuard readiness.
7. Inspect **Logs → Startup Timeline** before moving production traffic.

Example request to an agent:

> Create a new isolated Minimal Router VM using the current signed build. Do not touch the existing production router. Verify WAN/LAN mapping, install, boot, run health checks, and report PPPoE/DNS/WireGuard readiness before I switch traffic.

The agent can only perform actions in systems it is genuinely connected to; otherwise it should provide commands/instructions and verification steps rather than pretending the deployment occurred.

### First boot checklist

- Dashboard is reachable only from the intended trusted management network.
- LAN address and DHCP range are correct.
- PPPoE is established when WAN is enabled.
- DNS resolves and HTTPS Internet reachability works.
- Router time is synchronized.
- `/etc/minimalrouter/firmware-signing.pub` exists before dashboard signed updates are relied on.
- WireGuard and Dynamic DNS are configured only when needed.
- A known-good snapshot and encrypted backup exist.

## Overview

**Overview** answers three questions: is the router healthy, is the Internet working, and which devices are present? CPU/RAM/traffic counters are operational indicators, not tuning targets. A short spike during boot, update, backup, DNS refresh, or diagnostics is normal.

## Gateway Quality and Internet recovery

Gateway Quality watches the WAN path over time. Latency is response delay; packet loss is the percentage of probes that did not return. The automatic recovery supervisor is deliberately conservative: it acts only after the PPPoE link itself remains down, not merely because one website, DNS server, or probe target is unavailable. **Diagnose connection** checks the chain in order: PPPoE, public reachability, DNS and HTTPS. Use it before changing settings.

## Network: WAN, LAN, DHCP and local DNS

**WAN / PPPoE** connects the router to the ISP. The username/password normally come from the ISP. **LAN** is the private network behind the router. **DHCP** automatically gives local devices addresses, gateway and DNS information. Static leases reserve a predictable IP for a device. Static DNS records give memorable local names to fixed services.

Changing the management LAN can disconnect the browser. Critical network changes are protected by Safe Apply: if the new configuration is not confirmed within the safety window, Minimal Router rolls back automatically.

## Firewall

The firewall is stateful and default-deny on WAN. Unsolicited Internet traffic is not accepted just because a service exists on the router. Remote administration should use WireGuard rather than exposing the dashboard. Port forwards and custom rules should be added only when the intended traffic path is understood.

## Security

Trusted Networks restrict which local sources may even reach the management surface. TOTP adds a rotating second factor to login. Security events show authentication and policy activity without request bodies or secrets. Port forwards are surfaced as deliberate exposure and recovery actions remain separated from ordinary configuration.

## QoS / Smart Queue Management

QoS controls bufferbloat: the large latency increase that can happen while a connection is fully loaded. CAKE is the recommended default. Set download/upload limits to roughly 90% of measured line speed, then compare latency under load. QoS does not make the ISP line faster; it trades a small amount of peak throughput for predictable latency and fairness.

## WireGuard server

WireGuard provides encrypted remote access to the router and selected local networks. Each remote device gets its own peer. Generated client private material is shown once; save the configuration or QR code when it is created. Disable or delete a peer when a device is lost or no longer trusted.

## Outbound WireGuard client

The outbound client tunnel connects this router to another site. The policy is intentionally asymmetric: the local router can initiate traffic toward the remote site, while the remote side is not given a generic path to initiate new connections into the home LAN.

## Dynamic DNS

Dynamic DNS keeps a hostname pointed at a changing public IP. This is useful for WireGuard endpoints when the ISP does not provide a static address. Minimal Router supports the configured providers without requiring the management UI to be exposed publicly.

## Squid authenticated forward proxy

Squid is a **non-caching authenticated forward proxy**. A useful high-isolation pattern is to place selected computers in the Restricted list so they do not have ordinary direct Internet forwarding, then configure the browser to use the Minimal Router Squid address, port, username and password. The browser's supported web traffic is then deliberately sent through the authenticated proxy while ordinary direct outbound connectivity from that restricted client remains blocked by router policy.

This can materially reduce the exposed network surface of a workstation: software that simply tries to open arbitrary direct Internet connections cannot use the normal routed path. It does **not** make a computer absolutely safe or replace endpoint security. Applications explicitly configured to use the proxy may still reach allowed destinations, browser content can still be malicious, and local/LAN policy remains relevant. Squid is configured in non-caching mode.

Typical setup:

1. Give the workstation a stable address.
2. Enable Squid and set a strong proxy username/password.
3. Add that workstation to the Restricted list.
4. Configure the browser HTTP/HTTPS proxy to the router LAN address and Squid port.
5. Verify direct Internet access fails while browsing through the authenticated proxy works.

## DNS Filter and Device Profiles

DNS Filter blocks configured domains and can apply scheduled service policies to groups of devices. Device Profiles are preferable to many one-off rules because a named policy can cover multiple static IPs and time windows. Schedules use router local time, so correct time synchronization matters.

## Wi-Fi access point

When supported wireless hardware is present, Minimal Router can run the local access point. Wi-Fi joins the protected LAN path rather than creating an unmanaged parallel network. Use a strong passphrase and a channel appropriate for the local radio environment.

## Traffic and accounting

Traffic views show how much data interfaces/devices move. They are for troubleshooting and capacity planning. Counters are not packet capture and do not record application content.

## Configuration snapshots, Smart Change Preview and Safe Apply

Before important changes Minimal Router creates restore information. **Smart Change Preview** explains what is about to change, expected risk/interruption and whether timed rollback is armed. Critical changes require confirmation after apply. If management is lost, the router automatically returns to the previous known configuration.

Manual snapshots are useful before planned experiments. Restoring a snapshot creates an undo point so recovery itself is reversible.

## Backup and restore

Encrypted backup is intended for disaster recovery or migration. A backup should be tested on a spare/fresh VM before it is treated as proven recovery media. Keep the backup password separately from the backup file. Review restore preview before applying it.

## Signed software updates

**Update now** checks the official release path, downloads the architecture-matching release, verifies the pinned firmware signing key, signed manifest, hashes, layout and compatibility, stages it in the inactive A/B slot, then activates it. If the new slot does not become healthy, rollback returns to the previous slot. **Upload signed build** accepts a locally produced signed payload plus its matching signed manifest; unsigned builds are rejected.

The trust anchor `/etc/minimalrouter/firmware-signing.pub` must already exist from a trusted bootstrap/full install. The dashboard must never learn to trust a key supplied by an untrusted update.

## Logs → Startup Timeline

Startup Timeline retains the **last five boots**. For the first **10 minutes** of each routerd start it records small resource samples and first-ready times for management, PPPoE, DNS, Internet reachability and WireGuard. Events use relative times such as `+18s`, making it easy to compare a healthy boot with a slow/failing one. No passwords, private keys or request bodies belong in this log.

## Logs → Audit events

Audit events record security and configuration metadata: logins, rejected access, configuration mutations, backups, restores and related actions. They are intentionally bounded and redacted. Export JSON when collecting troubleshooting evidence.

## Local Proxmox / physical recovery console

Run `router-recovery` as root on the local VM console with no arguments to open the interactive recovery menu. It can show interfaces, assign WAN, repair LAN/IP, restore last-known-good, restore a selected snapshot, guide authentication/factory recovery, restart router services, reboot, or open a temporary shell. The menu is not a daemon and consumes no CPU/RAM when it is not open.

CLI equivalents include `router-recovery interfaces`, `set-wan`, `set-lan`, `snapshots`, `restore-last-good`, `restore-snapshot`, `reset-auth` and `factory-reset`.

## Recovery order when something is wrong

Prefer the least destructive action: **Diagnose connection** → **Startup Timeline/audit log** → restart only the affected service → restore last-known-good/a snapshot → factory reset only as a final recovery path.

## Security model in plain language

The management plane is intentionally not a general root process. Privileged network application is separated behind a narrow helper. WAN management is not opened by default. Authentication, trusted management networks, CSRF/origin checks, rate limiting and optional TOTP protect the dashboard. Secrets are redacted from normal read APIs and diagnostics. Firmware is accepted only when it chains to the installed trust anchor. These controls reduce risk, but they do not remove the need to secure administrator credentials, signing keys, endpoints and the hypervisor itself.
