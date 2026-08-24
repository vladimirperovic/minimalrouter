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

- Run: [32707894559](https://github.com/vladimirperovic/minimalrouter/actions/runs/32707894559)
- Workflow checkout: merge commit `d7c6e428419a78b250000f2f30cec799329d86c9` (PR head `f869fb5eb12b914e8d15a61d43ede97ff7c8e9e0`)
- Result: **failed in scenario 03** (`2` passes, `1` failure, `3` result files, runner exit `1`)
- Flash and firstboot passed: `FULL_ISO_INSTALL_OK` and `INSTALLED_SSH_OK`.
- Bootstrap, topology smoke, scenarios `01-pppoe-stop-start` and `02-pppoe-auth-failure` passed.
- Failure: after scenario `03-wan-carrier` lowered and restored the disposable ISP access NIC, the appliance recreated `ppp0` with `10.250.0.50` and its default route, but LAN-to-simulator HTTP never recovered. Every later scenario was correctly refused by the unhealthy baseline. Artifact `9517516613` retains the summary, scenario evidence and postmortem.
- Fix: carrier restoration now restarts the disposable ISP's PPPoE server so rp-pppoe reopens its raw discovery socket after the QEMU link flap. Manual dispatch also accepts one scenario name/number, allowing scenario `03` to be proved before another full run.

The next run is expected to start automatically from the PR branch update. It must again pass flash/firstboot before the 153 scenarios are attempted.
