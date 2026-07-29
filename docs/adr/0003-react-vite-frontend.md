# 0003 — React/Vite static frontend

- Status: Accepted
- Date: 2026-07-26
- Owners: project maintainers
- Supersedes: ADR 0002

## Context

The implemented dashboard was already React-based, while ADR 0002 selected
Svelte. Keeping a framework adapter or server-rendering compatibility layer
increased build size and dependency surface without adding appliance
capability.

## Decision

Use React with TypeScript and Vite to compile a static single-page application
into `web/dist/`. `routerd` serves those files directly. Node.js, pnpm, Vite,
and ESLint are development/build dependencies only and are never installed as
router runtime services.

## Consequences

- The appliance retains one Go web/runtime process and no JavaScript server.
- Frontend lint, type checking, build, and dependency audit are release checks.
- UI code must not invent service health; unavailable backend capabilities are
  disabled and labelled.
- Inline scripts remain forbidden. Existing inline style attributes require
  `style-src 'unsafe-inline'` until styles are migrated to classes.

## Validation

`pnpm lint`, `pnpm build`, and `pnpm audit` must pass. The Alpine VM must serve
the built assets through HTTPS without a Node.js process or package installed.
