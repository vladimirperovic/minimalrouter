# Web updates: what makes a release installable from the dashboard

This document explains why a signed release is sometimes installable from the
dashboard and sometimes only by the full distribution installer, and how to tell
which one a given build is. Read it before changing `rust/src/bin/router-update.rs`,
`scripts/build-rust.sh`, or anything under `/usr/libexec/minimalrouter/bootstrap`.

## The rule

An A/B web update replaces the *slot*: `routerd`, `router-applyd` and the
dashboard bundle. It cannot replace anything that has to keep working when the
update is rolled back.

Three runtime tools are deliberately outside the slot:

```text
/usr/libexec/minimalrouter/bootstrap/bin/router-update-<arch>
/usr/libexec/minimalrouter/bootstrap/bin/router-recovery-<arch>
/usr/sbin/router-setup
```

`router-update` performs the activation and the rollback; `router-recovery` is
the local console used when the management plane cannot be reached. The installed `router-setup` also verifies canonical provisioning before services start. These tools cannot
be rolled back by moving a symlink, because the code doing the rollback would be
the code being rolled back. So `verify_runtime_layout_compatibility` refuses to
activate a candidate whose copies of them are not **byte-identical** to the ones
already installed ([router-update.rs](../rust/src/bin/router-update.rs)).

The same applies to the OS integration files (init scripts, firstboot scripts,
sysctl, module list, logrotate, `slot-exec`, `compatibility.json`, the PPP QoS hook): they run
as root outside the slot, so an A/B update requires an exact match there too.

A release that changes any of those is not defective — it simply has to arrive
through the full signed installer once.

## The trap this caused, and the fix

Go stamps VCS metadata into every binary by default. Comparing the published
v0.1.5 and v0.1.6 archives shows `router-update-amd64` differing **only** in:

```text
mod   github.com/vladimirperovic/minimalrouter v0.1.5  ->  v0.1.6
build vcs.revision=80a84a10...                        ->  fd5e6802...
build vcs.time=2026-08-20T07:37:59Z                   ->  2026-08-23T09:09:02Z
```

Same Go version, same dependencies, same flags, identical compiled code — and a
different SHA-256. Under the byte-identity rule that made *every* release
un-installable over *every* other release, for a reason that had nothing to do
with compatibility. The dashboard's update button would have promised an install
that the root updater must refuse.

The bootstrap binaries are therefore built with `-buildvcs=false`
(`GO_BOOTSTRAP_BUILD_FLAGS` in the [Makefile](../Makefile)), so their bytes
depend on bootstrap source and toolchain only. Their provenance is still carried
by the signed release manifest, which is the trust anchor that actually matters;
`routerd` and `router-applyd` keep their VCS stamps because they live in the
slot and are never byte-compared.

Because the toolchain is part of the input, the toolchain version is part of the
bootstrap contract: bumping it changes the bytes and requires the same
acknowledgement as a source change.

Since v0.4.0 the same rule holds for the Rust build. `scripts/build-rust.sh`
builds the three bootstrap programs without the version, commit and build-date
variables that the runtime daemons receive, remaps source and toolchain paths out
of the binaries, and uses `--locked` so `Cargo.lock` is part of the input. The
pinned inputs are Rust 1.99.1, zig 0.17.0 and cargo-zigbuild 0.24.0
(`.github/actions/setup-rust`); changing any of them, or `Cargo.lock`, can change
the bytes.

## How to tell whether a build is web-updatable

```bash
sh scripts/ci/bootstrap-compatibility.sh
```

It builds the bootstrap binaries from the baseline release and from the working
tree with the same toolchain and deterministic flags, compares them, and prints
`WEB_UPDATE_SUPPORTED=true|false`. CI runs it on every pull request.

The baseline and any intentional break are recorded in
[`packaging/alpine/bootstrap-baseline.json`](../packaging/alpine/bootstrap-baseline.json):

| Field | Meaning |
|---|---|
| `bootstrap_baseline_ref` | The release whose bootstrap bytes this tree should match |
| `bootstrap_change_acknowledged_version` | The version in which the bootstrap deliberately changed |
| `bootstrap_change_reason` | Why it had to change — a reviewer must be able to judge it |
| `requires_full_installer_from` | Appliances at or below this version need the installer once |

If the check reports drift that is not acknowledged, it fails. The fix is to
record the change and document the installer step — **never** to relax the
byte-identity check in `router-update`.

## Current checkout and installation boundary

