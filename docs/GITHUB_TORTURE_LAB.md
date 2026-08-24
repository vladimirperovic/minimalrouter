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

- Run: [32725133168](https://github.com/vladimirperovic/minimalrouter/actions/runs/32725133168)
- Workflow checkout: PR head `9919906370670c25bd386f0825f7e154f3ecab17`
- Selection: focused `03-wan-carrier`
- Result: **failed** (`0` passes, `1` failure, `1` result file, runner exit `1`)
- Flash and firstboot passed: `FULL_ISO_INSTALL_OK` and `INSTALLED_SSH_OK`.
- The focused baseline and all checks through PPPoE recovery passed. LAN-to-simulator HTTP alone remained broken after the carrier cycle. Artifact `9520752321` retains the expanded route, neighbor, PPP and HTTP evidence.
- Evidence: the recreated ISP `ppp0` had the correct `10.250.0.50/32` peer route, but the hosted-only `11.250.0.10/32` and `11.255.0.2/32` simulator routes were absent after `eth1` lost carrier. Traffic therefore followed the ISP's default route instead of the isolated simulator segment.
- Fix: the GitHub transport now restores those two hosted-only routes after carrier restoration and lab reset. The generic reset also raises the ISP access interface so an interrupted carrier scenario cannot poison later tests.

The next run is expected to start automatically from the PR branch update. It must again pass flash/firstboot before the 153 scenarios are attempted.
