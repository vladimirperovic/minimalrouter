# Changelog

All notable user-visible changes are documented here. The project intends to use
semantic versioning when the first stable release is published. During beta,
compatibility may still change between releases.

## [Unreleased]

Next development version: **v0.1.8**.

## [v0.1.7] — 2026-09-06

### Highlights

- The dashboard tells you when a new release exists and installs it for you.
- One appearance, rebuilt: **Studio**, and the four others are gone.
- Eleven findings from the 2026-09-05 technical review are repaired, including
  two that could lose a committed configuration or accept a revoked password.

### Added

- **Updating from the dashboard.** A *New version* button appears at the foot
  of the sidebar when a newer signed release is published. It states what it
  found, what it will do and what it cannot do; the install runs behind a
  confirmation, into the inactive slot, and is verified against the release's
  Ed25519 manifest before anything is switched over. An update is offered only
  when both the archive and its signed manifest are present, so the button can
  never lead to a download that cannot be verified.
- **Bootstrap binaries are byte-identical between builds of the same source.**
  `router-update` and `router-recovery` embedded the commit hash and build
  time, so two builds of one tree produced different bytes and an A/B slot
  update could not prove it was installing the same bootstrap it was running.
  They are now built without VCS stamping, and a CI job holds the property
  against a recorded baseline.
- **The dashboard has one look.** Studio — warm paper ground, bronze accent,
  serif display type — is no longer one appearance among five; it is the
  dashboard's design, rebuilt underneath as six small stylesheets: tokens, the
  palette, typography, surfaces, controls and the Overview. The display face is
  a system serif stack rather than a webfont, because the dashboard is served
  from the appliance and is routinely opened while the WAN is down.

### Changed

- **Overview reads as one page rather than three stacked boxes.** The status
  card was a card holding a card holding tiles, all at the same weight. It is
  now a single card carrying one grid: the verdict, the three readings checked
  next, three groups of equal weight (Connection, Load, Trust & access) and the
  appliance identity, all on the same columns.
- **Text meets the contrast it is required to meet.** Section copy was measured
  at 1.65:1 against the page ground — a third of the 4.5:1 that normal text
  requires — and set two steps below body size; the small-caps labels sat at
  4.18:1. Both are corrected and the copy is at the body step.
- **The dashboard stopped rewriting its own markup after rendering.** A
  presentation layer patched the DOM after React drew it, which is a race with
  every re-render. The patches it carried are in the components.

### Removed

- The four appearances that were not chosen — Console, Atelier, Topology and
  the previous default — along with the topbar control that selected them.
- The standalone LAN & DHCP design gallery published at `/design/` on the
  GitHub Pages site. It existed to compare candidate appearances against each
  other; the comparison is over, and a page still offering them would advertise
  a control the dashboard no longer has.

### Fixed

- **Recovery no longer reverts a committed configuration.** When the canonical
  SQLite commit succeeded but the privileged helper's last-good acknowledgement
  failed, the engine kept the pre-transaction configuration in memory while the
  database and the running system held the new one. The recovery reconcile that
  the failure triggers then rebuilt its request from the stale copy and pushed
  the previous settings back onto a correctly applied revision.
- **Traffic accounting counts every counter generation.** Each apply recreates
  the nftables table, restarting the kernel counters. A reset was detected only
  when the new counter was still below the last reading, so a counter that had
  already climbed past it silently dropped a whole generation of traffic (100
  recorded plus 200 after the reset totalled 200 instead of 300). Cursors now
  carry the configuration revision they were sampled in.
- **Disabling accounting deletes history even across a restart.** The decision
  lived in a process flag that starts false, so disabling and then restarting
  routerd before the next collection tick left the history on disk forever. The
  outcome is now recorded durably and retried until the deletion succeeds, and
  the API stops serving history while accounting is off.
- **Automatic WAN recovery ignores stale and unmeasurable samples.** The
  gateway monitor keeps its last summary when a collection round fails, so an
  hour-old offline reading could still satisfy the "link has been down for
  three minutes" condition. Recovery now requires a fresh, available sample and
  treats `unknown` as unknown rather than as an outage.