v0.4.0 replaces the Go runtime with a Rust implementation, so `router-update`,
`router-recovery` and `router-setup` are different programs from every v0.3.x
build on both architectures. `bootstrap-baseline.json` acknowledges the change
for version 0.4.0 and moves `requires_full_installer_from` to 0.3.3: **every
appliance running v0.3.3 or earlier needs the full signed distribution installer
once**, and the dashboard A/B updater refuses the transition by design. The
v0.4.0 Golden ISO also moves the operating system to Alpine 3.24 with linux-lts
6.18; an existing appliance keeps its own Alpine release, and a replacement
appliance may be installed on a blank disk and restored from an encrypted backup.
The baseline stays at v0.3.1 until the v0.4.0 tag exists; the check reports a
baseline without a Rust workspace as a recorded change. After the tag, move
`bootstrap_baseline_ref` to v0.4.0 so the comparison is against the bytes
appliances actually run.

`bootstrap_baseline_ref` is v0.3.1, the bytes published appliances run since
2026-10-14. v0.3.2 changed only `routerd`, `router-applyd`, the dashboard and
the full installer script, and v0.3.3 changes only the DNS filter inside
`routerd`: `scripts/ci/bootstrap-compatibility.sh` reports all six bootstrap
binaries (`router-update`, `router-recovery`, `router-setup` on AMD64 and ARM64)
byte-identical to v0.3.1, and the installed integration files (firstboot, init
scripts, sysctl, module list, logrotate, `slot-exec`, `compatibility.json`, the
PPP QoS hook) are unchanged since v0.3.1. v0.3.1 and v0.3.2 appliances
therefore take v0.3.3 as an ordinary signed dashboard update. v0.3.0 and
earlier appliances still need the full signed distribution installer
once, to reach v0.3.1 or later. Fixes that would change those files (a minimum
length for the firstboot recovery password, audit ordering in the shared store,
init script orphan cleanup) are deliberately deferred to a release that needs
the full installer anyway.

v0.3.1 changed `router-recovery` and `router-setup` on AMD64 and
ARM64 again, for fixes found in the v0.3.0 live test: the shared configuration
store lists and prunes snapshots by configuration revision (a corrected clock
could otherwise make old snapshots look newest and prune the real newest ones),
configuration deep copies keep missing lists distinct from empty ones (restore
previews showed a phantom change), and the shared network package gained the
ARP neighbour reader behind the LAN address check. `router-update` is
byte-identical to v0.3.0 on both architectures. Consequently, v0.3.1 requires
the **full signed distribution installer once** on existing v0.3.0 and earlier
appliances, and `bootstrap-baseline.json` acknowledges the change for version
0.3.1. As with v0.3.0, the dashboard A/B updater refuses the transition, the
Golden ISO refuses an occupied disk, and a replacement appliance may be
installed on a blank disk from the v0.3.1 Golden ISO and restored from an
encrypted backup.

v0.3.0 changed the same two binaries for a larger reason: the shared
configuration store began migrating named snapshots and durable recovery/backup
metadata, configuration transactions began retaining sanitized audit outcomes,
and Go 1.26.9 with updated `x/net`, `x/crypto` and `x/sys` (the October 2026
vulnerability scan findings) changed the bootstrap build inputs. v0.3.0
therefore required the full signed distribution installer once on v0.2.1 and
earlier appliances. Its acknowledgement kept `bootstrap_baseline_ref` at v0.1.8
until the v0.3.0 tag existed; v0.3.1 moves the baseline to v0.3.0 so the
comparison is against the bytes appliances actually run.

For historical context, the v0.2.0 candidate preserved the v0.1.9 bootstrap and installed integration
contract. On 2026-10-08, all three bootstrap programs built for both supported
architectures with the release toolchain (Go 1.25.13) matched the published
v0.1.9 manifest hashes. The 12 installed integration files also matched the
published AMD64 payload. This supports an ordinary signed dashboard update from
an unmodified v0.1.9 appliance; it is not a real-appliance update test or a claim
that any later source tree shares those bytes.

Published v0.1.7 and earlier appliances still require the one-time signed full
distribution installer introduced for v0.1.8. Changing a version label cannot
make their bootstrap bytes compatible. `router-setup` participates in the same
deterministic byte comparison as updater/recovery. Future releases that change
this contract must record the exact version in the bootstrap acknowledgement.

The compiled `internal/firmware/appliance_roles.go` table describes required
payload files, architecture-specific binaries, modes and installed comparison
paths. The manifest cannot choose arbitrary privileged destination paths.
`firstboot`, `firstboot-ready`, `init.d/minimalrouter-firstboot`, `install-core.sh`
and `bin/router-setup-<arch>` are required alongside the existing runtime files.
Readable static assets, including `.gz` files, are signed and normalized to 0644.
Both daemons must come from the same complete slot; an invalid active slot fails
startup rather than selecting a different daemon or Dashboard generation.

`state.json.minimum_version` is the monotonic installed release floor.
`UpgradeFloor()` takes the maximum of that floor and a real current release.
A synthetic `0.0.0+bootstrap.<digest>` directory identifies payload contents, not
a trusted release version. An unknown legacy/development baseline therefore
refuses web staging; build-info display values never establish update trust.
Normal stage/activate requires a strictly newer version. Explicit rollback keeps
the high-water mark and also verifies the previous slot's installed integration.

