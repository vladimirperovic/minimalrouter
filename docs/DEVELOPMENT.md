<<<<<<< HEAD
# Development

This is the active development contract for the Go 1.25 and React/Vite
implementation.

## Prerequisites

- Git
- Go 1.25-compatible toolchain
- Node.js 22.13+ and pnpm 11 for rebuilding static dashboard assets only
- QEMU/KVM or another supported hypervisor
- Alpine image-building tools
- nftables, pppd, dnsmasq, WireGuard tools, Squid, and iproute2 inside the test
  VM. Wi-Fi AP testing additionally needs `hostapd`, `iw`, and an AP-capable
  radio; Cloudflare DDNS uses `inadyn`. Cloudflare Tunnel and DoH remain
  unsupported.

Do not require these networking services on a developer's host. Integration and
end-to-end tests run in an isolated Linux VM or namespace environment.

## Intended local workflow

```sh
go test ./...
go vet ./...
pnpm --dir web lint
pnpm --dir web build
make build-linux-arm64
make dist-arm64
```

The `Makefile` is a discoverable front door; underlying scripts must also work
in CI without an interactive shell.

## Version pinning

Before application scaffolding:

- Pin the Alpine stable branch and package repository URLs.
- Pin Go with a checked-in toolchain declaration.
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

Golden files are appropriate for nftables, dnsmasq, pppd, WireGuard, Squid,
global blocklist, and QoS generators. Tests must normalize only truly
nondeterministic values.

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
=======
# Development guide

Minimal Router OS is an early alpha Go and React project. The production
appliance runs on Alpine Linux; ordinary unit and frontend work can be done on
macOS or Linux without altering the developer's network.

## Prerequisites

Required for normal development:

- Git
- the Go toolchain declared in `go.mod`
- Node.js 22
- pnpm 11

Required only for appliance/integration work:

- Docker with privileged-container support, or a disposable Linux VM
- Alpine Linux 3.22 repositories
- nftables, pppd, dnsmasq, iproute2, WireGuard tools, and the optional services
  being tested
- an AP-capable radio for real Wi-Fi testing
- a dedicated test account for external DDNS testing

Do not install or activate router services on a developer's active workstation
just to run unit tests.

## Clone and verify

```sh
git clone https://github.com/vladimirperovic/minimalrouter.git
cd minimalrouter

go test -race ./...
go vet ./...

pnpm --dir web install --frozen-lockfile
pnpm --dir web lint
pnpm --dir web build
```

The dashboard development server is optional:

```sh
pnpm --dir web dev
```

Node.js and pnpm are build dependencies only. The deployed appliance serves
static dashboard assets from `routerd`.

## Common Make targets

Use `make help` for the current list. Common targets include:

```sh
make test
make build
make build-linux-amd64
make build-linux-arm64
make dist-amd64
make dist-arm64
make dist
```

Generated archives are local build output and must not be committed.

## Clean Alpine smoke test

The CI smoke test builds the AMD64 distribution, installs it in a clean
privileged Alpine container, starts `router-applyd` and `routerd`, completes the
wizard over HTTPS, and checks forwarding, nftables, dnsmasq, and safe defaults.

Run it only where privileged Docker is expected and isolated:

```sh
make dist-amd64
sh scripts/ci-fresh-install-smoke.sh
```

This test is valuable, but it does not replace a real boot, physical NIC, PPPoE,
power-loss, or external scan test.

## Safe development order

For a configurable feature, implement in this order:

1. canonical configuration model;
2. validation and cross-field policy;
3. deterministic generator;
4. component preflight;
5. privileged apply and rollback behavior;
6. REST API and OpenAPI contract;
7. dashboard workflow;
8. unit, integration, failure, and recovery tests;
9. documentation.

The dashboard must not be the only place a security or configuration rule is
enforced.

## Privileged-code rules

Changes under `cmd/router-applyd`, `internal/apply`, networking generators, or
Alpine packaging require extra review.

- Never execute user-controlled text through a shell.
- Use fixed executable paths and separate argument arrays.
- Reject unknown operations, paths, interfaces, and service names.
- Keep request and generated artifact sizes bounded.
- Preflight before replacing active state.
- Preserve the previous known-good state and rollback path.
- Do not broaden root authority merely to simplify an API handler.
- Record no credentials in process arguments, logs, tests, or fixtures.

## Generated artifacts

Generated service files must:

- be reproducible from canonical state;
- use stable ordering;
- include a generated-file warning;
- have explicit ownership and permissions;
- be syntax-checked before activation;
- be installed atomically where supported;
- never become the canonical source of truth.

Golden-output tests are appropriate when they remain readable and do not hide
important semantic assertions.

## Database migrations

- Migrations are append-only after public merge.
- Startup must complete a migration transaction or leave the previous database
  valid.
- A newer unsupported schema fails closed.
- Destructive migrations require backup, restore, and rollback analysis.
- Tests must cover upgrades from every supported schema version.
- Runtime databases and snapshots never belong in Git.

## Frontend development

Dashboard changes should preserve:

- keyboard navigation and visible focus;
- readable contrast in light and dark modes;
- responsive behavior;
- clear unavailable/disabled states;
- no secret rendering or persistence;
- the API as the source of configuration truth.

Screenshots in pull requests and documentation must use synthetic data. Follow
`docs/images/README.md`.

## Documentation screenshot

The README screenshot is generated from the real current React build with a
synthetic API fixture. It must not be captured from a personal router. The
committed image should show no real public IP, hostname, device name, MAC
address, token, key, QR code, or local path.

## Dependency updates

Go modules, frontend packages, and GitHub Actions are monitored with Dependabot.
Dependency pull requests are reviewed and tested; they are not merged
unattended.

Do not add a dependency merely to avoid a small amount of maintainable standard
library code. Also do not reimplement security-sensitive primitives already
provided by a well-maintained library.

## Logging and diagnostics

Use stable event names and bounded metadata. Do not log:

- raw request bodies;
- full configuration objects;
- environment dumps;
- credentials or provider tokens;
- session IDs or CSRF tokens;
- WireGuard private material or QR payloads;
- generated secret-bearing service files;
- command lines containing secrets.

## Pull-request evidence

A change affecting routing, boot, security, persistence, privileges, recovery,
or performance should include:

- exact commands and results;
- successful and rejected-input tests;
- failure and rollback behavior;
- security impact;
- upgrade/compatibility impact;
- measured resource impact where relevant;
- redacted screenshots or generated-config excerpts when useful.

Draft pull requests and requests for help are welcome.

## Local safety

Never run integration tests against your active host firewall, routes, DNS,
Wi-Fi, or production WAN. Use a disposable VM, namespace, or explicitly
recognized CI container. Keep console access and a known-good router available
for every physical-network pilot.
>>>>>>> public/main
