# Development

This is the initial development contract. Exact commands will be added when the
Go module, web application, and image builder are scaffolded.

## Prerequisites

- Git
- Current pinned Go toolchain
- Node.js and pnpm versions pinned by the repository
- QEMU/KVM or another supported hypervisor
- Alpine image-building tools
- nftables, pppd, dnsmasq, WireGuard tools, and cloudflared inside the test VM

Do not require these networking services on a developer's host. Integration and
end-to-end tests run in an isolated Linux VM or namespace environment.

## Intended local workflow

```text
make bootstrap   # verify/install project-local development dependencies
make generate    # generate API types, mocks, and deterministic artifacts
make lint        # Go and frontend static checks
make test        # fast unit tests
make integration # isolated real-component tests
make image       # reproducible Alpine development image
make e2e         # boot image and exercise management/apply flows
make check       # all pre-commit checks that do not require special hardware
```

The `Makefile` is a discoverable front door; underlying scripts must also work
in CI without an interactive shell.

## Version pinning

Before application scaffolding:

- Pin the Alpine stable branch and package repository URLs.
- Pin Go with a checked-in toolchain declaration.
- Pin Node.js and pnpm.
- Commit `go.sum` and the frontend lockfile.
- Record image-builder versions.

Automated dependency updates must run tests and must not merge unattended.

## Configuration development

For each configurable feature, implement in this order:

1. Canonical domain model
2. Schema and cross-field validation
3. Deterministic generator
4. Component preflight adapter
5. Apply and rollback behavior
6. REST API and OpenAPI contract
7. UI

The UI must not be the first or only implementation of a configuration rule.

## Generated artifacts

Generated service files:

- Are reproducible from canonical state.
- Include a generated-file warning.
- Use stable ordering to keep diffs meaningful.
- Are created with explicit permissions.
- Are validated before installation.
- Are never edited in place.

Golden files are appropriate for nftables, dnsmasq, pppd, WireGuard, and
cloudflared generators, but tests must normalize only truly nondeterministic
values.

## Database migrations

- Migrations are append-only after merge.
- Every migration has an upgrade test from the oldest supported schema.
- Destructive migrations require backup/restore and rollback analysis.
- Startup either completes a migration transaction or leaves the previous
  database valid.
- A newer unsupported schema fails closed with a useful local-console message.

## Logging

Use structured logging with stable event names. Do not log raw API bodies,
configuration objects, environment variables, command lines containing
credentials, or generated secret files.

## Source layout

Follow the proposed layout in `ARCHITECTURE.md`. Keep OS/component adapters
behind small interfaces so domain validation and planning can be tested without
root privileges.

## Pull request evidence

Changes affecting routing, security, apply, boot, or performance include:

- Test commands and results
- Failure/rollback scenario tested
- Security impact
- Before/after boot time, memory, or throughput where relevant
- A redacted generated-config diff when useful

## Local safety

Never run integration tests against the developer's active host firewall,
routes, DNS, or network interfaces. Test scripts must refuse to run outside the
recognized disposable environment unless an explicit development-only override
is provided.
