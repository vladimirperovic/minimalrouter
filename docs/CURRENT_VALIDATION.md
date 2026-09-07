# Current validation status

This document is the short source of truth for what is proven today. Historical
reports remain in `docs/` for traceability. Automated CI evidence and real
owner-Proxmox/ISP evidence are deliberately kept separate.

## Current release line

**Minimal Router OS v0.1.7 — Beta / controlled pilot.**

The build tree (`VERSION`, `web/package.json`) is v0.1.7. It is not recommended
as an unattended replacement for pfSense/OpenWrt.

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
