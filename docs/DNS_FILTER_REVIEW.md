# DNS Filter review, 2026-10-08

## Implementation follow-up

The v0.2.0 candidate now addresses the confirmed editing, global-switch, response-type
and service-set replacement defects below. It adds opt-in maintained network
category blocking, subtree exceptions, domain checking, precise schedule editing,
Recovery improvements and common page spacing. See [the current implementation
and its explicit limits](DNS_FILTER.md). The findings below describe the original
audit, not the final candidate. Native Linux DNS/nft tests and release gates must
pass before release; source and mocked browser tests are not real-router proof.

The current implementation has a useful dnsmasq/nftables foundation, but its
small built-in blocking list and static-address schedules are not a complete
household protection product. For a household with frequently changing devices,
whole-network category controls should be the primary workflow. Device profiles
should remain optional. This is a review and proposal, not a claim that the
proposed controls have been implemented.

## Existing behavior

- `internal/services/adguard.go` supplies 17 ad/tracker domains when DNS Filter
  is enabled. There is no active automatic refresh of this blocking list.
- `internal/config/validation.go` rejects external blocklist URLs. The unused
  StevenBlack downloader must not be presented as an enabled subscription.
- `internal/services/device_profiles.go` generates IPv4 service destination
  sets and weekly rules for static client IPv4 addresses. The rules precede
  established-connection acceptance, which is the right ordering for schedules.
- Validation rejects malformed profiles, conflicting profile addresses,
  unsupported services and overlapping time windows. Privileged changes remain
  behind router-applyd, candidate validation and the existing apply transaction.
- DNS Activity's new maintained category lists produce observations and alerts;
  they do not replace the existing blocking policy.

## Confirmed defects and implementation risks

| Priority | Finding | Evidence and effect | Correction |
| --- | --- | --- | --- |
| P1 | A fresh revision can carry stale profiles | `DNSFilterPanel.persist` fetches current configuration, then replaces its profile array with one built from an older rendered snapshot. Browser reproduction: another session adds a profile at revision 43; saving a rename from revision 42 submits revision 43 with the new profile missing. The backend's revision check cannot detect that overwritten snapshot. | Apply a specific operation to fresh configuration, rejecting conflicts on the edited profile; or preserve the original edit revision and require reload on conflict. Never transplant a stale full array into a newer revision. |
| P1 | Firewall reload discards learned service destinations | `installAndActivate` invokes `runNftFile` on every apply. It atomically deletes/recreates the owned nftables table, while generated `svc_*` sets start empty. Existing client connections or client-cached destinations need not perform another DNS lookup. Their schedule match can disappear after an unrelated save. This path is established by code review; an installed-kernel reproduction is still required. | Preserve validated, bounded, unexpired entries for unchanged service sets across atomic replacement, or separate their lifecycle from rule replacement. Test active streams and client DNS caches through unrelated changes and rollback. Do not weaken the atomic/default-deny firewall boundary. |
| P2 | Renaming a profile can change its schedule | `gridFromProfile` discards minutes; `submitProfile` serializes the resulting hourly grid. Backend/API schedules accept minutes. Browser reproduction: changing only the name turns Monday 19:30–22:30 into 19:00–22:00. | Retain exact stored windows unless the schedule is edited; support precise times or require explicit conversion to hourly slots. |
| P2 | Editing a profile enables the global filter | Both add and edit call `persist(true, ...)`. Browser reproduction: rename a profile while filtering is disabled, and the submitted global switch becomes true. The generic change preview does not explain this specific change. | Preserve the global switch on edits; provide an explicit activation action for new policies. |
| P2 | Enabled profile is presented as active under a disabled global switch | The table renders `profile.enabled` as “Active” even when global filtering is off. A saved policy is not evidence of enforcement or the current schedule window. | Distinguish configured, globally disabled, currently allowed, currently blocked and unverified runtime state. |
| P2 | The ad list includes a broad platform API | `graph.facebook.com` appears among trackers. Meta's own SDK uses this host for Graph API calls, including ordinary platform functions. Blocking it can affect more than advertisements; this is a risk assessment, not evidence of a failure on an owner's device. | Review broad entries, retain source/rationale and add explicit blocking exceptions with a domain tester. |
| P2 | Blocked DNS response types are incomplete | Generated rules are `address=/domain/0.0.0.0`. Dnsmasq 2.86+ forwards record types that do not match the address literal unless a corresponding local-domain rule suppresses forwarding. This includes AAAA and other types. Existing WAN IPv6 deny rules limit IPv6 forwarding; this finding alone does not prove a current IPv6 Internet bypass. | Define and verify A, AAAA, HTTPS/SVCB, CNAME, local-record and subdomain semantics in a real resolver test before changing block responses. |