- **The speed test rejects failed measurements instead of turning them into QoS
  suggestions.** Neither direction checked the HTTP status, and the upload rate
  divided the *planned* byte count by the elapsed time, so a server answering
  before reading the body produced absurd results (an HTTP 503 measured tens of
  millions of Mbps). Both directions now require a successful status and a
  sufficiently complete sample, and measure the bytes that actually moved.
- **The Golden-image flasher verifies the whole pipeline.** `gzip -dc | dd`
  reports only `dd`'s exit status, so a decompressor that failed after partial
  output finished with "Golden image copied successfully" over a truncated
  disk. Both stages are now checked, and the written byte count is compared
  against the uncompressed size recorded at build time
  (`/minimalrouter/golden.img.bytes` in the ISO).
- **The privileged helper no longer loses the reply to a long operation.** The
  response write deadline started when the connection was accepted, so a
  legitimate multi-second apply could consume it before the first response byte
  and force a transport replay. It is armed immediately before the reply, and a
  failed delivery is logged instead of discarded.
- **The MCP server survives router errors instead of misreporting them.** API
  responses are status-checked, so an authentication error body can no longer be
  decoded as configuration; a missing configuration section is refused instead
  of panicking the process; an expired session is re-authenticated once (never
  by replaying a single-use TOTP code); a change awaiting confirmation is
  reported as awaiting confirmation rather than as applied; and a malformed
  stdin stream ends the process instead of spinning forever in a decoder that
  cannot resynchronize.
- **TLS certificate validity is re-checked on cache hits.** The cached
  certificate was keyed on configuration alone, so a long-running appliance
  could keep serving an expired — or, after a clock correction, not-yet-valid —
  certificate. The parsed validity window is now checked per handshake without
  re-reading PEM from disk.

### Security

- **Session issuance is bound atomically to the credential that was verified.**
  A login that read the password hash, was preempted by a password change, and
  then created its session picked up the *new* authentication generation, so a
  session proving only the revoked password passed validation. The hash and
  generation are now read as one snapshot and the session row is inserted only
  while that generation is still current.
- **Concurrent Argon2id derivations are admission-controlled.** Every
  derivation reserves 64 MiB against a 128 MiB process budget, and the login
  rate limits bound attempts per minute, not attempts in flight. Logins,
  password changes, recovery resets and encrypted backups now share one
  bounded-wait gate, and an exhausted gate answers with a retryable 503 rather
  than an incorrect-password error.

## [v0.1.6] — 2026-08-22

### Highlights

- Added a selectable dashboard appearance. Alongside the default look the
  topbar offers **Console** (dense operator console, monospaced figures,
  hairline grid), **Atelier** (spacious, settings-app rows, soft elevation) and
  **Topology** (network-first, teal, addressing in mono). The choice is stored
  per browser and is independent of the light/dark control, so each appearance
  has both a light and a dark form.
- Implemented appearances as a CSS layer keyed off `data-skin`, remapping the
  `--classic-*` tokens the shell already reads. The default appearance ships no
  stylesheet, so an appliance with nothing selected renders as v0.1.5 did.
- Unified the table standard across the remaining pages so connected devices,
  reservations, port forwards, firewall rules, WireGuard peers, snapshots and
  audit events share one row, header and action presentation.
- Applied a compact form standard across all pages and grouped LAN & DHCP into
  labelled WAN / LAN / DHCP / DNS sections with per-section save actions.
- Added Wake-on-LAN for offline devices, plus in-place editing of firewall
  rules, WireGuard peers and DHCP reservations.
- Added a per-device traffic column to connected devices, and service recovery
  action tiles with public-IP history to Gateway Health.
- Reported application memory separately from reclaimable file cache.
- Added snapshot deletion from the dashboard and one-second boot sampling.
- Published a standalone LAN & DHCP design gallery at `/design/` on the GitHub
  Pages site, alongside the unchanged demo at the site root.

### Fixes

- Fixed the WAN / PPPoE field grid collapsing to zero width. The floated
  fieldset heading was cleared by the element immediately after it, which in the
  demo build is the hidden WAN toggle and therefore cleared nothing. Headings
  now sit in normal flow inside the card and the float is removed.
- Restored accessible names on fieldset groups whose `<legend>` became a heading
  element; each group is now named with `aria-labelledby`.
