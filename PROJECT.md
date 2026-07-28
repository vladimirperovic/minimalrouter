# Minimal Router OS Vision

## Product statement

Minimal Router OS is an ultra-lightweight Linux appliance for home and small
office networks. It is not a pfSense replacement, a new firewall, or a new
networking stack. It reuses proven Linux components and focuses on simplicity,
stability, security, performance, and a clean user experience.

The final goal is a router that installs in minutes, requires almost no
networking knowledge, and just works with excellent UX and minimal overhead.

## Core principles

1. Less is better. Every feature must justify its existence.
2. Never reimplement something Linux already does well.
3. Safe changes are more important than clever changes.
4. Defaults must be secure and understandable.
5. The UI and API are two clients of the same validated configuration model.
6. Hypervisor-specific optimizations are optional and never required.

## Target platforms

These remain targets until the release compatibility matrix records a passing
install, boot, networking, rollback, and performance run for each platform.

- Bare metal
- Proxmox VE
- VMware
- Hyper-V
- KVM
- VirtualBox

## Technology stack

- Alpine Linux
- Go backend
- React + TypeScript + Vite frontend (static assets; no Node.js runtime)
- nftables
- pppd
- dnsmasq
- WireGuard
- SQLite

## Installation experience

Installation must take less than two minutes on supported hardware:

1. Select the target disk.
2. Install and reboot.
3. Open the first-run wizard.
4. Enter PPPoE credentials and an administrator password.
5. Review the proposed WAN interface.
6. Confirm WAN; the remaining interface becomes LAN.
7. Apply the default LAN address `192.168.1.1/24`.
8. Enable DHCP.
9. Create the initial snapshot.
10. Show a successful completion screen with
    `https://192.168.1.1`.

The wizard asks only for information that cannot be safely inferred.

## User interface

The dashboard shows only:

- Internet status
- Public IP address
- PPPoE status
- CPU, memory, disk, and uptime
- WAN and LAN traffic
- Connected devices
- WireGuard status
- Supported-service health and explicit unavailable states

There are no decorative or unnecessary graphs.

The primary pages are:

- Internet
- LAN and DHCP
- Static Leases
- Simple Firewall: allow/deny and LAN-to-WAN NAT; inbound WAN port forwards are
  forbidden
- WireGuard
- Cloudflare DDNS through the packaged and verified Alpine `inadyn` lifecycle
- Wi-Fi access point on a compatible AP-capable Linux radio
- Cloudflare Tunnel remains unavailable because WireGuard is the only remote
  entry path
- Backup and Restore
- Updates

## Safe configuration lifecycle

Every change creates an automatic snapshot. A candidate configuration is
validated before activation. After activation, health and connectivity checks
must pass. If validation, apply, or verification fails—or a disruptive change
is not confirmed—the system rolls back to the previous known-good snapshot.

Everything exposed in the web interface is also available through a versioned
REST API.

## Security principles

- HTTPS only
- Secure, HTTP-only cookies
- CSRF protection
- Argon2id password hashing
- Least privilege
- No WAN management access by default
- Existing Linux security mechanisms wherever possible
- No custom cryptography

See [SECURITY.md](SECURITY.md) for the threat model and release requirements.

## Performance targets

- Boot in less than 10 seconds on reference hardware
- Use 150–250 MB RAM during normal operation
- Support 1 GbE, 2.5 GbE, and 10 GbE where hardware permits
- Keep the control plane out of the packet-forwarding data path

## Version 1 exclusions

Version 1 explicitly excludes:

- IDS/IPS
- Captive portals
- Multi-WAN
- BGP
- OSPF
- Docker
- Kubernetes
- Enterprise QoS
- OpenVPN
- IPsec

Adding an excluded feature requires a new product decision, not only an
implementation pull request.

## Development rule

Never edit Linux service configurations directly. Every configuration change
must pass through:

`input -> validation -> config model -> config generator -> preflight -> snapshot -> apply -> verification -> commit or rollback`

Generated service files are disposable artifacts. The validated configuration
model is the source of truth.

## Version 1 success criteria

Version 1 is complete when a user can:

- Install on bare metal and at least one supported hypervisor.
- Complete the first-run wizard without networking expertise.
- Establish a PPPoE internet connection.
- Manage LAN, DHCP, static leases, simple firewall rules, and LAN-to-WAN NAT
  while WAN port forwarding remains forbidden.
- Configure WireGuard, Cloudflare DDNS, and a Wi-Fi access point on compatible
  hardware. Cloudflare Tunnel and DoH are visibly unavailable and rejected.
- Back up and restore the appliance safely. Automatic updates are a later
  release gate, not a current capability.
- Recover automatically from an invalid or connectivity-breaking change.

The release must also meet the security gates in `SECURITY.md` and performance
targets above on documented reference hardware.
