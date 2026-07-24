# Contributing

Minimal Router OS is in its foundation phase. Prefer small, reviewable changes
that preserve the product's narrow scope.

## Before changing code

1. Read `PROJECT.md`, `ARCHITECTURE.md`, and `SECURITY.md`.
2. Search existing issues and ADRs.
3. For a new feature, explain why it belongs in version 1.
4. For a change to a trust boundary, persistence model, update strategy, or
   configuration lifecycle, add or update an ADR first.

## Development rules

- Never edit Linux service configuration directly from an API handler.
- All configuration follows the validated transaction pipeline.
- Never execute user-controlled text through a shell.
- Keep generated files deterministic and test them as golden outputs.
- Treat credentials, private keys, session data, and backups as secrets.
- Keep packet forwarding in the Linux data plane.
- Add only dependencies that clearly reduce implementation or security risk.
- Do not add excluded version 1 features without a product decision.

## Change workflow

1. Create a focused branch.
2. Add tests before or with the implementation.
3. Run the local checks described in `docs/DEVELOPMENT.md`.
4. Update affected documentation and OpenAPI definitions.
5. Describe security impact, rollback behavior, and resource impact in the pull
   request.
6. Obtain review before merging security-sensitive changes.

## Commit style

Use short imperative subjects, for example:

```text
config: validate overlapping LAN networks
apply: roll back failed dnsmasq reload
api: require revision on firewall updates
```

Keep refactors separate from behavior changes where practical.

## Definition of done

A change is complete when:

- Acceptance behavior is documented.
- Unit and integration tests cover success and failure.
- Security and authorization behavior is tested.
- Rollback is tested for configuration mutations.
- Documentation and API contracts are current.
- Logs and errors contain no secrets.
- CI passes.
- Measured boot, memory, or throughput impact is recorded when relevant.

## Reporting security issues

Follow `SECURITY.md`. Do not disclose vulnerability details in a public issue.
