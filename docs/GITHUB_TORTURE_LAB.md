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

- Run: [32457183474](https://github.com/vladimirperovic/minimalrouter/actions/runs/32457183474)
- Workflow checkout: merge commit `7236e0a0a7dd37308b7b2dc2b65da59e01091cd4` (PR head `3a6279a3abf549f334cc7c4cfacdbc1be68ff265`)
- Result: **failed before scenario execution**
- Flash and firstboot passed: `FULL_ISO_INSTALL_OK` and `INSTALLED_SSH_OK`.
- Failure: ISP provisioning stopped with `ERROR: ISP-LAB kernel did not load ppp_generic`. The running ISP kernel was `6.12.101+deb13-cloud-amd64`; a `/lib/modules` directory existed, but the usable `ppp_generic` module was not available to the payload's suppressed `modprobe` attempts. PPPoE discovery therefore never reached the lab ISP and no scenario result files were produced.
- Fix: the ISP payload now installs `kmod`, and the QEMU bootstrap checks for both `modprobe` and an actually discoverable `ppp_generic` module with `modprobe -n` before deciding that no kernel preparation is needed.
- Fix commits: `28fc020da8b59ab7d271855d40b76a8db3301476` and `ee1ae55647dc3d2a8e57827772a241223a08add2`.

The next run is expected to start automatically from the PR branch update. It must again pass flash/firstboot before the 153 scenarios are attempted.
