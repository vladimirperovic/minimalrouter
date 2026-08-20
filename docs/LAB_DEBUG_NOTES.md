# Lab debug findings — sanitized historical notes (2026-08-08)

> **Historical and sanitized.** These notes preserve reusable debugging lessons
> from the isolated lab without publishing live topology, VM IDs, bridge names,
> credentials, private hostnames, MAC addresses, local paths, or operator access
> details. Current v0.1.5 evidence belongs in
> [`CURRENT_VALIDATION.md`](CURRENT_VALIDATION.md).

## Reusable deployment/debug lessons

1. **Treat guest-agent output as an interface contract.** Different wrappers may
   expose command output as decoded text or encoded data. Test the wrapper and
   fail explicitly rather than silently treating empty output as success.

2. **Run one PPPoE access concentrator instance.** A manually spawned simulator
   can conflict with the managed service and produce misleading reconnect loops.
   Keep one lifecycle owner and use synthetic test credentials only.

3. **PPPoE secret syntax matters.** PAP/CHAP fixtures must match the simulator's
   expected peer name and address policy. Never copy ISP/customer credentials
   into a public fixture or debug note.

4. **Readiness is stronger than process existence.** A supervised `routerd`
   process is not proof that the management plane is usable. Test the readiness
   marker/API and preserve the fail-closed startup contract.

5. **Do not let a base distribution network config compete with MinimalRouter.**
   The appliance owns its intended interfaces. Duplicate DHCP/static ownership
   can add long boot delays or route replies through the wrong interface.

6. **Use the product update path.** Current lab deployments should exercise
   signed `router-update` staging/activation/rollback instead of swapping live
   binaries or mounting the guest disk to patch it behind the application's back.

7. **Verify executable permissions after preparing a lab payload.** A restrictive
   extraction environment can make a staged payload unreadable/executable by the
   unprivileged daemon and create a misleading mixed bootstrap/slot runtime.

8. **Drive hard-power tests from the hypervisor/test harness.** The unprivileged
   router process deliberately cannot power off the host. Fault hooks should
   create a deterministic pause/marker while the external harness performs the
   abrupt stop.

9. **Match complete state text, not ambiguous substrings.** Assertions such as
   `grep staged` can accidentally match a negative phrase like “not staged”. Use
   exact positive states or structured output.

10. **Process names may reflect slot executables.** A/B dispatchers can make a
    naïve process-name grep miss the real binaries. Validate the executable path,
    selected slot, service readiness and API behavior together.

11. **Configuration confirmation must be tested as a protocol.** Capture the
    pending transaction ID, confirm the exact transaction within its window, and
    verify canonical/helper state converge. Do not infer success from a dropped
    HTTP connection during a disruptive network apply.

12. **Optional subsystems must not poison unrelated local saves.** A broken DDNS,
    Wi-Fi, proxy or remote tunnel should remain degraded without forcing global
    recovery when a local DNS/DHCP-only change can still be applied safely.

## Privacy rule for future lab notes

Public lab notes may contain only synthetic/documentation examples. Keep all of
the following outside the public repository:

- real PPPoE/DDNS/admin/root credentials;
- public IP addresses and private operator hostnames;
- VM IDs, bridge mappings and hypervisor inventory;
- MAC addresses and household device inventory;
- WireGuard private/preshared keys and QR/profile material;
- local access tunnels, passwords or management routes;
- raw logs, generated configs, databases, backups and packet captures.

If a credential-like value ever appears in public history, removing it from the
current tree does not make it secret again. Rotate/revoke the credential if it
was real.

## Where to continue testing

Use [`PROXMOX_ISOLATED_LAB.md`](PROXMOX_ISOLATED_LAB.md) for the reproducible
matrix, [`TESTING.md`](TESTING.md) for project-wide testing rules, and
[`CURRENT_VALIDATION.md`](CURRENT_VALIDATION.md) for what the current v0.1.5 tree
has actually proven.
