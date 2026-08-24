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

- Run: [32695044359](https://github.com/vladimirperovic/minimalrouter/actions/runs/32695044359)
- Workflow checkout: merge commit `725332f0c61dceafe24abd1075f1e8427f54d9b4` (PR head `ebe22fbe8a7dfd56dd78965b49f869994dc6ba2b`)
- Result: **failed before scenario execution**
- Flash and firstboot passed: `FULL_ISO_INSTALL_OK` and `INSTALLED_SSH_OK`.
- Failure: reboot synchronization worked, but Debian's `6.12.101+deb13-cloud-amd64` kernel still had no loadable `ppp_generic`; the preflight and ISP payload both recorded that exact failure. The harness nevertheless continued because its initial `cleanup` call ran `set +e` in the parent shell, unintentionally disabling fail-fast behavior for the remainder of the job. PPPoE discovery later timed out waiting for PADO. No scenarios ran. Artifact `9509276093` retains the evidence.
- Fix: install Debian's generic `linux-image-amd64` kernel for the disposable PPPoE ISP and run cleanup in a subshell so its relaxed error handling cannot leak into the harness.

The next run is expected to start automatically from the PR branch update. It must again pass flash/firstboot before the 153 scenarios are attempted.
