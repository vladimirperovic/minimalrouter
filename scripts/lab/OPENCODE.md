# OpenCode lab worker

OpenCode runs in LXC 160 as an orchestrator. It is connected only to the
management/production bridge and reaches Proxmox over SSH. Fault traffic is
generated inside VMs 150, 151, 153 and 154 on `vmbr-lab-*`; LXC 160 must never
be attached directly to those bridges.

## One-scenario workflow

From a clean, current checkout in `/projekti/minimalrouter`:

```sh
sh scripts/lab/agent-run-next.sh status
sh scripts/lab/agent-run-next.sh next
```

`next` runs exactly one scenario. On failure, inspect its output and evidence
under `scripts/lab/results/`, fix the product or test, run the normal unit
tests, then invoke `next` again. A failed scenario remains next until it passes.
The wrapper keeps its own durable ledger in
`scripts/lab/results/.agent-ledger/`, so multi-phase scenarios are counted once.

Do not launch `lab-run.sh all` from the autonomous agent. Do not run a second
scenario while the lock in `/tmp/minimalrouter-lab-agent.lock` exists. Do not
copy a newer checkout over a dirty working tree; commit, stash or review the
agent's existing work first.

The runner performs a Proxmox topology preflight before every scenario and
refuses to proceed unless the targets are the documented isolated lab VMs.
pfSense VM 106 and `vmbr0` are read-only production invariants, never fault
targets.

See `COVERAGE.md` for the current risk estimate and the highest-value gaps.
