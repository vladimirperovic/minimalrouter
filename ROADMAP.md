# Roadmap

Minimal Router OS is early alpha. The roadmap is organized around evidence and
release gates rather than dates.

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

## Proven on the first real Proxmox pilot

- [x] real PPPoE and Internet forwarding
- [x] recorded 570/327 Mbps throughput sample
- [x] 0% loss in the recorded 600-packet test
- [x] external phone WireGuard handshake
- [x] dashboard access through WireGuard
- [x] successful pfSense fallback
- [x] `linux-lts` identified as the working PPPoE kernel path

## Next pilot gates

- [ ] MinimalRouter-managed No-IP update and later public-IP change
- [ ] five repeated guest/host reboot cycles with stable WAN/LAN identity
- [ ] repeated PPPoE reconnect and reboot recovery
- [ ] WireGuard recovery after PPPoE reconnect/reboot
- [ ] encrypted backup restore into a fresh VM
- [ ] external IPv4/IPv6 scan
- [ ] sustained throughput, packet rate, latency/loss and thermal measurements
- [ ] full-disk, inode, read-only-filesystem and abrupt-power tests
- [ ] seven-day continuous pilot with bounded resource growth

## Production gates

- [ ] owner-qualified signed install/recovery media
- [ ] independent focused security review
- [ ] supported Proxmox/NIC matrix
- [ ] stable migration/update policy
- [ ] security-update/support policy
- [ ] no unresolved critical/high-severity findings

Until these pass, Minimal Router remains a controlled pilot rather than an
unattended pfSense/OpenWrt replacement.

## Deferred

Multi-WAN/HA, IDS/IPS, captive portal, BGP/OSPF, OpenVPN/IPsec, arbitrary WAN port
forwarding, container hosting and a general third-party package ecosystem are
outside the current small-router scope.
