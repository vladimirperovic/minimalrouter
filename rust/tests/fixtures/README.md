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

## Deliberate deviations from Go v0.3.3

The fixtures stay frozen. These behaviours differ from Go on purpose, and
`rust/tests/parity_services.rs` transforms the *expected Go value* for exactly
these (everything else is compared byte for byte):

- **WireGuard unsafe text.** Cases `default-582` (address is a single space)
  and `full-1110` (peer public key contains a tab) are rejected by both server
  renderers. The expectation adapter asserts those exact synthetic inputs and
  expects the specific rejection instead of the old generated text.
- **nftables SSH rules.** Go accepted `tcp dport 22` from the whole LAN and
  WireGuard interfaces. Rust accepts it only from the IPv4 members of
  `trusted_networks`, and writes a comment instead when there is no IPv4
  trusted network. Each of these is followed by a comment line and an explicit
  `iifname "<if>" tcp dport 22 drop`, so a custom firewall rule written later
  in the `input` chain cannot widen SSH (`with_ssh_deviation`).
- **Dynamic DNS forced update.** Go wrote `forced-update = 2592000` (30 days);
  Rust writes `604800` (7 days), because inadyn only compares the WAN address
  with its own cache and a lost or overwritten update otherwise stays in
  public DNS for weeks (`with_ddns_deviation`).
- **Firewall explanation, probe 3** (`192.168.1.50 -> 192.168.1.1 tcp 22`). Go's
  management `allow` becomes `deny` with reason "SSH is accepted only from
  trusted management networks." when the source is outside those networks
  (`with_explain_deviation`).
