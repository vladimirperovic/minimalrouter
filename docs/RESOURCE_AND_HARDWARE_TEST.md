# Resource, Power-Loss, and Network Test — 2026-07-28

## Decision

The current build is suitable for an isolated, console-accessible bench pilot.
It is not yet approved to replace the production pfSense router.

The tests below provide real evidence for an ARM64 Alpine virtual machine,
persistent ext4 state, hard-power recovery, rollback, and an isolated Linux
network data path. They do not substitute for real dual-NIC hardware, an ISP
PPPoE session, an Internet-origin scan, or booting owner-signed recovery media.

## Measured resource use

Test environment: Alpine Linux 3.22.5 ARM64, Linux `6.12.94-0-virt`, Apple
Virtualization.framework, two virtual CPUs, and no swap.

| State | Whole-system RAM used | Available RAM | Application RSS |
|---|---:|---:|---:|
| Idle after reboot and rollback | 140 MiB | 722 MiB | `routerd` 83 MiB + `router-applyd` 15 MiB |
| After setup, login, and configuration work | 203 MiB | 659 MiB | `routerd` 138 MiB + `router-applyd` 13 MiB |

Measured installed payload after removing the unused storage packages:

| Item | Size |
|---|---:|
| Alpine packages reported by `apk` | 41.1 MiB in 89 packages |
| `routerd` | 10.6 MiB |
| `router-applyd` | 7.6 MiB |
| Static dashboard | 360 KiB |
| Initial persistent state | 240 KiB |
| Approximate payload total before kernel, boot files, logs, and snapshots | 60 MiB |

The current release archives are 7.7 MiB for ARM64 and 8.3 MiB for AMD64.
Those compressed download sizes are not installed-disk requirements.

The complete WireGuard integration test passed with 512 MiB RAM. Treat that as
the current tested minimum and provision 1 GiB for comfortable production
headroom. Use at least a 4 GiB disk for a bench system and retain the current
8 GiB production recommendation for logs, snapshots, upgrades, and recovery
space.

## Bash-free WireGuard runtime

Node.js is not installed or running on the router. It is used only to build the
static dashboard.

The router runtime is now Bash-free. Project-owned Alpine scripts use BusyBox
`ash` through `#!/bin/sh`, and the appliance installs only Alpine's
`wireguard-tools-wg` subpackage. Neither the Bash package nor the `wg-quick`
package/binary was present in the clean ARM64 VM.

`router-applyd` now uses fixed `wg setconf` and `ip` argument arrays. A separate
0600 runtime configuration excludes the `Address` directive and cannot contain
`wg-quick` shell hooks. The helper creates a temporary WireGuard interface for
preflight, applies the address, PPPoE-aware MTU and validated peer routes, then
verifies the active interface. Wider-than-tunnel peer routes are rejected.

An opt-in root Linux integration test created an isolated client namespace,
performed a real WireGuard handshake, sent five encrypted test packets, checked
the 1412-byte PPPoE-aware MTU and peer route, and confirmed cleanup. The test
passed in Alpine ARM64 with 512 MiB RAM.

Removing Bash and `wg-quick` reduced the measured Alpine set from 102 packages
and 48 MiB to 88 packages and 40 MiB. Removing three unused storage packages
reduced that build to 82 packages and 38 MiB. Adding the real Wi-Fi and
Cloudflare DDNS adapters brought the current clean VM to 89 packages and
43,147,851 bytes (41.1 MiB), or about 60 MiB with the application binaries and
dashboard. The DDNS and Wi-Fi daemons remain stopped when their features are
disabled, so package installation alone does not materially change steady
RAM. The 1 GiB recommendation provides full-workload and future-upgrade
headroom.

## Cloudflare DDNS and Wi-Fi adapter result

The current ARM64 VM installed Alpine's stable `hostapd` 2.11, `iw` 6.9, and
`inadyn` 2.12 packages. The real `inadyn` binary accepted the generated
Cloudflare provider configuration. A dashboard/API attempt to enable Wi-Fi in
the VM returned HTTP 422 because no `wlan0` radio existed; the active
configuration and LAN remained unchanged. A Cloudflare DDNS enable attempt
with WAN disabled also returned HTTP 422.

These are useful fail-closed and package/lifecycle checks, but they are not a
substitute for the remaining physical tests. A real AP-capable radio and
client are required to test association, WPA2/WPA3 negotiation, bridge
traffic, reboot, and rollback. A restricted Cloudflare token, existing DNS
record, and real WAN address are required to test an actual public record
update. Cloudflare Tunnel remains unavailable because WireGuard is the only
allowed remote-entry path.

The dashboard's connected-device count now comes from dnsmasq's bounded
runtime lease table. The lease file lives under `/run/minimalrouter`, so a
reboot clears it and the router does not create a separate history of client
hostnames, MAC addresses, or IP assignments.

## Persistent disk, reboot, and hard-power result

A fresh 4 GiB virtual block disk with an ext4 filesystem held the canonical
application and privileged helper state.

