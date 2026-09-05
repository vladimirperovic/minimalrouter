# Release security, verification, update and rollback

Minimal Router OS uses separate controls for source/tag authenticity,
distribution integrity, Golden ISO integrity and local appliance update
activation. No single checksum or workflow result is sufficient on its own.

## Release trigger

A tag matching `vMAJOR.MINOR.PATCH` starts the signed release workflow.

The workflow refuses lightweight or unverifiable tags. Maintainers must create
an **SSH-signed annotated tag** whose signer appears in the protected
`MINIMALROUTER_RELEASE_ALLOWED_SIGNERS` secret. The tag version must exactly match
`VERSION` before any release artifact is built.

### Tag-signing key rotation, 2026-09-06

The SSH key that signed the v0.1.2–v0.1.6 tags was lost with the machine that
held it. `MINIMALROUTER_RELEASE_ALLOWED_SIGNERS` was replaced with a new key
from v0.1.7 onward, and the old key was not retained in the list.

The consequence is recorded here rather than left to be discovered:
**the v0.1.2–v0.1.6 tag signatures no longer verify against the current
allowed-signers list.** Those releases are still published and their artifacts
are still covered by the firmware signature, the checksums and the GitHub
attestations, all of which verify normally. It is the *tag* authenticity of
those five releases that can no longer be re-established, because the key that
made those signatures no longer exists anywhere.

For the record, the retired signer was:

```text
ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIO3MOr8j99sQZdQCz0gm2mRzRRWOeA1d/UZhOyn1bgol
```

This is a public key: it verifies the historical signatures for anyone who
adds it to their own allowed-signers file, and it cannot make new ones.

**The firmware signing key was not affected.** It lives in the
`MINIMALROUTER_RELEASE_PRIVATE_KEY_B64` Actions secret rather than on a
maintainer machine, and has not been rotated — so an appliance verifies a
v0.1.7 update with the key it already trusts, and no appliance-side action is
required by this rotation.

## v0.1.7 release artifacts

The Beta release publishes:

### Golden installer

- `minimalrouter-0.1.7-amd64.iso`
- `minimalrouter-0.1.7-amd64.iso.sha256`

### Signed update/install distributions

For AMD64 and ARM64:

- self-contained Alpine distribution archive;
- Ed25519-signed manifest covering every regular file in the extracted payload;
- SPDX JSON SBOM.

### Shared verification material

- `SHA256SUMS` covering both archives, both manifests, both SBOMs and the Golden ISO;
- GitHub artifact attestations for release archives/SBOMs, signed manifests,
  checksums and the tested Golden ISO.

## Golden ISO release rule

The release ISO is not rebuilt from an unsigned development distribution.

The workflow order is:

1. build AMD64 and ARM64 release distributions with tag/commit/build metadata;
2. sign the extracted distributions with the protected Ed25519 firmware key;
3. embed `firmware-signing.pub` into the signed distributions;
4. repack the signed tarballs;
5. build the Golden ISO from the **already signed AMD64 distribution** using:

```text
MINIMALROUTER_USE_EXISTING_DIST=1
MINIMALROUTER_REQUIRE_SIGNED_DIST=1
```

6. fail if `firmware-signing.pub` is absent;
7. boot and fully install that release ISO in QEMU;
8. publish only after the installed appliance passes the full E2E markers.

This ensures a fresh ISO installation has the same pinned firmware verification
trust anchor required for later `router-update` staging.

## Release ISO E2E gate

Before publication, the workflow proves the release ISO can:

- boot the production live flasher;
- verify and copy its Golden image to a blank 8 GiB VirtIO disk;
- reboot into the installed `linux-lts` appliance;
- complete firstboot over serial;
- accept a root login on the installed `ttyS0` getty;
- accept a real password-authenticated trusted-LAN SSH login;
- apply `192.168.1.1/24`;
- expose the expected SSH nftables rule and listener;
- create canonical MinimalRouter state;
- match running kernel to `/lib/modules`;
- start router services and readiness state;
- listen on Dashboard/API TCP/8443;
- cold-boot the installed appliance without the ISO and prove firstboot does not re-enter;
- recover `routerd` through service supervision after a forced crash;
- warm-reboot and return to the same ready state;
- refuse to overwrite an existing MinimalRouter installation;
- reject an undersized 4 GiB target before destructive writes begin.