- Restored the `Online` state on connected devices, which the unified table work
  had replaced with the lease countdown alone.
- Kept device tables inside their containers.
- Moved inline style objects out of runtime React source into stylesheets.
- Preserved executable bits for distribution integration files.

### Compatibility

No routing, firewall, DHCP, WireGuard or update-trust behaviour changed in this
release. An appliance qualified on v0.1.5 does not need to repeat the full
network validation matrix.

## [v0.1.5] — 2026-08-19

### Highlights

- Promoted the approved v0.1.5 dashboard visual system into the production
  appliance frontend while keeping the GitHub Pages demo on the same shared
  components and CSS; demo mode now differs only in mocked/demo-only state.
- Reworked mobile navigation around a fixed top-right control and pushed/scaled
  foreground-page interaction, with same-button close, Escape close,
  exposed-page close, route navigation close and scroll restoration.
- Locked the desktop frame to an exact 37 px gutter at the viewport/sidebar edge,
  between sidebar and content, and at the far-right content edge.
- Removed the redundant `Gateway healthy` Overview ribbon chip while retaining
  the separate topbar health control.
- Redesigned Logs startup diagnostics as a horizontal milestone timeline with a
  horizontally scrollable mobile presentation.
- Added per-device Internet pause for 15 minutes, 1 hour or until resumed, using
  a router-applyd-owned nftables timeout set and trusted-LAN address validation.
- Added sampled 30-day gateway availability/outage insights and bounded local
  public-IP transition history.
- Added fixed Gateway Health recovery actions for WAN reconnect, DNS/DHCP restart
  and WireGuard restart without exposing arbitrary service names or shell access.
- Moved Recovery/backup/pfSense migration tools to the Recovery workspace while
  keeping authentication/security controls in Security.
- Expanded connected-device state with Online, Last seen and New indicators from
  existing bounded DHCP/accounting data.
- Expanded production and GitHub Pages Playwright coverage across all mobile
  dashboard routes, pushed-menu behavior, overflow, desktop frame geometry and
  horizontal startup timeline behavior.
- Expanded the Golden Appliance ISO matrix with installed cold boot, firstboot
  non-reentry, SSH/API readiness, forced routerd supervision recovery, warm
  reboot, existing-install overwrite refusal and undersized-disk refusal.
- Updated release-facing README, installation, Proxmox, validation, release
  security and v0.1.5 release documentation for the new ISO and signed artifacts.

### Release artifacts

The signed release workflow publishes the tested AMD64 Golden ISO and checksum,
signed AMD64/ARM64 distribution archives and manifests, SPDX SBOMs,
`SHA256SUMS`, and GitHub attestations after the exact tagged release passes its
release E2E gate.

## [v0.1.4] — 2026-08-18

### Highlights

- **Golden Appliance ISO for AMD64/Proxmox.** Alpine 3.22, the matching
  `linux-lts` kernel/modules/initramfs, MinimalRouter, Dashboard and runtime
  dependencies are assembled in CI into a bootable 8 GiB Golden disk image.
  The user VM only verifies and raw-copies that image, then reboots into a
  one-shot firstboot wizard.
- **Offline installation by construction.** The live flasher no longer runs
  `apk`, `setup-disk`, `mkinitfs`, target chroots or the MinimalRouter installer.
  This removes the repository-path/package-resolution and live-kernel/target-
  kernel mismatch failure classes found in earlier ISO work.
- **End-to-end appliance validation.** CI flashes a blank VirtIO disk, reboots,
  completes firstboot over `ttyS0`, proves an installed serial root login and a
  real password-authenticated SSH login, then verifies LAN, nftables, kernel/
  modules, canonical state, routerd readiness and Dashboard `:8443`.
- **Release ISO carries update trust.** The signed release workflow builds the
  Golden ISO from the already signed AMD64 payload, requires
  `firmware-signing.pub`, repeats the full QEMU install test, and publishes the
  ISO with SHA-256 verification and GitHub attestation.
- **Serial recovery is first-class.** A dedicated `ttyS0 @ 115200` installer
  entry is available, firstboot follows the selected console, and the installed
  appliance restores a password-protected serial recovery getty.
