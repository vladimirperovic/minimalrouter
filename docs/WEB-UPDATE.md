# Web updates: what makes a release installable from the dashboard

This document explains why a signed release is sometimes installable from the
dashboard and sometimes only by the full distribution installer, and how to tell
which one a given build is. Read it before changing `cmd/router-update/`,
`Makefile` build flags, or anything under `/usr/libexec/minimalrouter/bootstrap`.

## The rule

An A/B web update replaces the *slot*: `routerd`, `router-applyd` and the
dashboard bundle. It cannot replace anything that has to keep working when the
update is rolled back.

Two binaries are deliberately outside the slot:

```text
/usr/libexec/minimalrouter/bootstrap/bin/router-update-<arch>
/usr/libexec/minimalrouter/bootstrap/bin/router-recovery-<arch>
```

`router-update` performs the activation and the rollback; `router-recovery` is
the local console used when the management plane cannot be reached. Neither can
be rolled back by moving a symlink, because the code doing the rollback would be
the code being rolled back. So `verifyRuntimeLayoutCompatibility` refuses to
activate a candidate whose copies of them are not **byte-identical** to the ones
already installed ([runtime_layout.go](../cmd/router-update/runtime_layout.go)).

The same applies to the nine OS integration files (init scripts, sysctl, module
list, logrotate, `slot-exec`, `compatibility.json`, the PPP QoS hook): they run
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

Because the toolchain is part of the input, `go.mod`'s Go version is part of the
bootstrap contract: bumping it changes the bytes and requires the same
acknowledgement as a source change.

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
byte-identity check in `cmd/router-update`.

## Current state (v0.1.7)

`router-update` is unchanged since v0.1.6. `router-recovery` changed, because it
links `internal/config` and the firewall-source and lease-time validators were
corrected after v0.1.6; the recovery console must validate exactly like the
release it ships with.

So:

- **v0.1.6 → v0.1.7:** full signed distribution installer, once.
- **v0.1.7 onward:** web-updatable whenever the check reports
  `WEB_UPDATE_SUPPORTED=true` for the release.

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