1. Setup and login succeeded.
2. A persistent marker was synced to the filesystem.
3. An unconfirmed LAN change from `192.168.1.1` to `192.168.2.1` entered the
   commit-confirm window.
4. The VM process was killed to simulate abrupt power loss.
5. The same disk was mounted after restart.
6. `e2fsck` recovered the journal and returned exit status 0.
7. The marker and its SHA-256 remained intact.
8. Login with the persisted account returned HTTP 200.
9. Boot reconciliation restored revision 2 and `192.168.1.1/24`; no
   transaction remained pending.

Result: persistent state, hard-power filesystem recovery, and automatic
rollback passed.

## Isolated throughput and WAN-policy result

An Alpine VM used separate LAN and WAN Linux network namespaces joined through
the router namespace.

| Check | Result |
|---|---:|
| In-memory virtual routing baseline | 143.24 Gbit/s |
| Stateful nftables plus NAT | 132.22 Gbit/s |
| CAKE shaped at 1 Gbit/s | 955.9 Mbit/s |
| Ping under the CAKE load | 0% loss; 0.377 ms average |
| Synthetic WAN IPv4 TCP ports 1–10000 | all filtered/no response |
| Synthetic WAN IPv6 reachability | fail-closed; no host response |
| Data-plane-only VM RAM at the end | 50 MiB used |

The two very large unshaped figures are memory/veth ceilings, not physical
router performance. The meaningful result is that the virtual path sustained
approximately line-rate 1 GbE while CAKE was configured. Physical NIC,
PPPoE, thermal, and long-duration performance remain unmeasured.

## Why the physical and external tests did not run

The Mac had one active physical network interface: Wi-Fi `en0`. It did not
have two connected Ethernet ports that could be safely isolated as WAN and
LAN. No ISP/test PPPoE credentials or independent Internet scan host were
available. Reconfiguring the active Wi-Fi would have risked the user's current
network without producing a valid two-port router test.

To complete these tests, use:

- two supported Ethernet adapters or a two-port target appliance;
- an isolated LAN switch/client and local console;
- ISP or laboratory PPPoE credentials entered locally during a maintenance
  window;
- an unrelated external IPv4/IPv6 host for the WAN scan;
- a rollback cable or the existing pfSense appliance ready to reconnect.

## Recovery ISO status

No signed recovery ISO was produced. The repository builds an Alpine overlay,
but it does not yet contain a completed `mkimage` profile, a signed APK
repository/release image, or an owner-controlled production signing identity.
Generating a disposable key and calling that a trusted recovery image would
provide misleading evidence.

The release gate is:

1. create and review the bootable Alpine image profile;
2. build the package repository and ISO in a controlled build environment;
3. sign the manifest/image with an owner-controlled offline key;
4. verify it using a separately distributed public key;
5. boot it on the target hardware and rehearse restore and rollback.

## Comparison boundary

OpenWrt officially warns that 64 MiB RAM is only a minimum and 128 MiB is
preferable, while more than 32 MiB flash is recommended for modern use.
pfSense officially requires at least 1 GiB RAM and 8 GB disk.

Minimal Router's measured 140–203 MiB whole-system RAM and roughly 60 MiB
initial payload place it between the two: heavier than a minimal OpenWrt
device, but much smaller than pfSense's minimum provision. This comparison is
about footprint, not security maturity or feature parity. OpenWrt and pfSense
have much broader deployment history, hardware coverage, upgrade/recovery
experience, and independent scrutiny.

Sources:

- [OpenWrt supported devices and resource guidance](https://openwrt.org/supported_devices)
- [OpenWrt 8/64 device warning](https://openwrt.org/supported_devices/864_warning)
- [pfSense minimum hardware requirements](https://docs.netgate.com/pfsense/en/latest/hardware/minimum-requirements.html)
- [pfSense hardware sizing guidance](https://docs.netgate.com/pfsense/en/latest/hardware/size.html)

## Next optimization opportunities

These should be handled in this order and measured after each change:

| Priority | Candidate | Likely benefit | Safety condition |
|---:|---|---|---|
| 1 | Replace the Alpine `ppp` meta-package with only the daemon and PPPoE plugin | Removes unused ATM, L2TP, RADIUS, Winbind, password-fd and minconn packages | Real PPPoE connect/reconnect and reboot test must pass |
| 2 | Make Squid an explicit optional image profile | Largest remaining optional userspace service and libraries; saves disk and avoids a dormant feature surface | Dashboard/helper must fail closed when the proxy package is absent |
| 3 | Profile `routerd` heap and SQLite allocations under a long dashboard/API workload | Best path to reducing the measured 83–138 MiB `routerd` RSS | Optimize from profiles, then rerun race, rollback and load tests |
| 4 | Add bounded log rotation and snapshot retention | Prevents long-term disk growth | Preserve enough audit and rollback history for recovery |
| 5 | Build hardware-specific kernel/module profiles | Can substantially reduce boot-image disk usage | Keep a generic recovery image and test every supported NIC/storage driver |

Do not merge `routerd` and `router-applyd` to save one process: that would
remove the most important privilege boundary. Do not use UPX or remove recovery
tools from the only image merely to reduce the advertised archive size.