- Startup diagnostics are anchored to the actual Linux boot ID, retain only the
  last five boots, stop network probes after each milestone succeeds, and become
  completely read-only after the boot capture is complete.
- The local recovery console remains an on-demand tool rather than a daemon and
  no longer keeps an idle refresh goroutine or timer running while waiting for
  operator input.
- Passive appliance work is reduced: per-device accounting collects every five
  minutes, passive SQLite maintenance runs hourly, and WireGuard telemetry avoids
  repeated helper processes when an interface is absent and caches stable state.
- Dashboard polling is visibility-aware. Live WAN byte counters sample every five
  seconds only while the tab is visible; the coherent dashboard snapshot refreshes
  every 30 seconds and immediately refreshes when the operator returns to the tab.
- The Overview can make one lightweight post-connect WAN estimate outside the boot
  critical path and displays it as an approximate download/upload line rate. A
  later full Speed Test replaces that estimate automatically.
- Speed Test temporarily removes QoS only from the active runtime, never from the
  canonical configuration or durable last-good state, and always reconciles the
  canonical runtime before returning. Restore failure enters `RecoveryRequired`.
- Smart Change Preview, conservative PPPoE auto-recovery, one-click network
  diagnostics, Startup Timeline, the local recovery console, and the complete
  English operator guide are part of the same appliance reliability baseline.
- Large unreferenced duplicate dashboard screenshots were removed; the numbered
  documentation screenshot set remains canonical.

### Fixed

- Ambiguous login failures no longer make the optional TOTP field mandatory for
  users who do not have two-factor authentication enabled.
- Runtime status and diagnostic exports now report build-injected release
  version, source commit and build date instead of the stale `v0.1-alpha` label.
- Firmware API preflight and A/B staging now share one signed-appliance candidate
  validation boundary, including executable/layout checks in addition to hashes.
- Normal A/B staging is forward-only. Returning to an older verified slot remains
  available through the explicit rollback command rather than signed-release
  replay through the ordinary stage path.
- GitHub Beta releases use a draft-first flow, with release build metadata
  embedded in the Go binaries. The workflow reports when repository-level
  release immutability is not enabled.
- Golden ISO build metadata now carries the real version, source commit and build
  date instead of producing `unknown` commit/date values in appliance binaries.
- The final ISO CI harness no longer falsely fails when Alpine prints `sshd` at
  the start of an `rc-update` line or when the real `INSTALLED_SSH_OK` marker has
  already been consumed by the authenticated SSH verification step.

## [v0.1.2] — 2026-08-15

First repository-native signed Beta release. The release contains AMD64 and ARM64
install archives, Ed25519-signed appliance manifests, SHA-256 checksums, SPDX
SBOMs and GitHub attestations, built from an SSH-signed release tag.

### Added

- Public, interactive GitHub Pages dashboard demo with synthetic documentation
  data. It never connects to a router or contains production credentials.

### Fixed

- **Boot ordering: forwarding is no longer enabled before the firewall exists.**
  `applyKernelHardening` used to set `net.ipv4.ip_forward=1` before `nft -f` ran.
  On a cold boot the `inet minimalrouter` table does not exist yet, so there was
  a window where routing was on and the kernel default ACCEPT policy was the only
  thing between WAN and LAN. Forwarding is now switched on by a separate
  `enableIPForwarding()` call that runs only after the policy is proven loaded,
  on the apply, startup-reconcile and rollback paths alike.
- **Boot ordering: `router-applyd` now declares `after net`.** Only `routerd`
  depended on `net`, so OpenRC left the relative order of the network service and
  the privileged helper undefined; `ifup` could re-run against a LAN address
  `configureRuntimeLAN()` had just installed.
- **Installer now owns `/etc/network/interfaces`.** A stock `setup-alpine` host
  keeps `iface eth0 inet dhcp`, which competes with pppd for the WAN and delays
  boot on `need net`. The isolated lab has always written a `manual`-only file by
  hand (`scripts/lab/payloads/mr-install.sh`), so every lab result was produced on
  a host where this was already fixed and no installed system got the benefit.
  The previous file is saved to `/etc/network/interfaces.minimalrouter-backup`.
