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
   `audit/github-torture-lab` or the PR branch), leave **scenario** as `all`
   for the complete suite (or enter one scenario name/number for a focused
   diagnostic run), and start it.
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

- Run: [32729865737](https://github.com/vladimirperovic/minimalrouter/actions/runs/32729865737)
- Workflow checkout: PR head `025ba00a6fdd57d5ac9cf2c86ec1a17995a87939`
- Selection: focused `03-wan-carrier`
- Result: **passed** (`1` pass, `0` failures, `1` result file, runner exit `0`)
- Flash and firstboot passed: `FULL_ISO_INSTALL_OK` and `INSTALLED_SSH_OK`.
- Scenario `03-wan-carrier` proved carrier loss, PPPoE loss and automatic recovery, restored LAN-to-simulator HTTP, canonical/last-good convergence and the production isolation invariant. Artifact `9522676847` retains the passing evidence.
- The fix restores the hosted-only simulator routes after carrier restoration and lab reset; the generic reset also raises the ISP access interface so an interrupted carrier scenario cannot poison later tests.

The focused regression is green. A complete 153-scenario run is still required before the lab is considered green.
