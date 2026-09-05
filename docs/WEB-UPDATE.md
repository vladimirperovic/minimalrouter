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

## What is still missing for a dashboard "New version" button

This document and the check cover the compatibility gate only. Still to build:

1. a background release check with caching, ETag/backoff and a `stale` state, so
   the dashboard does not call GitHub on every page load;
2. candidate selection that pins the confirmed release (a newer release
   published mid-confirmation must not be installed silently);
3. an update operation with durable state that survives closing the tab and
   restarting `routerd`, coordinated against configuration applies;
4. a readiness definition where success means the expected build is actually
   serving, not that a symlink moved;
5. the shared update dialog and the sidebar entry that opens it.

Until those exist, the offline upload path in the profile menu remains the
supported way to install a signed release from the dashboard.