- **QoS is no longer applied before the PPPoE restart that destroys it.** In
  `installAndActivate` shaping was attached to `ppp0` and then the same apply
  restarted pppd, which recreates the interface and drops every qdisc; the
  verification immediately below therefore always logged a missing qdisc. The
  startup-reconcile and rollback paths already had the correct order.
- **Graceful shutdown.** Neither daemon handled SIGTERM, so OpenRC always killed
  them hard after the 10-second retry window and every deferred close in
  `routerd` was unreachable — SQLite never got a clean close on reboot.
- **Canonical reconcile budget.** `routerd` gave `Reconcile` 150s while the
  privileged apply budget is 2 minutes x 2 attempts, so a transport retry could
  be cancelled halfway and turned into a fatal start. The budget is now derived
  from the helper constants (`apply.ReconcileBudget`), and `routerd.initd`
  `start_post` waits longer than it.
- **Telemetry cost.** `RuntimeSnapshot` spawns eight short-lived `doas wg show`
  processes per call, and the Overview polls `/api/v1/system` every two seconds
  for the bandwidth chart. Snapshots are now shared behind a one-second cache.
- Readiness marker is opened with `O_CREATE`, so a manual start no longer fails
  fatally when the OpenRC `start_pre` hook has not pre-created the file.
- Management certificate is cached instead of being re-read and re-parsed from
  disk on every TLS handshake.
- Speed test: uploads are timed from the first body byte rather than from before
  connection setup, the run is bound to the request context, and concurrent runs
  are rejected instead of halving each other's result.
- `applyKernelHardening` reasserts `nf_conntrack_max` and the default
  `rp_filter`, which previously existed only in the boot-time sysctl file.

### Fixed (dashboard)

- **Appliance health is now actually displayed.** `/api/v1/health` and the whole
  `internal/health` package existed, and `ApplianceHealth` was even declared in
  `api-types.ts`, but nothing in the dashboard ever called the endpoint —
  including while `docs/APPLIANCE_HEALTH.md` described the banner in the present
  tense. Overview now renders it, and the DNS chip and notification bell are
  driven by it.
- **Removed status elements that were green regardless of state:** the hardcoded
  DNS chip, the unconditional "Within normal operating range" resource note, the
  notification bell that always reported no alerts, and the static
  "Setup complete" pill.
- **Storage pressure is visible.** `runtime.storage` was populated by the backend
  and rendered nowhere, so the first sign of trouble was a configuration save
  failing with HTTP 507 at 90% usage. There is now a chip plus warning and
  critical banners.
- **Gateway latency outages are drawn as gaps.** A failed probe has no latency;
  substituting `0` rendered an outage as the best possible result.
- Dynamic DNS status card reports the configured provider instead of whichever
  tab is open, and the enable toggle can no longer switch on a provider other
  than the one shown.
- The LAN prefix selector, which the state machine rejects on every submit
  ("live LAN subnet changes are unsupported"), is now read-only, and the netmask
  is derived from the prefix instead of a `/16`-or-`/24` branch that produced a
  netmask disagreeing with the CIDR for every other length.
- Theme choice persists across reloads and honours `prefers-color-scheme`.
- Confirmation countdown no longer restarts on every 15-second poll.

- Proxmox live-cutover DDNS reliability: `router-applyd` now raises the
  loopback interface/address when generic networking is intentionally disabled
  and enforces `root:inadyn 0640` for the generated No-IP/Cloudflare config in
  both normal apply and boot reconciliation. This prevents local DNS failure
  and the misleading inadyn “Missing .conf file” state.

### Added

- **Port forwards now work, over the WireGuard tunnel.** Rules were stored,
  validated, imported from pfSense, counted in the UI and editable — but the
  nftables generator contained no `dnat` or `prerouting` chain at all, and
  validation rejected every enabled rule, so the panel could only ever produce an
  error. The generator now emits a `prerouting` DNAT chain in which every rule is
  bound to the WireGuard server interface. WAN exposure remains unsupported:
  a DNAT rule matching `ppp0` or the WAN interface is impossible to generate, and
  a regression test asserts it.
- **Per-device traffic accounting** (`accounting.enabled`, opt-in). Two dynamic
  nftables sets count forwarded bytes per LAN address; `routerd` folds them into
  calendar-month buckets, treating the counter reset caused by every apply as a
  reset rather than as negative traffic. Only byte totals per address are stored —
  no ports, destinations or payload. New `GET /api/v1/accounting` and a Traffic
  dashboard section.
