# Architecture decisions

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

Add each decision as a numbered `## NNNN — Title` section at the end of this
file. Never reuse a number. A changed decision supersedes the previous ADR instead of
rewriting its history.

## Template

```markdown
## NNNN — Decision title

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

- 0001 — Canonical configuration and apply pipeline
- 0002 — Initial web frontend
- 0003 — React/Vite static frontend
- 0004 — Local activity history

## 0001 — Canonical configuration and apply pipeline

- Status: Accepted
- Date: 2026-07-24
- Owners: project maintainers

### Context

The appliance coordinates several Linux components with different
configuration formats and reload behavior. Version 1 requires automatic
snapshots, validation, rollback, crash recovery, and a REST API. Treating
generated files as independent sources of truth would make cross-component
validation and atomic recovery unreliable.

### Decision

SQLite is the canonical configuration and state store. Service files are
deterministic derived artifacts.

All mutations use one serialized transaction pipeline:

`input -> validation -> model -> generation -> preflight -> snapshot -> apply -> verify -> commit or rollback`

`routerd` plans transactions without root privileges. A separate
`router-applyd` process performs only typed, allowlisted privileged operations.
Disruptive changes use commit-confirmed rollback.

### Consequences

Benefits:

- Transactional state and schema migrations
- One validation path for UI and API
- Deterministic snapshots and generated artifacts
- Clear crash-recovery markers
- Smaller privileged interface

Costs:

- Generators and adapters must be maintained for each Linux component.
- Operators cannot treat hand-edited service files as persistent.
- SQLite migration compatibility becomes a release responsibility.
- Cross-component apply cannot be truly atomic, so compensation and rollback
  must be tested aggressively.

### Alternatives considered

#### Single configuration file

Simple and inspectable, but less suitable for concurrent revisions,
idempotency, audit metadata, migrations, and snapshot bookkeeping.

#### Directly edit native service files

Rejected because native formats cannot provide one cross-component source of
truth or reliable transactional rollback.

#### Run the entire backend as root

Rejected for production because compromise of the web/API process would grant
unrestricted system control.

### Validation

Milestone 0 must demonstrate a WAN/LAN configuration that:

- Generates nftables, dnsmasq, and PPPoE artifacts deterministically.
- Passes real component preflight.
- Applies successfully from an empty state.
- Rolls back after injected failure at every stage.
- Recovers correctly after reboot at each durable transaction marker.

## 0002 — Initial web frontend

- Status: Superseded by the React/Vite implementation on 2026-07-26
- Date: 2026-07-24
- Owners: project maintainers

### Context

The product allows Svelte or Vue. The management interface is small, local,
served as static assets, and must have low runtime overhead. A decision is
needed before scaffolding so the project does not maintain two UI stacks.

### Decision

Use Svelte with TypeScript and Vite for the version 1 web interface. Build a
static single-page application that communicates only with `/api/v1`.

Do not add server-side JavaScript rendering or a Node.js runtime to the
appliance. The Go management service serves the compiled assets.

### Consequences

Benefits:

- Small static deployment
- No JavaScript server process on the router
- Strong type checking
- Simple integration with generated OpenAPI client types

Costs:

- Contributors need the pinned Go toolchain and optionally the Vite build toolchain for rebuilding dist assets.
- The project must define navigation, error, and loading conventions early.
- The UI remains dependent on a JavaScript package supply chain during builds.

### Alternatives considered

#### Vue

Vue is capable and mature, but maintaining an undecided or dual stack adds more
cost than value. It can be reconsidered only if the first vertical slice shows
a concrete blocker.

#### Server-rendered templates

This would reduce frontend tooling but makes interactive configuration flows
and a clean API/UI separation less straightforward.

#### SvelteKit server runtime

Rejected for the appliance because it would add a Node.js runtime without a
version 1 requirement for server-side rendering.

### Validation

The first vertical slice must:

- Produce static assets served by Go.
- Meet the selected browser support policy.
- Use generated API types.
- Enforce the required Content Security Policy without inline-script
  exceptions.
- Remain responsive within the normal memory target during traffic load.

## 0003 — React/Vite static frontend

- Status: Accepted
- Date: 2026-07-26
- Owners: project maintainers
- Supersedes: ADR 0002

### Context

The implemented dashboard was already React-based, while ADR 0002 selected
Svelte. Keeping a framework adapter or server-rendering compatibility layer
increased build size and dependency surface without adding appliance
capability.

### Decision

Use React with TypeScript and Vite to compile a static single-page application
into `web/dist/`. `routerd` serves those files directly. Node.js, pnpm, Vite,
and ESLint are development/build dependencies only and are never installed as
router runtime services.

### Consequences

- The appliance retains one Go web/runtime process and no JavaScript server.
- Frontend lint, type checking, build, and dependency audit are release checks.
- UI code must not invent service health; unavailable backend capabilities are
  disabled and labelled.
- Inline scripts remain forbidden. Existing inline style attributes require
  `style-src 'unsafe-inline'` until styles are migrated to classes.

### Validation

`pnpm lint`, `pnpm build`, and `pnpm audit` must pass. The Alpine VM must serve
the built assets through HTTPS without a Node.js process or package installed.

## 0004 — Local activity history and aggregate firewall counters

Status: Accepted

### Context

The dashboard needs measured traffic history and recent device activity without
cloud telemetry, packet inspection or a second privileged management path.

### Decision

Extend the existing local accounting SQLite database with 32 days of hourly byte
totals and daily per-address totals. The existing five-minute collector records
positive counter deltas and successful idle samples. The first sample and long
collection gaps establish a baseline rather than assigning unknown traffic to a
recent interval. Monthly totals keep their existing retention configuration.
Disabling accounting hides device history immediately and clears it durably on
the next collector round. Migrations only add tables and indexes; older readers
can ignore them when rolling back the application.

A fixed FIREWALL_COUNTERS Unix RPC extends the existing authenticated helper
allowlist. router-applyd runs a fixed nft command, retains only two aggregate
packet counters and a boot/table generation, and never returns raw rules or
addresses. Results are cached briefly. Observation chains run before and after
the original input/forward filter chains; they do not override default-deny
verdicts. routerd samples once a minute and retains at most 25 hours of aggregate
history, independently of optional per-device accounting. Resets, unavailable
reads and long gaps leave missing intervals. No new listener or privilege grant
is introduced.

The authenticated trusted-network API exposes bounded periods with no-store
responses. Charts separate missing samples from measured zero traffic. Overview
uses recent positive deltas, not DHCP expiry, as evidence of recent activity.
Activity is address-based, labels come from current configuration, and quiet or
local-only devices may be absent. It is not an online/offline guarantee.

### Consequences and validation

Storage and collection costs remain bounded; no browsing classification or
external telemetry is collected. Packet totals describe this router's input and
forward chains, not an application security verdict. Regression tests cover
counter projection, reset/gap handling, retention, disable behavior, authentication,
trusted-network access and preservation of default-deny policy. Signed update
and Golden-image requirements remain unchanged.
