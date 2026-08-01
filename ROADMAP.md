# Roadmap

This private roadmap is outcome-based and evidence-driven. Dates are intentionally
secondary to safe, reproducible results.

Current automated status: [`docs/CURRENT_VALIDATION.md`](docs/CURRENT_VALIDATION.md).
Existing Proxmox VM continuation: [`docs/PROXMOX_AI_HANDOFF.md`](docs/PROXMOX_AI_HANDOFF.md).

## Completed implementation and automated validation

- [x] Alpine packaging and OpenRC services
- [x] split unprivileged `routerd` and privileged `router-applyd`
- [x] SQLite canonical store, migrations, typed validation, and generators
- [x] snapshot/apply/verify/commit-or-rollback lifecycle
- [x] default-deny WAN policy and LAN-to-WAN NAT
- [x] PPPoE, DHCP, DNS, WireGuard, QoS, DDNS, Wi-Fi, and DNS Filter paths
- [x] HTTPS wizard, interface confirmation, Argon2id, sessions, CSRF, rate limits,
      TOTP, and local recovery
- [x] encrypted backup export and restore validation
- [x] crash-safe A/B activation and rollback with durable operation journal
- [x] signed manifests, checksums, SBOM, and provenance support
- [x] gateway-quality and PPPoE health monitoring with bounded history
- [x] bounded local storage retention, passive SQLite WAL maintenance, and bounded
      router service log rotation
- [x] 80% storage warning and 90% critical fail-closed durable-write pressure mode
      while the active forwarding plane remains untouched
- [x] central authenticated appliance health with Healthy / Warning / Degraded /
      Recovery required / Unknown aggregation and Overview banner
- [x] race/vet/vulnerability checks and repository hygiene
- [x] frontend lint, unit, TypeScript 6 build, dependency audit, and Playwright E2E
- [x] clean Alpine install, setup, update activation, and rollback CI
- [x] interrupted-update stress tests and two fuzz targets
- [x] security analysis, binary inspection, shell/workflow validation
- [x] ARM64 build and QEMU smoke test
- [x] isolated WAN-router-LAN DHCP/DNS/NAT/firewall/traffic laboratory
- [x] API/update performance and allocation baselines
- [x] core CI GitHub Actions and artifact upload v7
- [x] TypeScript 6.0.3 and Node type definitions 26.1.2
- [x] private AI handoff for the existing Proxmox VM

## Immediate milestone — continue the existing Proxmox VM

The VM already exists. Do not recreate or rewire it until read-only inventory
identifies the exact candidate and both bridge roles.

- [ ] identify Proxmox node, candidate VM, disk, and NIC/bridge purpose privately
- [ ] confirm isolated LAN and test/NAT WAN
- [ ] ensure pfSense rollback is independent and immediately available
- [ ] export encrypted application backup
- [ ] create a known-good Proxmox snapshot from a consistent state
- [ ] record installed commit, current/previous slots, Alpine/kernel, vCPU, RAM,
      disk, NIC model, and offloads
- [ ] update or reinstall from the exact current private commit using a verified
      archive and supported path
- [ ] verify `/api/v1/health` on the target VM
- [ ] verify storage pressure and log-rotation behavior on a disposable target
- [ ] create `docs/PROXMOX_TEST_REPORT_YYYY-MM-DD.md` with redacted evidence

Exit criterion: another operator can reproduce the inventory and safe boot without
relying on chat history or guessing topology.

## Proxmox functional pilot

- [ ] five graceful guest reboots
- [ ] repeated Proxmox graceful shutdown/start cycles
- [ ] stable WAN/LAN reconciliation after every boot
- [ ] DHCP lease and static-lease validation
- [ ] DNS forwarding and filter validation
- [ ] LAN-to-WAN NAT and stateful firewall validation
- [ ] HTTPS management reachable only through intended LAN/WireGuard path
- [ ] central appliance-health state matches injected/observed conditions
- [ ] unconfirmed LAN/firewall change automatically rolls back
- [ ] local recovery console remains usable
- [ ] signed update activation, reboot, verification, and explicit rollback
- [ ] encrypted backup restored into a fresh VM

Exit criterion: the candidate survives lifecycle and recovery tests without manual
file repair, ambiguous interfaces, or production-network impact.

## Proxmox performance and stability

- [ ] boot-to-forwarding-ready and management-ready
- [ ] idle and loaded CPU/RAM
- [ ] routing/NAT throughput and packets per second
- [ ] latency, jitter, retransmits, and packet loss without and under load
- [ ] management responsiveness during sustained traffic
- [ ] VirtIO multiqueue/offload comparison
- [ ] 1 GbE result
- [ ] 2.5 GbE result where available
- [ ] 10 GbE feasibility only after stable lower-speed tests
- [ ] PPPoE throughput, reconnect, MTU, and CPU cost
- [ ] WireGuard throughput and CPU cost
- [ ] QoS under load
- [ ] record bounded log, SQLite WAL, snapshot, gateway-history, and disk growth
- [ ] seven-day continuous pilot

Exit criterion: every result includes exact commit, environment, commands,
duration, raw summary, failures, and limitations.

## Recovery and destructive tests

Run only after backup, snapshot, console access, and pfSense rollback are proven.
Use a disposable clone or dedicated test disk where indicated.

- [x] automated interrupted update and rollback journal reconciliation
- [x] automated corrupt journal and hostile API fuzzing
- [x] automated storage-pressure threshold and durable-write classification tests
- [x] automated central health severity and recovery precedence tests
- [ ] service crash and restart behavior on target VM
- [ ] full disk and inode exhaustion on disposable target
- [ ] read-only filesystem on disposable target
- [ ] verify HTTP 507 pressure mode on a real full test filesystem while existing
      routing, DHCP/DNS, PPPoE and firewall state remains active
- [ ] corrupt state/snapshot recovery
- [ ] abrupt guest termination during controlled update stages
- [ ] abrupt Proxmox host/guest power-loss recovery
- [ ] fresh-VM backup restore

Exit criterion: all failures recover to a known-good state or trigger immediate,
documented pfSense restoration.

## Real network and production gates

- [ ] real ISP PPPoE during a maintenance window
- [ ] repeated PPPoE disconnect/reconnect and guest reboot
- [ ] WireGuard from an unrelated mobile/external network
- [ ] external IPv4 scan
- [ ] external IPv6 scan or documented fail-closed behavior
- [ ] owner-signed installation and recovery media
- [ ] supported Proxmox/NIC matrix
- [ ] independent focused security review
- [ ] stable cross-version migration and update policy
- [x] bounded logs and disk-pressure policy implemented
- [ ] target-host storage-pressure evidence
- [ ] support and security-update policy
- [ ] no unresolved critical or high-severity findings

Until these gates pass, pfSense remains the known-good production fallback.

## Explicitly deferred

IDS/IPS, captive portals, multi-WAN, HA, BGP, OSPF, Docker, Kubernetes,
enterprise QoS, OpenVPN, IPsec, arbitrary WAN port forwarding, and a general
third-party package platform remain outside the current target.
