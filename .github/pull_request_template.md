## What changed

Describe the user-visible or technical change and the problem it solves.

## Why it belongs in Minimal Router OS

Explain why this fits the project's intentionally narrow scope.

## Validation

List the exact checks performed.

```text
[ ] go test -race ./...
[ ] go vet ./...
[ ] pnpm --dir web lint
[ ] pnpm --dir web build
[ ] clean Alpine install / wizard smoke test, when applicable
```

## Security impact

Describe changes to listeners, privileges, secrets, authentication, firewall
rules, generated files, dependencies, update paths, or trust boundaries. Write
`None` only after considering each area.

## Failure and rollback behavior

Explain what happens when validation, generation, service reload, connectivity,
or confirmation fails.

## Screenshots

For dashboard changes, include screenshots from a current build with hostnames,
public IPs, MAC addresses, device names, tokens, keys, and private network
information removed.

## Documentation and compatibility

- [ ] Documentation is updated or the change does not require it.
- [ ] OpenAPI is updated or the API contract is unchanged.
- [ ] Compatibility or migration impact is documented.
- [ ] No credentials, runtime databases, backups, snapshots, or private network
      data are included.
- [ ] The change does not overstate production readiness.