- **DHCP reservations in the dashboard.** dnsmasq already received `dhcp-host=`
  for every static lease; there was simply no way to create one outside a
  hand-written API call. The device table now has a reservation editor that warns
  when the chosen address falls inside the dynamic pool.
- **Firewall custom-rule editor.** The generator has always emitted custom rules
  into all four input/forward x allow/deny positions; the capability had no UI.
- **30-day gateway history.** Raw samples are pruned after 7 days, so proving an
  ISP problem from last month was impossible. Samples are now also rolled up
  hourly (400-day bound, ~10k rows) and `?window=30d` is served from that table.
- Static DNS records (`dns.records`): fixed name → IP entries served by the
  local resolver via `host-record=` regardless of DHCP, for hosts with static
  addressing (e.g. `immich.local` on the isolated media network).
- Isolated extra LAN segments (`firewall.extra_lans`): a second network behind a
  dedicated router interface with no WAN/LAN egress and no router services
  (no DHCP/DNS). Only the listed source networks may reach the single exposed
  service (`dst_ip:dst_port`); input is anti-spoofed and ICMP-only, everything
  else is dropped by the default policy. Used for the isolated Immich media
  network (`10.20.30.0/24`).

## [v0.1.0] — 2026-08-03

Beta milestone: the full system is validated and running in production on
Proxmox VM 108. See the GitHub release on `minimalrouterhome`.

### Added

- WireGuard peer provisioning by name only: the router auto-assigns the next
  free client IP in the subnet and defaults the server endpoint to the DDNS
  domain (`nextFreeWireGuardIP` with subnet-bound allocation).
- Central Security page: firewall posture, failed-login and CSRF/origin-reject
  counters, and a bounded live security-events feed; account controls moved into
  the VP profile dropdown.
- Overview security chips (firewall, rejected requests, previous login) and a
  router-icon sidebar with the revision pinned at the bottom.

### Fixed

- Production deploy procedure hardened: binary swap now stops services first
  (avoiding `Text file busy`) and gunzips on the guest before install.

## [v0.1.1] — 2026-08-03

### Added

- `trusted_networks` management access gate: only source networks listed in the
  config (plus loopback) can reach the administrative Web UI/API; enforced on the
  real TCP peer address before authentication, never via forwarded headers, with
  an operator-lockout guard on configuration changes and a Security-tab panel for
  adding/removing CIDR networks. Does not replace the administrator password.
- Bounded storage-pressure policy with explicit 80% warning and 90% critical
  thresholds, authenticated runtime telemetry, and HTTP 507 rejection of durable
  management mutations when safe persistence cannot be guaranteed.
- Periodic SQLite retention maintenance and passive WAL checkpoints without
  `VACUUM` or forwarding-plane interruption.
- Bounded `routerd` and `router-applyd` log rotation (1 MiB active logs, four
  compressed rotations) in Alpine installation and distribution paths.
- Central authenticated appliance-health model with Healthy, Warning, Degraded,
  Recovery required, and Unknown states covering recovery, storage, memory,
  conntrack, time, WAN/gateway, core supervision, DNS/DHCP, PPPoE, WireGuard,
  update state, and encrypted-backup age.
- Overview appliance-health banner with independent 15-second refresh and concise
  explanations for non-healthy signals.
- Durable update-operation journal for crash-safe A/B activation and rollback.
- Durable privileged-operation intent and completed-result records written around
  each `router-applyd` side-effect boundary.
- Recovery-safe bootstrap executables independent of the candidate firmware slot.
- Failure-injection coverage for interrupted activation, rollback, corrupt
  journals, state-write failures, and concurrent update operations.
- Deterministic router failure scenarios covering lost privileged responses,
  confirmation-response loss, simulated power loss during pending changes,
  WireGuard-only management changes, failed automatic rollback, SQLite commit
  failure, unverified helper results, boot reconciliation, incomplete helper
  intents, corrupt helper metadata, and two-phase confirmation ordering.
- Explicit `RecoveryRequired` transaction and RPC outcome for runtime states whose
  commit or rollback has not been verified.
