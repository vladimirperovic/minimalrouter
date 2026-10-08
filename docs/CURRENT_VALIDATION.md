# Current validation status

This document is the short source of truth for what is proven today. Historical
reports remain in `docs/` for traceability. Automated CI evidence and real
owner-Proxmox/ISP evidence are deliberately kept separate.

## Current release line

**Minimal Router OS v0.2.0 — Beta / release candidate.**

The build tree (`VERSION`, `web/package.json`) targets v0.2.0. This is not a
publication claim. Exact-candidate CI, ISO and signed-release gates remain
required under [the release process](RELEASE_PROCESS.md).

Local evidence collected on Windows, 2026-10-08:

- Full `internal/dnsrisk`, `internal/accounting` and `internal/api` test suites
  passed, including new risk matching/lifecycle and collection-gap regressions.
- Linux cross-build and `go vet ./...` passed. Deterministic Linux builds of all
  three bootstrap programs match v0.1.9 byte-for-byte for both amd64 and arm64.
- Production dashboard build, lint and all 62 Vitest tests passed. The focused
  DNS/overview/insights browser suite passed 88 cases across desktop/mobile
  Chromium and WebKit. DNS cases cover Noema/Studio, light/dark, review/ignore/
  undo, filtering, pagination, deletion, disable, runtime labels and failure
  states. The OpenAPI YAML parses and all local schema references resolve.
- An opt-in live public-feed test downloaded/indexed all five Block List Project
  lists: adult 953,182; phishing 190,185; malware 2,655,237; fraud 256,183;
  gambling 278,854 accepted domains. Active indexes used about 110 MiB. These
  are source-compatibility observations on this workstation, not appliance
  performance guarantees or evidence that lists are complete.
- Native Windows cannot run the full Linux-only command test suite; Linux CI
  remains required. The existing Node precompression symlink test also requires
  symlink permission unavailable in this Windows session.

No real-appliance DNS risk monitoring or ISP evidence has been collected for
v0.2.0. Golden installer/boot behavior is unchanged, with qualification still
required on the exact candidate. The complete sidebar review and follow-up
scope are in [DASHBOARD_REVIEW_v0.2.0.md](DASHBOARD_REVIEW_v0.2.0.md).

## v0.1.9 line (previous release)

**Minimal Router OS v0.1.9 — Beta / controlled pilot.**

The v0.1.9 release build tree targeted v0.1.9. It is not recommended
as an unattended replacement for pfSense/OpenWrt.

v0.1.9 adds opt-in DNS activity statistics and read-only MCP insight tools. It
does not change the bootstrap tools that run outside the A/B slot:
`scripts/ci/bootstrap-compatibility.sh v0.1.8` builds `router-update`,
`router-recovery` and `router-setup` byte-identical to v0.1.8 for amd64 and
arm64, so a v0.1.8 appliance can take v0.1.9 as a dashboard web update. The
Golden flasher and installer scripts are unchanged from v0.1.8.

Evidence for this line, precisely:

- pull request #158 runs every required workflow, including **Build, flash and
  boot minimalrouter golden ISO** and the **WAN, router and LAN namespace
  laboratory**. That is CI evidence on a pull-request branch;
- unit and API tests cover the dnsmasq log parser and its memory and IPC
  bounds, fail-closed enabling of the dnsmasq drop-in, the SQLite store and its
  per-day/total bounds and retention, deletion on disable, storage-pressure
  shedding and the authenticated API;
- the owner appliance's `/etc/dnsmasq.conf` was read and contains
  `conf-dir=/etc/dnsmasq.d/,*.conf`, which the drop-in relies on. DNS activity
  recording has not yet been exercised on a real appliance.

No new owner-Proxmox or ISP evidence has been produced for v0.1.9.

## v0.1.8 line (previous release)

**Minimal Router OS v0.1.8 — Beta / controlled pilot.**

v0.1.8 changes the bootstrap tools that run outside the A/B slot
(`router-update`, `router-recovery`, `router-setup`), so an existing v0.1.7
appliance takes v0.1.8 once through the signed full distribution installer
rather than the dashboard updater (see [`WEB-UPDATE.md`](WEB-UPDATE.md)). The
Golden flasher and installer scripts are unchanged from v0.1.7.

Evidence for this line, precisely:

- pull request #156 ran every required workflow, including **Build, flash and
  boot minimalrouter golden ISO**, the **WAN, router and LAN namespace
  laboratory** and the ARM64 QEMU smoke test. That is CI evidence on a
  pull-request branch;
- a local live suite drove a real `routerd` through the production dashboard in
  macOS preview mode, where privileged Linux operations are simulated: first-run
  setup, DHCP, DNS, reservations, Dynamic DNS, firewall rules and presets, QoS,
  trusted networks with confirmation, WireGuard provisioning, tunnel port
  forwards, snapshots, encrypted backup export/restore, password change, 2FA,
  audit throttling, sign-in rate limiting, Wi-Fi, outbound-tunnel automatic
  rollback and pfSense migration. It is control-plane, API and persistence
  evidence, not data-plane evidence.

