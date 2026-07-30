## What changed

Describe the user-visible or technical change and the problem it solves.

## Why it belongs in Minimal Router OS

Explain why this fits the project's intentionally narrow scope. Link the issue or
ADR when applicable.

## Validation

List the exact checks performed, environment, and any limitations.

```text
[ ] go test -race ./...
[ ] go vet ./...
[ ] pnpm --dir web test
[ ] pnpm --dir web lint
[ ] pnpm --dir web build
[ ] clean Alpine install / wizard smoke test, when applicable
[ ] isolated hardware or VM test, when applicable
```

## Security and privacy impact

Describe changes to listeners, privileges, secrets, authentication, firewall
rules, generated files, dependencies, update paths, trust boundaries, telemetry,
external integrations, local data, logs, diagnostics, or backup contents. Write
`None` only after considering each area.

## Failure and rollback behavior

Explain what happens when validation, generation, service reload, connectivity,
confirmation, migration, or recovery fails.

## Screenshots and evidence

For dashboard or hardware changes, include evidence from a current build with
hostnames, public IPs, MAC addresses, device names, tokens, keys, profiles, QR
codes, and private network information removed. State whether values are
synthetic.

## Documentation and compatibility

- [ ] Documentation is updated or the change does not require it.
- [ ] OpenAPI is updated or the API contract is unchanged.
- [ ] Privacy documentation is updated or data handling is unchanged.
- [ ] Compatibility or migration impact is documented.
- [ ] An ADR is included or the change does not alter a major trust boundary,
      persistence model, update/recovery architecture, or supported topology.
- [ ] No credentials, runtime databases, backups, snapshots, packet captures, or
      private network data are included.
- [ ] The change does not overstate production readiness.
- [ ] I have the right to contribute this material under the project license.