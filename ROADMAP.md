# Roadmap

Minimal Router OS is an early-alpha community project. The roadmap is organized
around evidence and safety gates rather than promised dates.

Items marked complete indicate that an implementation exists and has passed the
listed development tests. They do not automatically imply production readiness
on every hardware platform.

## Current alpha baseline

Implemented in the current tree:

- [x] Alpine Linux packaging and OpenRC services
- [x] split `routerd` / `router-applyd` control plane
- [x] SQLite canonical configuration store and migrations
- [x] typed configuration validation
- [x] deterministic nftables, PPPoE, dnsmasq, WireGuard, Squid, QoS, DDNS, and
      Wi-Fi configuration paths
- [x] transaction state machine with snapshots and rollback
- [x] first-run wizard
- [x] HTTPS management and authenticated REST API
- [x] Argon2id authentication, sessions, CSRF, rate limiting, and optional TOTP
- [x] default-deny WAN policy and WireGuard-only remote-entry profile
- [x] DHCP/DNS and integrated global DNS blocklist
- [x] WireGuard split-tunnel client profiles
- [x] encrypted backup export and restore validation
- [x] redacted audit events and dashboard log view
- [x] React/Vite static dashboard
- [x] clean Alpine installation and wizard CI smoke test
- [x] Go race tests, vet, frontend lint/build, Dependabot, CodeQL, repository
      hygiene, and secret-scan configuration

## Public alpha release

Goal: publish a clean, honest, contribution-friendly repository without exposing
private development history, credentials, or historical repository metadata.

- [x] preserve and verify the complete private development history in private
      archives
- [x] remove tracked runtime configuration and snapshots from the reviewed tree
- [x] remove private AI handoff and session notes
- [x] rewrite README, architecture, comparison, security, contribution, support,
      development, release, and community documentation
- [x] add issue/PR templates, CodeQL, Dependabot, CODEOWNERS, repository hygiene,
      and current/full-history secret scanning
- [x] fix and test the TOTP-disable request-order regression
- [x] capture a real sanitized dashboard screenshot from the current clean build
- [x] add a local-only script for creating and scanning a one-commit candidate
- [ ] create the one-commit candidate locally and pass full-history Gitleaks
- [ ] rotate any credential that appeared in previous private history
- [ ] create a brand-new private repository with one branch, one commit, no tags,
      and no inherited pull requests, issues, workflow logs, or artifacts
- [ ] pass CI, private CodeQL analysis, and both secret scans in the new repository
- [ ] review repository settings and rendered documentation
- [ ] change visibility only as the final explicit owner action
- [ ] rerun CodeQL after publication and confirm GitHub Code Scanning upload

Exit criterion: the public repository contains only the reviewed one-commit
history, passes all checks, and accurately labels the project early alpha.

## Alpha 1 — reliable clean installation

Goal: a contributor can install a verified archive on clean Alpine Linux and
complete the wizard without manual source-code changes.

- [x] self-contained x86-64 distribution archive
- [x] architecture and payload validation in the installer
- [x] immediate kernel-module and sysctl activation
- [x] clean Alpine CI install
- [x] first-run HTTPS wizard smoke test
- [ ] validate aarch64 distribution in equivalent CI or hardware
- [ ] make interface selection robust across common predictable Linux names
- [ ] document recovery when the selected WAN/LAN interface is wrong
- [ ] publish checksummed alpha artifacts from tagged commits

Exit criterion: x86-64 and aarch64 clean-install evidence is reproducible and
new users have a documented rollback path.

## Alpha 2 — real home-router pilot

Goal: prove the minimum router workflow on real or dedicated test hardware.

- [ ] stable physical WAN/LAN NIC identification
- [ ] real ISP PPPoE connection and reconnect testing
- [ ] DHCP, DNS, NAT, and MTU validation with multiple client types
- [ ] reboot reconciliation after clean and failed shutdowns
- [ ] power-loss testing during idle, configuration apply, snapshot, and backup
- [ ] WireGuard provisioning and recovery from an unrelated external network
- [ ] external IPv4 and IPv6 scan from an unrelated network
- [ ] backup export, factory restore, and rollback rehearsal
- [ ] seven-day continuous pilot with bounded logs and disk usage

Exit criterion: the pilot survives repeated reboot, reconnect, invalid-change,
and recovery scenarios without requiring manual file repair.

## Alpha 3 — hardware and performance evidence

Goal: replace estimates with reproducible measurements.

- [ ] publish reference x86-64 and ARM64 hardware profiles
- [ ] measure idle and loaded CPU usage
- [ ] measure idle, setup, and sustained memory usage
- [ ] measure 1 GbE forwarding throughput and latency
- [ ] measure WireGuard throughput and CPU cost
- [ ] measure PPPoE and QoS performance
- [ ] test 2.5 GbE where supported
- [ ] investigate 10 GbE feasibility without promising support prematurely
- [ ] record thermal behavior and management responsiveness under load
- [ ] compare results using equivalent workloads rather than minimum-requirement
      tables

Exit criterion: every published performance claim links to hardware, commands,
configuration, and raw results.

## Beta — lifecycle and recovery

Goal: support safe long-term operation and upgrades.

- [ ] signed package or image release channel
- [ ] SBOM and provenance/attestation for release artifacts
- [ ] pre-update snapshot and verified rollback
- [ ] signed recovery media and tested console recovery
- [ ] bounded log rotation and disk-pressure behavior
- [ ] cross-version backup migration tests
- [ ] administrator password and TOTP console recovery procedure
- [ ] stronger `router-applyd` confinement using capabilities, namespaces,
      seccomp, or a documented alternative
- [ ] independent security review

Exit criterion: update, failed update, recovery, and restore are rehearsed on all
claimed platforms.

## Version 1 consideration

Version 1 will be considered only after the project has:

- a documented supported hardware and hypervisor matrix;
- reproducible installation and signed recovery artifacts;
- stable configuration migrations;
- measured performance and resource requirements;
- external network and security review evidence;
- a documented support and security-update policy;
- no unresolved critical or high-severity security findings;
- clear upgrade and rollback guarantees.

## Explicitly outside the current scope

The current release line does not target:

- pfSense feature parity;
- multi-WAN;
- high availability/CARP;
- IDS/IPS;
- captive portals;
- BGP or OSPF;
- OpenVPN or IPsec;
- arbitrary WAN port forwarding;
- Docker or Kubernetes on the router;
- a general third-party package platform;
- full AdGuard Home feature parity.

A proposal to add one of these items requires a product decision, threat review,
maintenance plan, and evidence that it belongs in a small home-router appliance.
