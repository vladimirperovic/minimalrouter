# GitHub QEMU torture lab

This document describes the GitHub-hosted lab used by PR #115. It runs the
existing 153 scenario scripts against a real Golden ISO appliance booted in
QEMU TCG, with disposable Debian ISP/PPPoE, simulator and LAN-client peers.
The scenarios run through a hosted-QEMU transport inside a temporary shadow
worktree, with lab-specific assertions kept alongside the shared scenarios.

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

Latest complete-suite run:

- Run: [32735068105](https://github.com/vladimirperovic/minimalrouter/actions/runs/32735068105)
- Workflow checkout: PR head `5b58931d64722855e59dbf1a565cebae42b8fdda`
- Selection: complete `all` inventory (`153` expected)
- Result: **failed** (`14` passes, `8` failures, `22` result files, runner exit `1`)
- Flash and firstboot passed before scenario execution.
- The first failure was `13-mtu-issues`: PPPoE recovered and LAN/DNS traffic
  passed at the reduced MTU, but the assertion inspected the client-side PPP
  interface even though Linux does not expose the peer's MRU through that
  interface MTU field. Focused run
  [32782438366](https://github.com/vladimirperovic/minimalrouter/actions/runs/32782438366)
  (artifact `9541582145`) proved the same for the server-side PPP interface.
  The corrected assertion now verifies the ISP's active `mtu 1400` and
  `mru 1400` options while retaining the real PPPoE, LAN-internet and DNS
  checks.
- Scenario `22-service-crash` subsequently left `routerd` unhealthy, so the
  baseline gate correctly refused scenarios `23` through `153` without
  injecting further faults. Artifact `9534511144` retains the summary,
  scenario log and per-failure postmortems.

Latest passing MTU regression:

- Run: [32786645766](https://github.com/vladimirperovic/minimalrouter/actions/runs/32786645766)
- Workflow checkout: PR head `3d32ee5587c7f9114178d5df38a019cd9d595e2f`
- Selection: focused `13-mtu-issues`
- Result: **passed** (`1` pass, `0` failures, `1` result file, runner exit `0`)
- Artifact `9542788032` proves the active `mtu 1400`/`mru 1400` ISP fault,
  PPPoE recovery, working LAN internet and DNS, and restoration to MTU 1492.
- The next complete-suite failure, `15-dns-dhcp`, exposed a missing shared LAN
  client-interface/lease-renewal contract: the hosted client uses `eth1`, not
  the Proxmox client's `eth0`, and its compatibility DHCP shim must use
  POSIX-shell argument handling.

Latest passing DHCP regression:

- Run: [32790475202](https://github.com/vladimirperovic/minimalrouter/actions/runs/32790475202)
- Workflow checkout: PR head `2451db77b6a4e2ceb993267a91b9c8f951d1fa8e`
- Selection: focused `15-dns-dhcp`
- Result: **passed** (`1` pass, `0` failures, `1` result file, runner exit `0`)
- Artifact `9544038344` proves DNS/DHCP service recovery and a real renewed LAN
  client lease through the shared interface-aware DHCP helper.
- The next complete-suite failures, `17-endpoint-blackhole` and `18-wg0`,
  recorded zero received bytes and no `wg0` handshake even while `wg1` worked.
  The generated default-deny output chain allowed the `wg1` endpoint port but
  omitted outbound `wg0` handshake packets entirely. The fix permits only UDP
  from the configured `wg0` listen port to each enabled peer's exact IPv4
  endpoint and port, on WAN interfaces only. The blackhole scenario now reads
  the actual configured endpoint instead of its obsolete private lab address,
  and all WireGuard scenarios share a real recent-handshake assertion.
- Focused run
  [32794200730](https://github.com/vladimirperovic/minimalrouter/actions/runs/32794200730)
  (artifact `9545262234`) proved that fix restored the `wg0` handshake and
  real tunnel traffic after the endpoint blackhole was removed. Its only
  remaining failure was a 90-second freshness threshold despite a live tunnel
  and a 98-second-old handshake. WireGuard normally rekeys after 120 seconds
  and rejects keys after 180 seconds, so the shared check now treats 180
  seconds as the minimum valid freshness window while still rejecting absent,
  future or expired handshakes.

Latest passing WireGuard regression:

- Run: [32797536133](https://github.com/vladimirperovic/minimalrouter/actions/runs/32797536133)
- Workflow checkout: PR head `af799ce096720054ae25c5b2e833e45c0bbd0a3a`
- Selection: focused `17-endpoint-blackhole`
- Result: **passed** (`1` pass, `0` failures, `1` result file, runner exit `0`)
- Artifact `9546304727` proves the correctly targeted endpoint blackhole,
  preserved PPPoE/LAN/DNS service, recovered `wg0` tunnel traffic, a valid
  handshake, and production isolation.
- The next complete-suite failure, `20-extralan-isolation`, reached the
  isolated service segment from the router, but the simulator's recorded route
  table had no return path for the main LAN. Its HTTP replies therefore took
  the management default gateway. The hosted lab now routes `192.168.1.0/24`
  back through `10.78.0.1` on the isolated interface; the scenario still
  verifies that unsolicited ExtraLAN-to-LAN and ExtraLAN-to-WAN traffic is
  denied.

Latest passing ExtraLAN regression:

- Run: [32800706746](https://github.com/vladimirperovic/minimalrouter/actions/runs/32800706746)
- Workflow checkout: PR head `2905ce63591795b7d9e3882c50c9366f4a3fb199`
- Selection: focused `20-extralan-isolation`
- Result: **passed** (`1` pass, `0` failures, `1` result file, runner exit `0`)
- Artifact `9547307398` proves the router can reach the service segment, the
  allowed main-LAN client can reach the isolated HTTP service, ExtraLAN cannot
  initiate traffic to either the real LAN client or WAN, and the firewall,
  canonical-state and production-isolation invariants remain intact.
- The next complete-suite failure, `21-router-reboot`, completed the reboot and
  restored PPPoE, LAN, DNS, firewall and Internet service. Its only failed
  assertion was LAN lease renewal through the same missing hosted-client DHCP
  helper already corrected and proven by focused scenario 15. The hosted TCG
  guest required roughly two hours to complete its actual reboot; runner and
  guest logs also use different time zones, so durations must be compared
  within the same clock source.

Latest passing router-reboot regression:

- Run: [32803749049](https://github.com/vladimirperovic/minimalrouter/actions/runs/32803749049)
- Workflow checkout: PR head `c6e55777a7665f7fa3bc71f910d4fdbf3d859eef`
- Selection: focused `21-router-reboot`
- Result: **passed** (`1` pass, `0` failures, `1` result file, runner exit `0`)
- Artifact `9550683337` proves a real router restart, recovered PPPoE, LAN,
  DNS, firewall and Internet service, a renewed LAN-client DHCP lease,
  canonical/last-good convergence, non-hybrid runtime and production isolation.
- The next complete-suite failure, `22-service-crash`, killed both management
  processes. The privileged helper rebuilds verified runtime before reopening
  its IPC socket, but `routerd` immediately exhausted its bounded supervisor
  retries while that socket was unavailable. Its startup now waits inside the
  existing fail-closed reconciliation deadline for a live helper socket before
  reconciling. Lab emergency cleanup also restarts stopped management services
  in dependency order, and postmortems retain their stderr diagnostics.

Latest passing carrier regression:

- Run: [32729865737](https://github.com/vladimirperovic/minimalrouter/actions/runs/32729865737)
- Workflow checkout: PR head `025ba00a6fdd57d5ac9cf2c86ec1a17995a87939`
- Selection: focused `03-wan-carrier`
- Result: **passed** (`1` pass, `0` failures, `1` result file, runner exit `0`)
- Flash and firstboot passed: `FULL_ISO_INSTALL_OK` and `INSTALLED_SSH_OK`.
- Scenario `03-wan-carrier` proved carrier loss, PPPoE loss and automatic recovery, restored LAN-to-simulator HTTP, canonical/last-good convergence and the production isolation invariant. Artifact `9522676847` retains the passing evidence.
- The fix restores the hosted-only simulator routes after carrier restoration and lab reset; the generic reset also raises the ISP access interface so an interrupted carrier scenario cannot poison later tests.

The carrier regression is green. The complete lab remains red while the
focused failures from run `32735068105` are repaired and rerun.
