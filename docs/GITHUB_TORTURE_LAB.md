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

Last recorded run:

- Run: [32454297997](https://github.com/vladimirperovic/minimalrouter/actions/runs/32454297997)
- Commit: `6fd3d27f516ce621aa6650bf1635a57d00d87c9b`
- Result: **failed before scenario execution**
- Flash and firstboot passed. The target MinimalRouter VM had
  `ppp_generic`, `pppox`, `pppoe` and a correct `/dev/ppp) device.
- Failure: the disposable Debian ISP VM had no `/lib/modules) tree for its
  running kernel. Its `pppoe-server` could discover the router, but every
  spawned `pppd` failed with `/dev/ppp: No such device or address`.
  No scenario result files were produced.
- All other checks on the PR head passed, including the focused PPPoE smoke,
  OpenRC PPPoE smoke, CI, Deep validation, Appliance ISO, Secret scan, CodeQL,
  Service supervision and Performance.

The next fix installs a Debian cloud kernel/module package in the ISP VM,
reboots it once, waits for SSH to return, then runs the idempotent ISP
provisioning. The payload now also fails early unless `ppp_generic) is loaded.
It is queued in commit
`8edc6437d070a8af7a652167260f81a8ccf47417`; replace this section with that
run's exact link and summary when it completes.
