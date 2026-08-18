# Roadmap

Minimal Router OS is currently **Beta (v0.1.4)**. The roadmap is organized around
evidence and release gates rather than dates.

Current evidence: [`docs/CURRENT_VALIDATION.md`](docs/CURRENT_VALIDATION.md).

## Implemented baseline

- [x] Alpine packaging and OpenRC services
- [x] `routerd` / `router-applyd` privilege separation
- [x] SQLite canonical config, validation and deterministic generation
- [x] transactional apply, confirmation, rollback and recovery
- [x] nftables firewall/NAT, PPPoE, DHCP/DNS and WireGuard
- [x] No-IP / Cloudflare DDNS, DNS Filter, QoS and optional Wi-Fi/Squid
- [x] secure sessions, CSRF, rate limits, TOTP and local recovery
- [x] encrypted backup and snapshots
- [x] crash-safe A/B update/rollback with signed-manifest support
- [x] bounded storage/logging and aggregate appliance health
- [x] gateway monitoring, live bandwidth and connected-device dashboard
- [x] Go/frontend/security/Alpine/QEMU/network-lab CI
- [x] AMD64 Golden Appliance ISO: CI-built Alpine + `linux-lts` + MinimalRouter
- [x] safe raw-image flasher with checksum verification and reinstall guard
- [x] VGA/noVNC install path plus persistent `ttyS0` 115200 recovery
- [x] blank-disk QEMU E2E: flash, reboot, firstboot, serial, SSH, firewall,
      router services and Dashboard verification
- [x] release pipeline capable of publishing the tested Golden ISO from the
      signed AMD64 release payload with checksum and GitHub attestation

## Proven on the first real Proxmox pilot

- [x] real PPPoE and Internet forwarding
- [x] recorded 570/327 Mbps throughput sample
- [x] 0% loss in the recorded 600-packet test
- [x] external phone WireGuard handshake
- [x] dashboard access through WireGuard
- [x] successful pfSense fallback
- [x] `linux-lts` identified as the working PPPoE kernel path

## Next pilot gates

- [ ] repeat the v0.1.4 Golden ISO on owner Proxmox from blank disk through real WAN cutover
- [ ] five repeated guest/host cold boots with stable WAN/LAN identity
- [ ] repeated real PPPoE disconnect/reconnect and reboot recovery
- [ ] MinimalRouter-managed No-IP update and later public-IP change
- [ ] WireGuard recovery after real PPPoE reconnect/reboot
- [ ] encrypted backup restore into a fresh VM
- [ ] external IPv4/IPv6 scan
- [ ] sustained throughput, packet rate, latency/loss and thermal measurements
- [ ] full-disk, inode, read-only-filesystem and abrupt-power tests
- [ ] seven-day continuous pilot with bounded resource growth

## Platform / media gates

- [ ] full installed-disk UEFI qualification; v0.1.4 E2E target is SeaBIOS/MBR
- [ ] owner-qualified recovery-media procedure
- [ ] supported Proxmox/NIC matrix
- [ ] ARM64 appliance-image/installer path if it becomes a supported target

## Production gates

- [ ] independent focused security review
- [ ] stable migration/update policy
- [ ] security-update/support policy
- [ ] no unresolved critical/high-severity findings

Until these pass, Minimal Router remains a controlled pilot rather than an
unattended pfSense/OpenWrt replacement.

## Deferred

Multi-WAN/HA, IDS/IPS, captive portal, BGP/OSPF, OpenVPN/IPsec, arbitrary WAN port
forwarding, container hosting and a general third-party package ecosystem are
outside the current small-router scope.