- Allowlisted `RECONCILE` privileged operation that may supersede unresolved
  helper journal state only by applying and verifying SQLite canonical state.
- Separate `COMMIT_CONFIRMED` phase after runtime confirmation and SQLite commit.
- Failure-scenario matrix for IPC, power, process, storage, firewall, DHCP/DNS,
  PPPoE, WireGuard, update, backup, restore, and target-Proxmox testing.
- Fuzz targets for malformed unauthenticated API requests and update journals.
- Isolated WAN-router-LAN network namespace laboratory covering DHCP, DNS, NAT,
  firewall, TCP, UDP, parallel flows, packet loss, latency, and WAN port checks.
- API and update-state performance workflows with `ns/op`, `B/op`, and
  `allocs/op` artifacts.
- ARM64 QEMU smoke testing for recovery-safe commands.
- High-confidence `gosec`, `shellcheck`, `actionlint`, Linux binary inspection,
  and executable-stack rejection.
- Current validation document separating automated evidence from target-host
  Proxmox and hardware gates.
- Expanded Proxmox pilot, testing, recovery, and evidence documentation.
- Professional project README, documentation index, governance, privacy,
  contribution, support, release, and community documentation.
- Dashboard screenshot and synthetic documentation data.
- Local recovery console for credential reset, LAN repair, snapshot restore, and
  factory reset with session revocation and undo snapshots.
- DNS Filter device profiles and scheduled Kids profile workflow.
- Frontend unit and Playwright E2E coverage.
- Ed25519 release manifests, SHA-256 checksums, SPDX SBOMs, provenance, and
  explicit A/B staging/activation/rollback.

### Changed

- Nonessential gateway sample/reconnect history is shed under critical storage
  pressure while live probing and in-memory gateway health continue.
- Recovery and configuration writes remain fail-closed under critical storage
  pressure; read-only status, previews, verification and encrypted backup export
  stay available.
- Ambiguous `routerd` to `router-applyd` transport failures retry the exact same
  transaction ID so the helper can return its persisted idempotent result.
- Privileged mutations write an intent record before side effects and a validated
  final result afterward; an incomplete intent blocks ordinary mutation.
- Privileged transaction, pending-confirmation, and last-good records are
  structurally validated and fail closed when unreadable or inconsistent.
- WireGuard key, port, address, peer, and route changes require commit-confirm
  while management is WireGuard-only.
- Commit-confirm now follows a two-phase persistence order: verify candidate
  runtime, commit the exact candidate revision to SQLite, then record helper
  `last-good` and clear pending state.
- A failed explicit final helper commit retry uses a fresh transaction ID while
  transport retries inside the same attempt retain the same ID.
- A failed automatic commit-confirm rollback retains pending/candidate access,
  blocks overlapping configuration, schedules another attempt, and uses a fresh
  rollback transaction ID rather than replaying a cached failed response.
- Core GitHub CI actions and artifact upload use v7.
- Dashboard development uses TypeScript 6.0.3 and Node.js type definitions 26.1.2.
- TypeScript 6 CSS side-effect import checks are satisfied with the Vite client
  declaration rather than disabling the stricter compiler behavior.
- Active A/B slots now supply `routerd`, `router-applyd`, and dashboard assets
  through stable dispatchers.
- Update and recovery commands always execute from an independent bootstrap
  payload, preventing a candidate slot from replacing rollback tooling.
- Update staging now re-verifies copied files against the signed manifest and
  performs atomic, synchronized filesystem operations.
- Recovery authentication reset, LAN change, snapshot restore, and factory reset
  use transactional SQLite operations.
- CLI help and read-only status commands avoid privileged side effects.
- The release signer safely handles raw Ed25519 keys and can emit the public trust
  anchor covered by the signed manifest.
- Documentation now treats dated resource/security reports as historical evidence
  and uses `docs/CURRENT_VALIDATION.md` for current repository status.
- Proxmox guidance explicitly requires read-only VM discovery, isolated LAN,
  test/NAT WAN, graceful lifecycle handling, and pfSense rollback.
- The first-run wizard uses discovered interface choices and external CSS.
- `router-applyd` starts with no-new-privileges, disabled dumpability, bounded
  resources, fixed executable path, and sanitized loader environment.
