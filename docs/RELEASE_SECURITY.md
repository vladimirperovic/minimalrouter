# Release security, verification, update and rollback

Minimal Router OS uses separate controls for distribution integrity and local
appliance activation. No single GitHub checksum, workflow result, or downloaded
manifest is sufficient on its own.

## Release artifacts

A version tag matching `vMAJOR.MINOR.PATCH` starts the release workflow.
The workflow refuses lightweight or unverifiable tags. Maintainers must create
an SSH-signed annotated tag whose signer appears in the protected
`MINIMALROUTER_RELEASE_ALLOWED_SIGNERS` secret.

For AMD64 and ARM64, a release publishes:

- a self-contained Alpine distribution archive;
- an Ed25519-signed manifest covering every regular file in the extracted
  update payload;
- an SPDX JSON software bill of materials;
- one `SHA256SUMS` file covering archives, manifests, and SBOMs;
- GitHub artifact attestations binding each archive and SBOM to the workflow,
  repository, commit, and tag that produced it.

The current release workflow uses a GitHub Actions protected Ed25519 signing
secret. It is decoded into a mode `0600` temporary file, used only to sign
canonical manifests, and removed before publication. This is **not an offline
key**: the protected `production-release` environment, required reviewer
approval, immutable Action pins, and SSH-signed tag verification reduce risk,
but a future trust-model migration should move signing to an isolated machine,
hardware-backed key, or a reviewed keyless design.

## Trust anchors

The appliance verifies updates with a 32-byte Ed25519 public key installed at:

```text
/etc/minimalrouter/firmware-signing.pub
```

This root-controlled file is the local update trust anchor. A public key carried
inside a downloaded manifest is informational and is never accepted as a new
trust root.

The signing private key must be generated and stored outside the repository.
Keep an offline backup and a documented revocation/replacement procedure. Never
store it in source, release assets, issue attachments, workflow logs, router
backups, or ordinary administrator workstations.

## Online verification before installation

From a trusted workstation:

```sh
sha256sum -c SHA256SUMS
gh attestation verify minimalrouter-linux-amd64.tar.gz \
  -R vladimirperovic/minimalrouter
gh attestation verify minimalrouter-linux-amd64.tar.gz \
  -R vladimirperovic/minimalrouter \
  --predicate-type https://spdx.dev/Document/v2.3
```

Repeat for the selected architecture. Confirm the release tag, source commit,
workflow identity, and expected repository before transferring files to the
router.

GitHub attestations supplement the appliance signature. They do not replace the
pinned Ed25519 verification performed locally by `router-update`.

## Staging on the router

Extract the archive into a private local directory, transfer the matching signed
manifest, and stage it:

```sh
router-update stage \
  --dir /root/minimalrouter-release \
  --manifest /root/minimalrouter-linux-amd64.manifest.json
```

Staging is fail-closed and performs these checks before committing an inactive
slot:

1. load the operating-system-pinned Ed25519 trust anchor and verify the signed
   manifest against it;
2. require a complete Minimal Router appliance payload with exactly one complete
   supported architecture binary set, and reject a package that does not match
   the running host architecture;
3. reject unsafe manifest paths, symlinks, non-regular runtime files, root-only
   daemon/script modes, and web assets that the unprivileged runtime cannot
   read;
4. enforce the signed `compatibility.json` bootstrap ABI, config schema and
   runtime protocol contract;
5. verify every manifest SHA-256 hash in constant time;
6. copy **only** manifest-covered files into a private temporary slot with
   normalized directory and file permissions independent of the caller's umask;
7. verify the copied slot again, including runtime modes, compatibility metadata
   and all signed content hashes;
8. atomically rename the completed slot and mark it pending.

Release-provided shell scripts are never executed by the update manager.
Unmanifested files in an extracted archive are never copied into an A/B slot.

## Stable bootstrap and activation compatibility

Ordinary A/B activation deliberately does **not** rewrite root-owned operating
system integration files. Before the active pointer can move,
`router-update` compares the candidate release byte-for-byte and mode-for-mode
against the installed stable runtime layout, including:

- `compatibility.json`;
- `slot-exec`;
- `routerd`, `router-applyd` and PPPoE OpenRC integration;
- sysctl, module-load, logrotate and PPP QoS hook files;
- the architecture-specific bootstrap `router-update` and `router-recovery`
  binaries.

If any of those files differ, A/B activation is refused and the operator must run
the full signed distribution installer. This prevents a new slotted daemon from
silently depending on a bootstrap/init/recovery layer that was not upgraded with
it. `router-update` and `router-recovery` themselves always execute from the
stable bootstrap path rather than from the slot under test.

## Activation and health confirmation

Staging does not change the active version. Review status and activate explicitly:

```sh
router-update status
router-update activate \
  --version 1.2.3 \
  --confirm ACTIVATE-UPDATE
```

After all runtime-layout compatibility checks pass, the current slot pointer is
replaced atomically and the previous verified slot is retained. Activation then
stops `routerd`, restarts `router-applyd` from the new current slot, starts
`routerd` from that same slot, and requires the OpenRC service/readiness checks to
succeed. If the new pair fails to start cleanly, the updater automatically moves
the slot pointer back and restarts the previous pair. A failure of both the new
slot and automatic rollback is reported as a hard recovery error rather than a
successful activation.

After activation, also verify the network behavior appropriate to the release:

- LAN management remains reachable;
- WAN, DHCP, DNS, firewall, WireGuard, and device-profile policy behave as
  expected;
- the audit log contains the expected update events;
- no unexpected listener or outbound connection appears.

Do not delete the previous slot until this validation is complete.

## Rollback

From the local console:

```sh
router-update rollback --confirm ROLLBACK-UPDATE
```

Rollback atomically restores the previous verified slot pointer and restarts the
runtime pair from it. It does not restore configuration snapshots; use
`router-recovery snapshots` and `router-recovery restore-snapshot` when the
problem is configuration rather than software payload.

## Key compromise

If the release private key may have been exposed:

1. stop publishing releases immediately;
2. remove the repository secret;
3. publish a security advisory identifying affected versions and key fingerprint;
4. generate a new offline key pair;
5. distribute the replacement public key through a separately authenticated
   recovery procedure, not through a manifest signed by the compromised key;
6. revoke or withdraw affected releases where practical;
7. preserve logs and attestations for investigation.

A normal online update must never silently rotate its own root of trust.
