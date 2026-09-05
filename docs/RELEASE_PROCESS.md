# Maintainer release process

This document is the operational release checklist for Minimal Router OS. The
current release line is **Beta v0.1.7**. Release claims must stay inside the
evidence recorded in [`CURRENT_VALIDATION.md`](CURRENT_VALIDATION.md).

## Named-release rule

An official release is published only from an exact reviewed commit that has
passed the repository release gates. Do not create a release tag on a red or
partially tested commit and do not weaken tag, firmware-signature, ISO, checksum,
SBOM, or attestation validation to make publication succeed.

For v0.1.7 the exact pre-tag candidate must pass all seven required PR workflows:

- CI;
- CodeQL;
- Secret scan;
- Deep validation;
- Performance;
- Service supervision;
- Appliance ISO.

The release tag must then point to the exact validated `main` commit that contains
`VERSION=0.1.7` and `docs/releases/v0.1.7.md`.

## 1. Final pre-tag audit

Before creating the tag:

1. Confirm the intended PR is merged and `main` contains only reviewed release
   changes.
2. Confirm `VERSION` and `web/package.json` both identify `0.1.7`.
3. Confirm current README, installation, Proxmox, security, support, validation,
   changelog and release documentation describe v0.1.7 rather than an older
   maturity level or install path.
4. Confirm the GitHub Pages demo uses the same production React entry point,
   components and CSS. Demo-only behavior must remain gated by
   `VITE_DEMO_MODE` and use synthetic data only.
5. Review the full current tree for credentials, private topology, generated
   runtime state, backups, packet captures, VM disks, logs, private host
   inventory and other material prohibited by `PRIVACY.md` and `AGENTS.md`.
6. Reconfirm all seven required workflows are green on the exact candidate.
7. Record the exact `main` SHA that will be tagged.

Historical release notes and dated evidence reports may retain older version
references when those references are part of the historical record. They must be
clearly identified as historical rather than presented as current v0.1.7 state.

## 2. Create the SSH-signed annotated tag

The release workflow accepts only an **SSH-signed annotated tag** whose signer is
listed in the protected `MINIMALROUTER_RELEASE_ALLOWED_SIGNERS` secret.
Lightweight or unverifiable tags are rejected.

On a trusted maintainer checkout with the configured SSH signing key:

```sh
git checkout main
git pull --ff-only origin main
git status --short
git rev-parse HEAD
git tag -s v0.1.7 -m "Minimal Router OS v0.1.7"
git verify-tag v0.1.7
git push origin v0.1.7
```

Before the push, `git rev-parse HEAD` must equal the exact audited `main` SHA.
Do not move or recreate a published release tag casually.

## 3. Signed release workflow

Pushing `v0.1.7` starts `.github/workflows/release.yml` (`Signed release`). The
workflow must first prove:

- the ref is an annotated tag object;
- its name matches the release-tag pattern;
- `VERSION` exactly matches the tag;
- the tag's SSH signature verifies against the protected allowed-signers list.

It then repeats release checks, builds the distributions, signs the update
payloads with the protected Ed25519 firmware key, builds the Golden ISO from the
**already signed AMD64 distribution**, boots and fully installs that release ISO
in QEMU, generates SBOMs/checksums, creates GitHub attestations, and only then
publishes the release.

A failed release E2E or signature check is a release blocker. Fix the underlying
problem on `main`, validate a new exact candidate, and create a new release tag
only according to the project's versioning policy; never bypass the failed gate.

## 4. Expected v0.1.7 release assets

The public v0.1.7 release must contain exactly the intended user-download and
verification set:

```text
minimalrouter-0.1.7-amd64.iso
minimalrouter-0.1.7-amd64.iso.sha256
minimalrouter-linux-amd64.tar.gz
minimalrouter-linux-arm64.tar.gz
minimalrouter-linux-amd64.manifest.json
minimalrouter-linux-arm64.manifest.json
minimalrouter-linux-amd64.spdx.json
minimalrouter-linux-arm64.spdx.json
SHA256SUMS
```

From v0.1.7 the archive and its manifest are also what the dashboard's
in-appliance updater reads: it offers a release only when both
`minimalrouter-linux-<arch>.tar.gz` and `minimalrouter-linux-<arch>.manifest.json`
are attached. A release published without one of them is invisible to the
*New version* button rather than offered as an unverifiable download, so a
partial upload fails closed — but it also means an incomplete release silently
strands appliances on the previous version. Confirm both are attached.

The two architecture archives are signed through their Ed25519 manifests. The
per-archive `.sha256` helper files used during the build are not separate public
release assets; `SHA256SUMS` covers both archives, both manifests, both SBOMs and
the Golden ISO. GitHub Attestations provide provenance/attestation for the
release archives/SBOMs, manifests/checksums and tested Golden ISO rather than a
separate hand-written provenance file.

The release is created draft-first and is published as a normal release, marked
**latest**, only after all assets are attached. The GitHub prerelease flag is
no longer used: it kept older full releases showing as *Latest* on the
repository page, which misdirected downloads. Beta maturity is communicated by
the release title and by the status statements in the README, `SECURITY.md`
and `SUPPORT.md`, not by the prerelease flag.

## 5. Post-publication verification

After the workflow finishes successfully:

1. Confirm the release title identifies `Minimal Router OS v0.1.7 (Beta)` and
   that the release is published, not draft, and is marked latest.
2. Confirm every expected asset above exists once and has a non-zero size.
3. Download/inspect `SHA256SUMS` and verify that it lists both archives, both
   manifests, both SBOMs and the Golden ISO.
4. Verify the standalone ISO checksum:

   ```sh
   sha256sum -c minimalrouter-0.1.7-amd64.iso.sha256
   ```

5. Verify the complete downloaded asset set where practical:

   ```sh
   sha256sum -c SHA256SUMS
   ```

6. Confirm GitHub Attestations were created for AMD64/ARM64 archives and SBOMs,
   signed manifests/checksums, and the tested Golden ISO.
7. Confirm release notes come from `docs/releases/v0.1.7.md`.
8. Confirm no temporary, unsigned, development-only, private, or internal artifact
   was attached to the release.

See [`RELEASE_SECURITY.md`](RELEASE_SECURITY.md) for verification and update
trust details.

## 6. Owner pilot after publication

v0.1.7 remains a controlled Beta even after the signed release workflow passes.
Before treating it as a normal replacement router, keep the known-good router and
local console available and execute the remaining owner-Proxmox gates in
`CURRENT_VALIDATION.md`/`ROADMAP.md`, including repeated real PPPoE/reboot
recovery, WireGuard/DDNS recovery, backup restore, destructive storage/power
faults, external scans and longer soak testing.

## Repository-publication history

The repository itself was originally prepared for public development using a
separate clean-history/public-boundary process. That is a **one-time repository
publication concern**, not the procedure for every named software release.

The enduring rules from that cutover remain relevant:

- never publish a private development repository merely by rewriting `main`;
- keep credentials, runtime state, private topology, backups and historical
  private metadata outside the public repository;
- use current-tree and full-history secret scanning;
- review repository settings, branch protections and security features after any
  visibility or repository migration;
- keep private deployment material in a separate trusted location/repository;
- rotate any credential that is ever exposed in public history rather than
  relying on a later deletion to make it secret again.

For ordinary v0.1.7 and later releases, follow the named-release procedure above.
