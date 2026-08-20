# Contributing to Minimal Router OS

Thank you for considering a contribution.

Minimal Router OS is an early-stage, community-driven development project. It
is not maintained by a large networking company, and contributors do not need to
be expert kernel or firewall developers.

Beginners, homelab users, network administrators, security reviewers, designers,
technical writers, translators, testers, and experienced Go or React developers
are welcome.

Useful contributions include:

- clear bug reports with reproducible steps;
- testing on different NICs, CPUs, hypervisors, and Alpine installations;
- documentation, spelling, diagrams, and examples;
- accessibility and responsive-dashboard improvements;
- unit, integration, failure, and recovery tests;
- narrowly scoped backend or frontend fixes;
- security review and threat-model feedback;
- translations and onboarding improvements.

No contribution is too small when it makes the project safer or easier to use.

## Project status and governance

The project is currently **Beta (v0.1.5)**. Compatibility may still change,
some features are intentionally unavailable, and no release should be treated as
an unattended production firewall unless its release notes explicitly say so.

Please do not present Minimal Router OS as a complete pfSense or OpenWrt
replacement. The current goal is a focused, understandable home-router appliance
with safe defaults, recoverable changes, and a small resource footprint.

Project decisions and maintainer authority are documented in
[GOVERNANCE.md](GOVERNANCE.md). Active ownership is listed in
[MAINTAINERS.md](MAINTAINERS.md).

## Before changing code

1. Read `README.md`, `ARCHITECTURE.md`, `SECURITY.md`, and the relevant ADRs.
2. Search existing issues and pull requests.
3. For a larger feature, open an issue before investing substantial time.
4. For a change to a trust boundary, persistence model, update strategy,
   privilege model, configuration lifecycle, or supported topology, propose an
   ADR first.
5. Use an isolated test environment for networking, packaging, and first-run
   changes. Keep console access and a known-good router available.

A maintainer may suggest a smaller first contribution. This is intended to make
review and validation easier, not to discourage participation.

## Development setup

See [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md) for the complete local workflow.
The standard checks are:

```sh
go test -race ./...
go vet ./...
pnpm --dir web install --frozen-lockfile
pnpm --dir web lint
pnpm --dir web build
```

Changes to packaging or first-run behavior should also exercise the clean Alpine
installation smoke test when the required container privileges are available:

```sh
make dist-amd64
sh scripts/ci-fresh-install-smoke.sh
```

Never run integration tests against an active host firewall, production WAN, or
normal household/office LAN. Use a disposable VM, network namespace, or
explicitly isolated CI environment.

## Development and security rules

- Never commit real credentials, private keys, tokens, backups, runtime state,
  databases, snapshots, packet captures, public IP addresses, device names, or
  private network inventories.
- Never execute user-controlled text through a shell.
- API handlers must not edit Linux service files directly.
- All configuration follows the typed validation and transaction pipeline.
- Keep generated files deterministic and test both accepted and rejected input.
- Keep packet forwarding in the Linux data plane.
- Do not weaken a test merely to make CI green; fix the behavior or explain why
  the test expectation was incorrect.
- New dependencies require a clear maintenance and security justification.
- Unsupported functionality should fail closed and be shown honestly in the UI.
- Privacy-relevant changes must update [PRIVACY.md](PRIVACY.md).
- API behavior changes must update `api/openapi.yaml` or explain why the contract
  is unchanged.

## Issues and hardware evidence

Use the provided issue templates:

- bug reports for reproducible software defects;
- feature requests for focused scope proposals;
- hardware validation reports for privacy-safe VM or dedicated-device evidence.

Hardware reports must state the exact commit, environment, test method, units,
duration, and limitations. A successful result on one device does not establish
support for another device or production readiness.

Questions that include a security vulnerability must not be opened publicly.
Follow [SECURITY.md](SECURITY.md).

## Pull requests

Keep pull requests focused. Include:

- what changed and why;
- why the change belongs in the project's intentionally narrow scope;
- exact validation performed;
- security and privacy impact;
- rollback or failure behavior;
- user-visible changes;
- screenshots for dashboard changes, with private information removed;
- documentation, OpenAPI, compatibility, and migration changes when applicable.

Draft pull requests are welcome. It is acceptable to ask for help or state that a
change is incomplete.

## Commit style

Use concise imperative subjects, for example:

```text
config: reject overlapping LAN networks
apply: roll back failed dnsmasq reload
ui: improve first-run interface selection
docs: clarify isolated installation workflow
```

Keep unrelated refactors separate from behavior changes where practical.

## Licensing of contributions

By submitting a contribution, you confirm that you have the right to provide it
and agree that it may be distributed under the project's
[MIT License](LICENSE). Do not copy code, documentation, images, icons, or other
material unless its license is compatible and attribution requirements are
satisfied.

## Good first issues

Issues labeled `good first issue`, `documentation`, `testing`, or `help wanted`
are intended as approachable entry points. A contributor may ask for additional
context before starting.

## Definition of done

A change is ready when the relevant items are true:

- behavior and limitations are documented;
- success and failure paths are tested;
- authorization, privilege, privacy, and secret handling are reviewed;
- disruptive network changes preserve rollback behavior;
- logs and errors contain no secrets;
- OpenAPI, compatibility, and migration impact are addressed;
- CI passes;
- the pull request does not overstate production readiness.

## Community behavior

Be patient, specific, and respectful. Review the
[Code of Conduct](CODE_OF_CONDUCT.md). Technical disagreement is welcome;
personal attacks, gatekeeping, or ridicule of less-experienced contributors are
not.

## Security reports

Do not disclose vulnerabilities in a public issue or pull request. Follow
[SECURITY.md](SECURITY.md) for private reporting instructions.
