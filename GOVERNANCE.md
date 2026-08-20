# Project governance

Minimal Router OS is a **Beta (v0.1.5)** open-source project with a deliberately
small maintenance and security boundary. Governance is designed to keep project
decisions transparent while preserving the conservative review required for a
router and firewall appliance.

## Roles

### Maintainer

The maintainer is responsible for:

- defining project scope and release gates;
- reviewing and merging changes;
- protecting security-sensitive paths and credentials;
- coordinating vulnerability reports and disclosure;
- maintaining CI, packaging, documentation, and release metadata;
- deciding when a build is suitable for a named release or deployment class.

The current maintainer is listed in [MAINTAINERS.md](MAINTAINERS.md).

### Contributors

A contributor is anyone who improves code, tests, documentation, design,
accessibility, translations, hardware evidence, issue reports, or security
review. A merged pull request does not create an obligation to provide ongoing
support.

### Reviewers and future maintainers

Trusted contributors may be invited to review specific areas. Maintainer access
is granted gradually after sustained, high-quality participation and a clear
understanding of the project's security model. Access may be limited to a
subsystem before repository-wide authority is granted.

## Decision-making

Routine fixes and documentation changes are decided through issue and pull
request review. The maintainer makes the final merge decision after considering
technical evidence, project scope, security impact, maintenance cost, and
community feedback.

The following changes require an Architecture Decision Record before merge:

- trust-boundary or privilege changes;
- configuration-state or transaction-model changes;
- new WAN listeners or remote-administration paths;
- update, signing, recovery, or rollback architecture;
- persistent data-model changes with migration impact;
- a new general-purpose extension or package mechanism;
- a substantial expansion of supported network topology.

ADRs are stored under [`docs/adr/`](docs/adr/README.md). Discussion is welcome,
but consensus is not always possible. When a decision must be made, the
maintainer records the reasoning and decides.

## Security-sensitive changes

Changes to authentication, authorization, secret handling, firewall generation,
privileged execution, update trust, backup encryption, or release signing
require focused review. A contributor must describe:

- the trust boundary affected;
- accepted and rejected inputs;
- failure and rollback behavior;
- logging and redaction behavior;
- tests for both success and failure paths;
- any new dependency, listener, capability, file permission, or external service.

The maintainer may require independent review or additional evidence before
merging, even when CI passes.

## Releases

Only the maintainer may create an official Minimal Router OS release. A release
must follow [`docs/RELEASE_PROCESS.md`](docs/RELEASE_PROCESS.md), include known
limitations, and avoid claims that exceed the published validation evidence.

Public source availability does not imply production readiness. Supported use is
defined by each release's notes, not by the existence of a tag or downloadable
artifact.

## Scope and compatibility

During Beta, compatibility may still change between releases. The project favors
safe defaults, a small attack surface, and recoverable behavior over broad
feature parity. Useful features may be declined when they substantially increase
privilege, exposure, complexity, or long-term maintenance cost.

## Conflicts of interest

Maintainers and reviewers should disclose material conflicts of interest related
to vendors, paid work, competing products, or security research. A conflicted
reviewer should avoid being the sole approver for the affected decision.

## Conduct and enforcement

All project participation is governed by
[CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md). Security vulnerabilities must be
reported according to [SECURITY.md](SECURITY.md), not through a public conduct or
feature discussion.

## Governance changes

Governance changes are proposed through a pull request and should explain the
problem, the proposed authority or process change, and its security and community
impact. The maintainer approves changes to this document.