The first, third and fourth findings were reproduced together against the
production dashboard build with mocked API responses. No real router was
modified. The reproduction inspected the actual submitted JSON, not only the
screen text. Desktop and mobile layouts were also inspected.

Source references for protocol/platform behavior:
[dnsmasq manual](https://thekelleys.org.uk/dnsmasq/docs/dnsmasq-man.html) and
[Meta's official SDK](https://github.com/facebook/facebook-python-business-sdk/blob/main/facebook_business/session.py).

## Recommended page structure

1. **Network protection:** a compact, measured status area and independent
   category controls for malware/phishing/fraud, ads/trackers, adult content
   and gambling. Show the chosen action clearly: off, monitor or block, once
   those modes are actually supported. Existing choices must not change on
   upgrade without an explicit operator action.
2. **Lists and coverage:** provider, enabled categories, accepted domain count,
   last successful refresh, next refresh and failures. Separate the small
   firmware list from refreshed public catalogs and from recorded observations.
3. **Check a domain:** show which exact rule/list matches, whether a parent
   matches and which policy action applies. Include resolver health without
   presenting a lookup as proof that a person viewed content.
4. **Blocking exceptions:** exact-domain/subdomain scope, reason and optional
   expiry. Keep these separate from DNS Activity notification exceptions.
5. **Device schedules:** a collapsed optional area for households that want
   stable-address rules. Show DHCP reservation status and address changes, with
   readable schedule summaries and the router timezone/current clock. Preserve
   minute precision. Do not promise stable child identity from MAC/IP.
6. **Advanced DNS:** move upstream resolvers below protection controls or link
   to the existing LAN & DHCP editor. Avoid two equally prominent editors for
   the same setting. `dhcp.dns_enabled` is the unavailable upstream DoH option,
   not a local DNS on/off status.

On mobile, use profile cards with visible actions rather than requiring lateral
scrolling to discover edit/remove buttons. Keep the status and primary protection
switch above the fold, reduce repeated “Add profile” calls to action, and replace
mixed English/local-language schedule text with consistent localized labels.

## Performance and reliability priorities

- Share public catalog downloads and domain normalization with risk monitoring
  where possible, but keep observation and enforcement independently enabled.
  Runtime blocking requires a validated, bounded, strictly formatted input to
  the privileged helper; never hand it arbitrary URLs or dnsmasq configuration.
- Keep the previous working list on failed refresh. Cap download size, entries,
  disk use, concurrent refreshes and helper work. Reject empty or implausibly
  shrunken replacements before activation. Test low-storage behavior.
- Avoid loading millions of domains into a large Go map or generating unbounded
  repeated configuration directives. Benchmark candidate blocking formats on
  the appliance for memory, resolver startup, query latency and update pause.
  The risk monitor's on-disk index measurements do not establish resolver costs.
- Add explicit limits/telemetry for dynamic service sets and regression tests
  for destination expiry and table replacement. Preserve schedule enforcement
  through updates instead of relying on the next client query.
- Verify the router timezone, clock and daylight-saving transitions on the
  actual kernel/nftables version before claiming accurate local-time schedules.
- Add blocked-query counts only when DNS response evidence is collected. A list
  match or risk alert is not a verified blocked query.

## Recommended delivery order

First fix loss of concurrent changes, exact schedule preservation and unintended
global activation, then prove service-set continuity and resolver response
semantics in the network laboratory. Follow with whole-network category policy,
maintained blocking lists and exceptions, then redesign the page around those
working controls. The DNS Activity alert feature can remain useful independently.

This original proposal also identified future work: exact-only/expiring exceptions,
verified blocked-query counts, per-set occupancy telemetry and installed-device/DST
measurements. Those are not advertised as implemented controls.