A failed release E2E prevents publication.

## Firmware signing key

The current release workflow uses a GitHub Actions protected Ed25519 signing
secret. It is decoded into a mode `0600` temporary file, used only to sign
canonical manifests, and removed before publication.

This is **not an offline key**. The protected `production-release` environment,
required reviewer approval, pinned Actions and SSH-signed tag verification reduce
risk, but a future trust-model migration should consider isolated/hardware-backed
signing or a reviewed keyless design.

## Local trust anchor

The appliance verifies A/B update payloads with the 32-byte Ed25519 public key at:

```text
/etc/minimalrouter/firmware-signing.pub
```

This root-controlled file is the local update trust anchor. A key carried inside
a downloaded manifest is informational and is never accepted as a new trust root.

The v0.1.7 release Golden ISO is required to install this trust anchor from the
signed release payload.

## Verify a downloaded release

At minimum, verify the checksum before attaching the ISO:

```sh
sha256sum -c minimalrouter-0.1.7-amd64.iso.sha256
```

For the complete downloaded release set:

```sh
sha256sum -c SHA256SUMS
```

With GitHub CLI, verify provenance/attestation for the selected artifact, for
example:

```sh
gh attestation verify minimalrouter-0.1.7-amd64.iso \
  -R vladimirperovic/minimalrouter

gh attestation verify minimalrouter-linux-amd64.tar.gz \
  -R vladimirperovic/minimalrouter
```

For archive updates, the appliance's pinned Ed25519 verification remains the
final local authorization boundary. GitHub attestations supplement it; they do
not replace it.

## Staging an archive update

Extract the matching release archive into a private local directory and stage it
with its signed manifest:

```sh
router-update stage \
  --dir /root/minimalrouter-release \
  --manifest /root/minimalrouter-linux-amd64.manifest.json
```

Staging:

1. verifies the manifest against the pinned Ed25519 key;
2. rejects unsafe paths, symlinks and non-regular files;
3. verifies every SHA-256 hash;
4. copies only manifest-covered files to a private temporary slot;
5. re-verifies the copied payload;
6. atomically marks the completed slot pending.

Release-provided shell scripts are never executed by the update manager.

## Activation

Staging does not change the active version. Review and activate explicitly:

```sh
router-update status
router-update activate --version 0.1.7 --confirm ACTIVATE-UPDATE
```

After activation, verify router services, LAN management, WAN, DHCP/DNS,
firewall, WireGuard and audit state before deleting the previous slot.

## Updating from the dashboard

From v0.1.7 the same staging and activation can be driven from the dashboard's
*New version* button instead of a shell. It is the identical trust boundary,
not a shortcut around it: the payload is verified against the release manifest
using the pinned local key at `/etc/minimalrouter/firmware-signing.pub`, and a
release is only offered when both the architecture archive and its signed
manifest are published. Nothing about the verification is delegated to the
release listing, which is used only to learn that a version exists.

## Rollback

From local recovery:

```sh
router-update rollback --confirm ROLLBACK-UPDATE
```

Rollback restores the previous verified slot pointer. Configuration rollback is a
separate snapshot/recovery operation.

## Key compromise

If the firmware release private key may have been exposed:

1. stop publishing releases;
2. remove the repository secret;
3. publish a security advisory identifying affected versions/fingerprint;
4. generate a new key pair in a safer environment;
5. distribute the replacement public key through a separately authenticated
   recovery procedure, never through a manifest signed only by the compromised key;
6. withdraw affected releases where practical;
7. preserve logs and attestations for investigation.

A normal online update must never silently rotate its own root of trust.
