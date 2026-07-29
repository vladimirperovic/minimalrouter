# Minimal Router OS dashboard

The dashboard is a React + TypeScript single-page application built with Vite.
It is a client of the versioned `/api/v1` Go API and contains no privileged
networking logic.

Node.js and pnpm are development/build dependencies only. The router serves the
compiled static files from `web/dist/`; no Node.js runtime is installed on the
appliance.

## Requirements

- Node.js 22
- pnpm version used by the repository CI

From this directory:

```sh
pnpm install --frozen-lockfile
pnpm lint
pnpm build
```

From the repository root, use the equivalent commands:

```sh
pnpm --dir web install --frozen-lockfile
pnpm --dir web lint
pnpm --dir web build
```

## Development server

```sh
pnpm dev
```

Vite normally opens the dashboard at `http://localhost:5173`. Requests under
`/api/v1` are proxied to `http://127.0.0.1:8080`, which is reserved for the
explicit macOS control-plane preview described in `../docs/DEVELOPMENT.md`.

The preview simulates API, authentication, SQLite, and transaction behavior. It
does **not** apply Linux firewall, routing, PPPoE, DHCP, DNS, or WireGuard state
and must never be presented as a router integration test.

## Design and behavior rules

- Use the REST API rather than duplicating backend validation in the UI.
- Never render or log plaintext credentials, tokens, private keys, session IDs,
  CSRF tokens, or unredacted backup data.
- Keep unsupported controls disabled and clearly labelled instead of simulating
  success.
- Preserve keyboard navigation, visible focus, readable contrast, and responsive
  behavior.
- Add or update backend/API tests for security-sensitive behavior; UI-only tests
  are not a substitute for server-side authorization and validation.
- Use synthetic documentation data in screenshots and examples.

See `../DESIGN.md`, `../ARCHITECTURE.md`, `../SECURITY.md`, and
`../CONTRIBUTING.md` before making substantial changes.
