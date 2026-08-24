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

- Run: [32460091098](https://github.com/vladimirperovic/minimalrouter/actions/runs/32460091098)
- Workflow checkout: merge commit `25190e9cfffbae569c444d715e5985867b0c030e` (PR head `3f30eda984f8670817b2651ba12dc76152e8ef31`)
- Result: **failed before scenario execution**
- Flash and firstboot passed: `FULL_ISO_INSTALL_OK` and `INSTALLED_SSH_OK`.
- Failure: the ISP needed a cloud-kernel reboot. The job scheduled that reboot and immediately used an SSH readiness probe, which could succeed during systemd's reboot delay. Provisioning then printed only `== packages ==`; none of the `interface detection`, `PPP runtime`, `pppoe-server` or `ISP-LAB ready` markers appeared. The later router-side discovery timed out waiting for PADO and no scenario result files were produced. Artifact `9439380509` retains the topology, discovery and serial evidence.
- Fix: the bootstrap now requires the ISP SSH endpoint to disappear before waiting for it to return, verifies the new kernel exposes `ppp_generic`, and checks the active PPPoE service plus `auth=good` immediately after provisioning.

The next run is expected to start automatically from the PR branch update. It must again pass flash/firstboot before the 153 scenarios are attempted.