The root-operated full installer verifies its pinned key, signed manifest,
architecture, complete inventory and modes before package/runtime writes. Signed
distributions include `release-manifest.json`; VERSION and all payload files are
signed before it is copied into the archive. A same-version signed full reinstall
is allowed. An unsigned development installation is a separate explicit root
operation: it preserves any known floor and trust key, and cannot create a known
floor for an otherwise unknown installation.

Before mutation, `BeginInstallation` rechecks the floor under the slot lock and
persists `installation.json`. The dispatcher/admission guard is installed and
synchronized before package or remaining integration changes. Pending installation
or offline migration blocks daemon startup while bootstrap recovery/updater remain
available. This is a forward-repair fence: after interruption, rerun the complete
installer from local recovery; do not delete its journal to resume mixed runtime.

The installer synchronizes system writes before final commit. The updater checks
and fsyncs installed integration, copies and verifies a complete baseline, fsyncs
all copied files/directories, and rechecks the floor under the commit lock before
publishing the baseline and clearing the fence. A stale preflight cannot lower or
erase a higher committed or interrupted-install floor. Each full install starts
a new rollback generation, clearing previous/pending A/B pointers because a slot
rollback cannot undo OS integration changes.

Startup allows 300 seconds, above the 270-second canonical reconcile budget.
Updater service starts allow 330 seconds plus a bounded pipe-drain allowance;
stop/status calls remain bounded at 30 seconds. Cancellation also terminates
OpenRC command children before recovery continues. Shared constants live in the
dependency-free `internal/runtimebudget` package.

## The dashboard flow

```text
routerd starts
  └─ release checker: first check ~3 min after readiness, then every 6 h + jitter
       ├─ conditional request (If-None-Match), bounded pages, Retry-After honoured
       └─ one cache for the whole process: every tab reads it, nobody calls GitHub to render

dashboard
  ├─ sidebar footer: "New version · vX.Y.Z", or a quiet "Updates" entry
  ├─ one dialog (sidebar and profile menu open the same one)
  └─ one confirmation, naming the exact candidate

POST /api/v1/firmware/update {candidate_id, target_version, idempotency_key}
  └─ 202 + operation id; the work continues without the browser
       queued → downloading → verifying → staging → activating → checking_health → succeeded
                                                              ↘ failed / rolled_back / recovery_required
```

### What the endpoints do

| Endpoint | Behaviour |
|---|---|
| `GET /api/v1/firmware/status` | Cached answer, no upstream call. Carries versions, candidate, capability, `blocked_reason`, freshness and the current operation |
| `POST /api/v1/firmware/check` | One operator-triggered check, 60 s cooldown, shared by concurrent callers |
| `POST /api/v1/firmware/channel` | `stable` or `beta`, stored outside the network configuration |
| `POST /api/v1/firmware/update` | Requires the confirmed `candidate_id` and `target_version`; re-derives the candidate server-side and refuses a superseded one |
| `POST /api/v1/firmware/upload` | Offline signed build, same verification and the same operation record |

### Rules the implementation keeps

- **A failed or stale check is never shown as "up to date".** Not knowing and knowing there is nothing new are different states, and only a check that actually succeeded may claim the latter.
- **The confirmation is pinned.** A release published between rendering and confirming cannot take the confirmed one's place; the server answers `candidate_superseded` and asks for a fresh confirmation.
- **The work outlives the browser.** After the 202 the appliance owns the update; closing the dialog or the tab changes nothing. The record is durable, so a `routerd` restart cannot lose it.
- **Restart is the normal path, not a failure.** Activation restarts `routerd`; on the next start the slot state decides the outcome — running the target means succeeded, running the previous version means rolled back, anything else means recovery required.
- **Success means the new version is serving.** A moved symlink is not success; the dashboard reloads only after the appliance reports the target version running.
- **Only one update at a time**, and never while a configuration change is applying or awaiting confirmation.
- **A retried request cannot install twice.** The idempotency key returns the existing operation or its outcome.
- **Read-only sessions may watch, not act**, and the privilege boundary — not the disabled button — is what enforces it.

## What is still missing

1. **A real appliance N → N+1 proof.** Everything above is covered by Go and frontend tests, but installing a signed release over a previous one on hardware, with configuration preserved, and a deliberately broken candidate rolling back, has not been run. `docs/GOLDEN-IMAGE.md` defines that evidence.
2. **Crash and power-cut coverage in the activation window**, beyond the restart reconciliation that is unit-tested here.
3. **Compatibility reporting per candidate.** The status exposes local capability; it does not yet say "v0.1.9 exists but needs the full installer" before the download.
