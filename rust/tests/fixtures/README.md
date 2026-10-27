# Parity fixtures

Frozen outputs of the last Go implementation (tag `v0.3.3`, commit `309965e`),
used by the `parity_*` tests so the Rust code keeps matching the behaviour the
Go release validated, without needing Go. Each file is gzip-compressed JSON
lines.

| File | Cases | What it holds |
|---|---:|---|
| `config_cases.jsonl.gz` | 502 | mutated configurations and the validation/normalisation result |
| `services_cases.jsonl.gz` | 1,401 | generated nftables, dnsmasq, WireGuard and other service files |
| `firmware_cases.jsonl.gz` | 500 | manifests signed by Go (public key and signature only; no signing key) |
| `version_cases.jsonl.gz` | 2,000 | release-version comparisons |

They contain only synthetic data. To regenerate them, check out the commit
before Go was removed (`git log --diff-filter=D -- go.mod`), then run
`go run ./rust/_goparity -out DIR -n 500` (the harness lives in
history under `_goparity/`) and gzip each file.

The tests fail, not skip, when a fixture is missing.
