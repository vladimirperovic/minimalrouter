# v0.4.1 review of the supplied Rust findings

Reviewed against public-core commit `3aae5c4fc3437d6c857dc4139c772392b0877609`.
This records source verification and regression coverage, not a penetration test
or physical-router validation. A static finding's existence does not by itself
establish its proposed severity or exploitability. The earlier reviewer's
claimed command history cannot be independently proven from the pasted text;
the validation record reports commands rerun here. A green Clippy run is not a
security audit or proof that Linux-only paths executed.

## Confirmed behavior and changes

| Supplied finding | Verification and disposition |
| --- | --- |
| WG client confirmation accepts old handshakes without keepalive | Confirmed: endpoint/peer edits use `wg syncconf`, which can retain the handshake. Changed clients now retire the previous peer before `syncconf`, so its authenticated state cannot survive. Both gates use one verifier; a changed client requires a handshake from activation start or later, including an immediate handshake within the same second. The helper persists its own boundary and rejects legacy pending state without one. Future timestamps beyond 30 seconds and stale changed-client handshakes are refused. Endpoint/AllowedIPs regression tests use keepalive zero. |
| WG engine accepts future timestamps | Confirmed; the shared verifier applies the same future-time rule to both gates. |
| WG unchanged-client engine check is skipped | Intentional scoping; the helper still checks an enabled client. Unchanged, idle, keepalive-zero peers do not require artificial fresh traffic. A handshake is evidence of peer authentication, not proof that every AllowedIPs route or remote application works. The report overstates the direct `wireguard_only` impact: that setting uses the separate server tunnel, not `wg_client`. |
| Trusted-network /1 and masked-address validation | Confirmed acceptance, but not an authentication bypass. Reject IPv4 broader than /8, public IPv6 broader than /16, and masked unspecified networks. Existing ULA subnets within fc00::/7 remain supported. Host bits retain standard CIDR semantics; effective duplicate networks are rejected. Tests cover /1, host bits, mapped IPv4 and ULA. Public management networks remain supported; private-only admission was not imposed. |
| applyd handler panic leaks capacity | Confirmed. RAII returns the slot on unwind and failed worker creation. Repeated panic regressions exceed the eight-slot production limit. Process-abort/OOM behavior remains the supervisor's responsibility. |
| HTTP responses have no write timeout | Confirmed. Default write timeout is now 30 seconds, including TLS sockets. A TCP peer that requests a large response and never reads must release its worker. This is a socket inactivity timeout, not a global bandwidth or response-duration quota. |
| DNS risk holds feed publication lock during SQL | Confirmed. Clone a small snapshot of Arc-owned connections under the read lock, release it before matching, and retain the old connections until that batch finishes. The regression proves publication can acquire the write lock while a snapshot remains usable. Sustained writer starvation was a possibility, not a measured result in the original report. |
| DNS filter repeatedly opens SQLite catalogs | Confirmed. Cache read-only connections for the fixed two slots per source; select metadata and query through the same connection. Check file metadata on lookup and invalidate before rebuilding a slot. Tests cover reuse and changed-hash reload. No unbounded in-memory domain sets or new rate-limit policy were introduced. |
| Session IDs persist as bearer credentials | Confirmed for raw database copies. Store domain-separated SHA-256 session identifiers and use hashes for lookup/touch/delete. Existing plaintext sessions are revoked during migration; new sessions survive restart. Stored digests cannot be replayed as cookies. CSRF values remain persisted to preserve restart behavior, but cannot authenticate without the original random bearer token. The normal encrypted portable backup contains configuration/DNS policy, not the sessions table. |
| Recovery backup uses ordinary fill(0) | Confirmed. RAII buffers use the existing volatile zeroization primitive on success, error and unwind. Cryptographic library internals and deserialized configuration allocations are outside this limited erasure claim. |
| IPC reader silently truncates oversized requests | Confirmed. Require EOF within the limit and reject an extra byte with InvalidData, including a valid JSON prefix followed by extra data. Tests cover exactly-limit and limit+1. |
| TLS temporary file permissions set after create | Confirmed, but protected by the private parent directory. Unix creation now requests mode 0600 before data is written; durable rename behavior is retained. |
| Update channel preference is not synced | Confirmed durability issue; the private atomic write now syncs before publishing the in-memory preference. |
| Archive root may be a regular file; archive modes applied | Confirmed intermediate behavior; final root verification already rejected a regular root. Reject it before writing and clamp extracted directories/files to 0755 and executable 0755/non-executable 0644. Signature/hash/architecture/rollback checks remain authoritative. |
| DNS worker panic leaves busy set; handles accumulate | Confirmed. Catch operation panics and record an unconfirmed, review-required outcome; clear busy and reap finished handles on subsequent starts. A regression intentionally panics, then runs further operations. |
| Content sniffing accepts vertical tab | Confirmed. Explicitly reject VT while preserving TAB/LF/FF/CR/ESC semantics. Parentheses alone would not fix the missing VT check. |
| Response header names are unchecked | Confirmed as missing hardening, not a demonstrated injection path with current constant names. Header construction refuses non-token names; the parser returns 400 for malformed names. |
| Revision arithmetic/casts can wrap | Confirmed theoretical boundary. Use checked increment, cap engine revisions at SQLite's signed range and use a checked snapshot conversion. It is not a realistic remote exhaustion attack. |
| MCP secret copies and redirects | Owned password/TOTP/login buffers and client cookies/CSRF now have erasure guards. Login serialization borrows secrets instead of cloning into JSON Values. Redirects are disabled for the configured API origin. This does not claim to erase the inherited environment or third-party HTTP/TLS allocations. Cookie/Authorization stripping in the installed ureq 2.12.1 means the supplied report did not demonstrate their cross-origin leakage. |
| Transfer-Encoding plus Content-Length | Confirmed acceptance; this origin-server behavior alone did not demonstrate proxy request smuggling. Return 400 for the combination and repeated Transfer-Encoding, with HTTP regression coverage. |

