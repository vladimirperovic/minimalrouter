# Minimal Router dashboard preview

One-page Apple × Swiss interface prototype based on the project's `DESIGN.md`,
`PROJECT.md`, `ARCHITECTURE.md`, and `SECURITY.md`.

## Local preview (development only)

```sh
pnpm install
pnpm dev
```

Open the URL printed by Vite (normally `http://localhost:5173`).

> The pre-built `web/dist/` is used for VM deployment. Rebuild only if
> the frontend source has changed.

## Production build

```sh
pnpm build
```

The appliance interface reads state from the versioned REST API. Controls
without a verified Alpine runtime adapter are disabled and labelled rather
than simulated. Node.js and pnpm are build-time dependencies only; the router
serves `dist/` directly from the Go process.