The signed release workflow rebuilds the exact tagged commit and repeats the
full E2E install before publication; the release claim rests on that run.

No new owner-Proxmox or ISP evidence has been produced for v0.1.8.

## v0.1.7 line (previous release)

**Minimal Router OS v0.1.7 — Beta / controlled pilot.**

v0.1.7 changes the Golden-image flasher's write verification, so the inherited
v0.1.5 ISO evidence below does not carry forward on its own and the blank-disk
E2E is required again for this line.

What has been re-run, precisely: the **Build, flash and boot minimalrouter
golden ISO** job has passed twice on the v0.1.7 line —

- [run 33965811766](https://github.com/vladimirperovic/minimalrouter/actions/runs/33965811766)
  on the branch carrying the flasher change itself (commit `1d2fb92`);
- [run 33996803693](https://github.com/vladimirperovic/minimalrouter/actions/runs/33996803693)
  on the branch that changes how the bootstrap binaries are built (commit
  `a9731bb`), which is the later of the two to touch what lands on the disk.

Both are CI evidence on pull-request branches. Neither is a claim about a
released artifact: the signed release workflow rebuilds the exact tagged commit
and repeats the full E2E install before publication, and the release claim
rests on that run rather than on either of these.

No new owner-Proxmox or ISP evidence has been produced for v0.1.7.

## Golden Appliance ISO evidence — v0.1.5 (inherited; see the flasher note above)

The v0.1.5 release candidate extends the original Golden Appliance path with
additional installed-appliance, supervision and installer-safety checks. The
signed release workflow is required to rebuild the exact tagged release and
repeat the full E2E install test before publication.

The automated Golden path proves:

- Alpine/MinimalRouter rootfs builds successfully;
- Golden image checksum and gzip integrity pass;
- production ISO boots and starts its flasher automatically;
- one clearly virtual QEMU disk is safely auto-selected;
- the Golden image is raw-copied to a blank 8 GiB VirtIO disk;
- the VM reboots into the installed `linux-lts` appliance;
- firstboot completes over `ttyS0`;
- installed serial root recovery login works;
- a real password-authenticated SSH login works through the test LAN;
- LAN `192.168.1.1/24` is present;
- nftables contains the expected trusted-LAN SSH accept rule;
- SSH TCP/22 is listening and enabled in OpenRC;
- firstboot completion marker and canonical SQLite state exist;
- running kernel matches `/lib/modules/$(uname -r)`;
- `routerd` reaches its readiness marker;
- Dashboard/API TCP/8443 is listening and reachable;
- Alpine v3.22 main/community repository configuration remains valid;
- an installed-disk cold boot succeeds without the ISO attached;
- firstboot does not re-enter after completion;
- a forced `routerd` crash is recovered by service supervision;
- a warm reboot returns the appliance to the same ready state;
- an existing MinimalRouter installation is refused rather than overwritten;
- an undersized 4 GiB target is rejected before destructive writes begin.

The test emits `FULL_ISO_INSTALL_OK` only after the required markers pass.

### Exact boundary of that evidence

The complete installed-disk E2E target is currently:

```text
AMD64 / x86-64
QEMU/KVM
SeaBIOS
MBR + ExtLinux Golden disk
8 GiB VirtIO Block target
2 VirtIO NICs
serial ttyS0-driven E2E
```

The installer ISO contains BIOS and UEFI boot metadata, but this does **not** yet
qualify the installed Golden disk for UEFI.

Automated QEMU installation does not prove real ISP PPPoE, physical NICs,
external Internet exposure, thermals, abrupt power-loss behavior or long-duration
operation.

## Real Proxmox evidence — 2026-08-01

A controlled owner-Proxmox pilot carried real Internet traffic through Minimal
Router for about 27 minutes and then successfully returned to pfSense.

| Test | Minimal Router | pfSense |
|---|---:|---:|
| Download | **570 Mbps** | 543 Mbps |
| Upload | **327 Mbps** | 318 Mbps |
| Packet loss (600 packets) | **0%** | **0%** |
| Ping 1.1.1.1 | 2.77 ms | **1.94 ms** |
| Ping 8.8.8.8 | 8.54 ms | **7.61 ms** |
| DNS (200 queries) | **12.65 ms, 200/200** | 13.00 ms, 200/200 |
| RAM after test | **172 MB** | — |

Additional results:

- real PPPoE and Internet forwarding: **PASS**;
- external phone WireGuard handshake: **PASS**;
- Dashboard access through WireGuard: **PASS**;
- pfSense operational fallback: **PASS**, about 93 seconds.

The tested Alpine `linux-virt` guest lacked the PPPoE module required by the real
WAN path. `linux-lts` provided it and the pilot succeeded. The Golden appliance
line therefore standardizes the AMD64 appliance on `linux-lts` rather than asking
the user to discover this during installation.

The successful external WireGuard pilot used a manually provisioned hostname on
the Proxmox side. MinimalRouter-managed No-IP and later public-IP propagation
still require a real-provider rerun.

## Isolated Proxmox lab evidence — 2026-08-06

A dedicated ISP-simulator → router → LAN-client lab validated:

- PPPoE CHAP negotiation: **PASS**;
- PPPoE PAP negotiation: **PASS**;
- private/CGNAT WAN address with safe router-local egress: **PASS after fix**;
- reboot with PPPoE session recovery and correct dnsmasq/WireGuard ordering: **PASS**.

## Torture-lab evidence — 2026-08-08

Scenarios 18–25 were run end to end:

| Scenario | Result |
|---|---|
| 18/19 — WireGuard recovery after endpoint blackhole | **PASS** |
| 20 — extra-LAN isolation | **PASS** |
| 21 — full reboot: LAN/DHCP/DNS/PPPoE/firewall recover | **PASS** |
| 22 — routerd+applyd crash: supervision/recovery | **PASS** |
| 23 — transaction fault-hook power-loss phases | **PASS** |
| 24 — signed update with verification + rollback | **PASS** |
| 25 — interrupted update mid-activate: cold boot to last-good | **PASS** |

Later scenario fixes and definitions remain tracked in the lab/failure documents;
a corrected test definition is not recorded as a real-lab PASS until rerun.

## v0.1.5 dashboard and operator validation

The release-candidate UI gate now covers both the production dashboard and the
GitHub Pages demo build. The two use the same production components and CSS;
demo mode differs only in mocked data and explicitly demo-only states.

Automated Playwright regression coverage includes:

- the Noema-inspired pushed mobile navigation interaction without copying Noema styling;
- fixed top-right mobile menu control, same-button close, Escape close and exposed-page close;
- scroll-position restoration and route-change reset behavior;
- all dashboard routes fitting the mobile viewport without page-level horizontal overflow;
- the production and Pages demo mobile build paths;
- equal 37 px desktop frame gutters;
- removal of the redundant `Gateway healthy` Overview ribbon chip while retaining the separate topbar health control;
- the horizontal Logs startup timeline on desktop and horizontally scrollable mobile presentation.

## Automated validation outside the ISO path

Repository workflows cover, among other checks:

- `go test -race`, `go vet`, vulnerability/security scans;
- frontend lint/unit/build/Playwright E2E;
- clean Alpine install and update/rollback lifecycle;
- transaction crash/recovery regression tests;
- CodeQL, secret scanning and shell/binary checks;
- ARM64 QEMU smoke tests;
- isolated WAN-router-LAN DHCP/DNS/NAT/firewall testing;
- storage-pressure and appliance-health regression tests;
- service-supervision regression testing;
- control-plane benchmarks.

These tests do not replace real ISP, NIC, thermal, power-loss or endurance
validation.

## Remaining gates before unattended production use

1. install the published v0.1.5 Golden ISO from blank disk on owner Proxmox and
   repeat the real WAN cutover;
2. repeat guest/host cold boots with stable WAN/LAN mapping;
3. repeated real PPPoE disconnect/reconnect and reboot recovery;
4. MinimalRouter-managed No-IP update and later public-IP change;
5. WireGuard recovery after real PPPoE reconnect/reboot;
6. timed device-pause expiry/resume on a real LAN client;
7. encrypted backup restore into a fresh VM;
8. external IPv4/IPv6 scanning;
9. destructive full-disk/inode/read-only-filesystem and abrupt-power tests;
10. sustained throughput, packet rate, latency/loss and thermal measurements;
11. installed-disk UEFI qualification if UEFI is to be supported;
12. at least seven days of stable unattended operation;
13. independent focused security review.

## Recommendation

v0.1.5 is suitable for a **controlled Proxmox pilot** with noVNC/serial recovery
and a known-good router ready for rollback. The Golden ISO is exercised as an
appliance image end-to-end and v0.1.5 broadens the automated cold-boot,
supervision and installer-safety evidence, but the real-WAN/endurance gates above
still prevent an unattended-production claim.

Detailed evidence and procedures:

- [`GOLDEN-IMAGE.md`](GOLDEN-IMAGE.md)
- [`ISO_INSTALLATION.md`](ISO_INSTALLATION.md)
- [`PROXMOX_TEST_REPORT_2026-08-01.md`](PROXMOX_TEST_REPORT_2026-08-01.md)
- [`LAB.md`](LAB.md)
- [`FAILURE_SCENARIOS.md`](FAILURE_SCENARIOS.md)
