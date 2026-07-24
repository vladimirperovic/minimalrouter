# Architecture Decision Records

Architecture Decision Records (ADRs) capture decisions that are expensive,
security-sensitive, or likely to be questioned later.

## When an ADR is required

- A core dependency or framework is selected or replaced.
- A trust boundary or privileged operation changes.
- Persistence, backup, update, or rollback behavior changes.
- A supported platform or network protocol is added.
- A version 1 exclusion is reconsidered.
- The configuration transaction invariant changes.

## Status values

- Proposed
- Accepted
- Superseded
- Deprecated
- Rejected

## Naming

Use sequential names:

```text
0001-config-state-and-apply-pipeline.md
0002-update-rollback-strategy.md
```

Never reuse a number. A changed decision supersedes the previous ADR instead of
rewriting its history.

## Template

```markdown
# NNNN — Decision title

- Status: Proposed
- Date: YYYY-MM-DD
- Owners: project maintainers

## Context

What problem, constraints, and forces require a decision?

## Decision

What will the project do?

## Consequences

What becomes easier, harder, safer, or more expensive?

## Alternatives considered

Which realistic alternatives were rejected, and why?

## Validation

How will tests or a proof of concept confirm the decision?
```

## Current ADRs

- [0001 — Canonical configuration and apply pipeline](0001-config-state-and-apply-pipeline.md)
- [0002 — Initial web frontend](0002-initial-web-frontend.md)
