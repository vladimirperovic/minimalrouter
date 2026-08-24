# GitHub QEMU torture lab

This document describes the GitHub-hosted lab used by PR #115. It runs the
existing 153 scenario scripts against a real Golden ISO appliance booted in
QEMU TCG, with disposable Debian ISP/PPPoE, simulator and LAN-client peers.
The scenario scripts remain unchanged; only the transport is replaced inside a
temporary shadow worktree.

## Run it on GitHub

1. Open the repository's **Actions** tab.
2. Select **GitHub QEMU torture lab**.
3. Select **Run workflow**, choose the branch to test (normally
   `audit/github-torture-lab` or the PR branch), and start it.
4. Wait for the job **153-scenario QEMU torture lab**.
5. Download the `github-qemu-torture-<commit>` artifact if the job fails. It
   contains `summary.json`, `torture.log`, per-scenario results/logs and the
   ISO installation log.

The workflow also runs automatically for pull requests and pushes to `main`
when the lab-related paths change. It first builds the Golden ISO, flashes a
blank 8 GiB disk, completes firstboot, boots the installed appliance and then
runs all 153 scenarios.

Direct links:

- [PR #115](https://github.com/vladimirperovic/minimalrouter/pull/115)
- [Workflow file](https://github.com/vladimirperovic/minimalrouter/blob/audit/github-torture-lab/.github/workflows/torture-lab.yml)
- [Actions runs](https://github.com/vladimirperovic/minimalrouter/actions/workflows/torture-lab.yml)

## What a passing result means

A pass requires the ISO build/install smoke checks and all 153 scenario scripts
to finish with exit code 0. The artifact summary must report:

```json
{
  "inventory": 153,
  "scenario_passes": 153,
  "scenario_failures": 0,
  "result_files": 153,
  "runner_rc": 0
}
```

This is automated regression evidence for the isolated QEMU lab. It is not a
claim of physical NIC, ISP, Proxmox, hardware or long-term soak validation.

## Current result

Latest recorded run:

- Run: [32703554600](https://github.com/vladimirperovic/minimalrouter/actions/runs/32703554600)
- Workflow checkout: merge commit `a4afbd2396a7334b9e00d66dbe2807ec21d6b827` (PR head `3dddf05a7b4914ca0839012d05db05fe1f7cd83a`)
- Result: **failed before scenario execution**
- Flash and firstboot passed: `FULL_ISO_INSTALL_OK` and `INSTALLED_SSH_OK`.
- The generic ISP kernel, PPP runtime, real PPPoE session and LAN DHCP lease all passed.
- Failure: `config-confirm.json` records `Transaction confirmation failed`. The baseline enables the outbound WireGuard tunnel, whose confirmation requires a completed handshake. The simulator's return route to the appliance PPP address was installed only after confirmation, so the handshake could not complete. The ignored HTTP conflict allowed the 90-second safety timer to roll the candidate back before topology smoke. No scenarios ran. Artifact `9512682056` retains the evidence.
- Fix: the simulator return route is now installed before configuration apply. Bootstrap waits for the outbound tunnel handshake and accepts confirmation only after an HTTP 200 response with a committed transaction; otherwise it fails immediately with retained diagnostics.

The next run is expected to start automatically from the PR branch update. It must again pass flash/firstboot before the 153 scenarios are attempted.