## Findings that do not justify the proposed behavior change

| Supplied finding | Assessment |
| --- | --- |
| One Argon2 slot / 15-second queue | Intentional memory admission control: each derivation uses 64 MiB under a 128 MiB process budget. Rate limits and retryable 503 responses remain. An unauthenticated last-admin-IP priority scheme is not a reliable identity boundary. Keep the bound and document contention; no security bypass was shown. |
| Root/routerd peer UID allowlist | The intended local privilege boundary. Root is already trusted and any process with the routerd identity has its authority. A /proc executable-name check is race-prone and does not establish code integrity; no path-based pseudo-authentication added. |
| Extra unmanifested firmware files | Source verification checks manifested files and slot staging copies only those files, then verifies the copy again. Extra files are not activated. A whole-tree inventory is optional hardening, not a bypass; signed manifest/trust-anchor packaging also needs explicit exceptions. No change to the activation allowlist. |
| Sleep thread per confirmation timer | Cancellation leaves a sleeping thread until its bounded timeout. Current single-pending-transaction design makes a scheduler replacement disproportionate to the demonstrated issue. No timer scalability claim made. |
| routerd wildcard bind | Required for changing LAN addresses; nftables and destination-address admission independently constrain exposure. Preserve both controls rather than bind to one stale address. |
| Accounting retention cap | Deliberate global cap of MAX_TRACKED_DEVICES times retention_months, ordered by newest month then traffic. This may drop quieter/older rows; it is not a promised per-month quota. |
| Read-only SQLite transaction in firewall activity | DEFERRED, not a write lock. It keeps the clock and minute rows in one consistent read snapshot. Removing it is not needed for correctness and can weaken consistency. |
| Clippy style clean-up / blanket -D warnings | Style and large-error/type suggestions are not security findings. Avoid an unrelated workspace-wide rewrite or a new warning gate until the existing lint baseline is resolved deliberately. Local lint results are recorded separately. |

## Operational effects

An edited WireGuard client with keepalive disabled needs actual tunnel traffic
and a new handshake before the 90-second confirmation window expires; otherwise
rollback remains the safe outcome. Handshake time is wall-clock based, so a clock
step can conservatively refuse confirmation. No active ping target is invented.

Narrow any formerly accepted broad trusted networks before installation. Existing
valid host-bit CIDRs and ULA scopes remain valid. Expect to log in again after the
session migration. The plaintext CSRF value alone is not a session credential.

Shared validation/storage also compile into installed recovery/setup tools.
The v0.4.1 bootstrap change is acknowledged and appliances at or below v0.4.0
need the full signed installer once; runtime byte identity is never bypassed.
See [WEB-UPDATE.md](WEB-UPDATE.md) and [CURRENT_VALIDATION.md](CURRENT_VALIDATION.md).

All examples and tests use synthetic addresses/credentials. No private/Home
inventory, live credentials, generated images or binaries belong in this diff.

WireGuard behavior was cross-checked against the upstream
[`syncconf` implementation](https://git.zx2c4.com/wireguard-tools/tree/src/setconf.c):
it preserves matching peers. Explicitly removing the old outbound peer avoids
relying on timestamp precision to distinguish two configurations.