- Node.js remains build-time only; the router runtime is Bash-free.

### Fixed

- Lost IPC responses after a completed apply or confirmation no longer force an
  unnecessary unknown outcome when the helper can return its saved result.
- A helper restart after an incomplete privileged operation can no longer treat
  the same request as fresh and repeat side effects.
- A different transaction can no longer bypass an unresolved helper intent or
  `RecoveryRequired` result.
- Corrupt or valid-but-empty helper metadata is no longer treated as absent state.
- Privileged RPC responses with contradictory success, verification, rollback, or
  recovery flags are rejected.
- Privileged confirmation now recognizes WireGuard control-plane changes that can
  break a WireGuard-only management path.
- A rollback that fails verification is no longer falsely reported as
  `RolledBack`; it is reported as `RecoveryRequired`.
- SQLite commit failure reports `RolledBack` only after a successful, verified
  privileged restoration of the previous configuration.
- Helper `last-good` no longer advances before the corresponding SQLite canonical
  commit during disruptive confirmation.
- Failure to acknowledge helper `last-good` after SQLite commit no longer enables
  timeout rollback of the now-canonical candidate.
- A crash between update pointer changes can no longer silently lose the intended
  activation/rollback transition; journal reconciliation restores consistency.
- Corrupt state no longer leaves a blocking partially staged version.
- State-save failures remove incomplete final slots.
- Unsafe, broken, absolute, or traversal update pointers are rejected.
- Recovery session-deletion failures roll back the entire credential/configuration
  transaction.
- Raw 64-byte signing keys no longer risk scanner-reported ignored close errors.
- Dashboard TypeScript 6 builds accept reviewed CSS module side effects.
- Clean Alpine CI exercises the signed update, active slot, dashboard marker, and
  rollback path.
- TOTP disable validates the request, current password, code, and session
  revocation in the correct order.
- Distribution builds create staging directories before copying binaries and
  dashboard assets.

### Security

- Critical disk pressure blocks durable management mutations rather than allowing
  runtime changes to be reported successful without persistent evidence.
- Health collection is read-only and does not inspect or return PPPoE passwords,
  WireGuard private keys, provider tokens, administrator credentials, backups, or
  other secret material.
- Signed staging rejects untrusted keys, unsafe paths, symlinks, non-regular
  files, hash mismatches, duplicate versions, and package-supplied hooks.
- The verify-to-copy window is closed by re-verification of staged files.
- Update/recovery dispatchers use a fixed PATH and remove loader/environment hooks.
- Privileged side effects are never started without a durable intent marker.
- Unknown privileged outcomes block future mutation until canonical
  reconciliation succeeds.
- Only the typed `RECONCILE` path may override unresolved helper journal state;
  ordinary apply and confirmation operations remain blocked.
- Runtime data, credentials, private keys, backups, packet captures, databases,
  VM images, and private network inventory are rejected by repository hygiene.
- Recovery has no WAN endpoint and credential changes revoke sessions.
- Device-profile rules are evaluated before established-connection acceptance so
  expired policy flows are not grandfathered.
- QoS activation is non-fatal everywhere: a missing or inactive qdisc no longer
  aborts apply, verify, or boot reconciliation; it is logged and retried on the
  next apply cycle.
- Squid shuts down cleanly on service stop via a bounded `shutdown_lifetime`.
- Minimum administrator password length is 12 characters (was 15) to match the
  deployed first-run credentials.

### Known limitations

- Beta; not yet supported as an unattended production firewall.
- v0.1.4 now has a tested, checksummed and attested AMD64 Golden release ISO, but
  the fully qualified installed-disk target is still SeaBIOS/MBR; UEFI target
  qualification and owner-qualified recovery media remain open gates.
- Real owner-Proxmox Golden-ISO cutover, repeated PPPoE/reboot testing, external
  scans, long-duration, full-disk, inode-exhaustion, read-only-filesystem,
  process-kill, and destructive power-loss evidence is still required.
- IPv6 parity, VLAN workflows, multi-WAN, HA, IDS/IPS, and a broad package
  ecosystem are not current stable features.
- Same-kernel namespace throughput is not a physical or VirtIO performance claim.
