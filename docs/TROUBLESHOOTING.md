# Troubleshooting

Minimal Router OS is **Beta (v0.1.6)** networking software for controlled pilots.
Troubleshooting must not leave the active network exposed or unavailable. Restore
the known-good router first when a test causes an outage, then investigate on an
isolated network.

## First response

1. Keep or regain local console access.
2. Disconnect the candidate WAN if firewall state is uncertain.
3. Restore the established router when normal connectivity is required.
4. Record the exact Minimal Router commit and Alpine version.
5. Preserve only the minimum redacted evidence needed to reproduce the problem.

Never publish administrator credentials, PPPoE secrets, provider tokens,
WireGuard profiles or QR codes, backup archives, session cookies, CSRF tokens,
public IP addresses, real hostnames, MAC addresses, or device inventory.

## Collect a safe baseline

Run these commands from the local console:

```sh
cat /etc/alpine-release
uname -a
ip -brief link
ip -brief address
rc-service router-applyd status
rc-service routerd status
rc-update show | grep -E 'routerd|router-applyd'
df -h
free -m
```

Before sharing output, replace interface-adjacent identifying data with synthetic
labels and remove all addresses that are not documentation examples.

## Dashboard does not open

Check, in order:

- the client is attached to the isolated LAN, not the WAN bridge;
- the selected LAN interface is up;
- the client received an address and route;
- `routerd` is running;
- the browser is using HTTPS;
- the Host header or destination address matches the configured management path;
- another service is not already using the management port.

```sh
rc-service routerd status
ip -brief address
ss -lntp
```

Do not expose the dashboard on WAN as a workaround. Remote administration should
use WireGuard after local setup.

## `router-applyd` is not running

```sh
rc-service router-applyd status
rc-service router-applyd restart
```

If it stops again, inspect the local service log and file ownership. Do not run
`routerd` as root to bypass the helper boundary. That removes a core security
control and is not a supported diagnostic step.

## Configuration remains pending

Disruptive changes may enter an awaiting-confirmation state. Confirm the change
only after verifying management and expected connectivity from the intended LAN.
If confirmation is not possible, allow the transaction to roll back.

Do not repeatedly reapply the same change without understanding why verification
or confirmation failed.

## No DHCP lease on LAN

Check:

- the correct interface was assigned to LAN;
- no other DHCP server is active on the isolated segment;
- the interface has carrier and is up;
- `dnsmasq` is running;
- the configured subnet and pool are valid and non-overlapping.

```sh
ip -brief link
rc-service dnsmasq status
```

Avoid connecting the test LAN to a production segment with an existing DHCP
server.

## DNS fails but IP connectivity works

Check the generated DNS service state, upstream reachability, and whether the
global blocklist is affecting the requested name. Use a neutral test domain and
compare a direct address test with a DNS lookup.

Do not disable the firewall broadly to diagnose DNS. Change one bounded variable
at a time and preserve rollback.

## No outbound internet connectivity

Verify:

- WAN has the expected test-network address or PPPoE state;
- the default route is present;
- IPv4 forwarding is enabled;
- the generated `nftables` policy loaded successfully;
- NAT applies to the intended WAN interface;
- the upstream test router allows traffic from the candidate WAN.

```sh
ip route
sysctl net.ipv4.ip_forward
nft list ruleset
```

Treat firewall output as potentially sensitive. Redact public addresses,
interface descriptions, hostnames, and comments before sharing.

## PPPoE does not connect

Use test credentials where possible. Verify the physical or virtual WAN path,
VLAN requirements outside the current supported profile, MTU expectations, and
that credentials were entered through the application rather than stored in the
repository or shell history.

A first real owner-Proxmox PPPoE/Internet pilot has passed. The remaining target
validation is repeatability: explicit disconnect/reconnect, reboot recovery,
longer stability, and recovery together with WireGuard/DDNS. Keep the known-good
router ready for immediate restoration while those gates remain open.

## WireGuard peer cannot connect

Check:

- WireGuard was explicitly enabled;
- the peer has a unique address;
- the client uses the correct public key and endpoint;
- the external UDP path reaches the router;
- management is attempted through the tunnel, not directly from WAN;
- system time is correct.

Never attach a client profile or QR code to a public issue. Generate a new peer if
a private key or preshared key may have been exposed.

## High disk use

Check logs, snapshots, build artifacts, and temporary files. Do not delete the
canonical state database or current transaction markers while the services are
running.

```sh
df -h
du -x -h /var/lib/minimalrouter /var/log 2>/dev/null | sort -h | tail
```

Bounded log rotation and snapshot retention are implemented, but real full-disk,
inode-exhaustion and read-only-filesystem behavior remain target-appliance
validation gates. Take an encrypted backup before manual cleanup.

## Reboot or power-loss recovery problem

Disconnect WAN if policy state is uncertain and use the local console. Record:

- whether both services started;
- whether the last committed configuration loaded;
- whether a pending transaction was reconciled or rolled back;
- filesystem errors or read-only mounts;
- interface renaming or missing kernel modules.

Do not repeatedly power-cycle a system with filesystem errors. Restore from a
known-good snapshot or reinstall on a clean test disk.

## Reporting a bug

Use the GitHub bug-report template and follow [SUPPORT.md](../SUPPORT.md). Include:

- exact commit;
- Alpine version and architecture;
- generic hardware or VM layout;
- expected and actual behavior;
- minimal reproduction steps;
- redacted logs;
- whether console access and rollback were available.

Security vulnerabilities must be reported privately according to
[SECURITY.md](../SECURITY.md).
