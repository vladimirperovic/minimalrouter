# 0002 — Initial web frontend

- Status: Superseded by the React/Vite implementation on 2026-07-26
- Date: 2026-07-24
- Owners: project maintainers

## Context

The product allows Svelte or Vue. The management interface is small, local,
served as static assets, and must have low runtime overhead. A decision is
needed before scaffolding so the project does not maintain two UI stacks.

## Decision

Use Svelte with TypeScript and Vite for the version 1 web interface. Build a
static single-page application that communicates only with `/api/v1`.

Do not add server-side JavaScript rendering or a Node.js runtime to the
appliance. The Go management service serves the compiled assets.

## Consequences

Benefits:

- Small static deployment
- No JavaScript server process on the router
- Strong type checking
- Simple integration with generated OpenAPI client types

Costs:

- Contributors need the pinned Go toolchain and optionally the Vite build toolchain for rebuilding dist assets.
- The project must define navigation, error, and loading conventions early.
- The UI remains dependent on a JavaScript package supply chain during builds.

## Alternatives considered

### Vue

Vue is capable and mature, but maintaining an undecided or dual stack adds more
cost than value. It can be reconsidered only if the first vertical slice shows
a concrete blocker.

### Server-rendered templates

This would reduce frontend tooling but makes interactive configuration flows
and a clean API/UI separation less straightforward.

### SvelteKit server runtime

Rejected for the appliance because it would add a Node.js runtime without a
version 1 requirement for server-side rendering.

## Validation

The first vertical slice must:

- Produce static assets served by Go.
- Meet the selected browser support policy.
- Use generated API types.
- Enforce the required Content Security Policy without inline-script
  exceptions.
- Remain responsive within the normal memory target during traffic load.
