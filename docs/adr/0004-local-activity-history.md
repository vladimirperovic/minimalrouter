# Local activity history and aggregate firewall counters

Status: Accepted

## Context

The dashboard needs measured traffic history and recent device activity without
cloud telemetry, packet inspection or a second privileged management path.

## Decision

Extend the existing local accounting SQLite database with 32 days of hourly byte
totals and daily per-address totals. The existing five-minute collector records
positive counter deltas and successful idle samples. The first sample and long
collection gaps establish a baseline rather than assigning unknown traffic to a
recent interval. Monthly totals keep their existing retention configuration.
Disabling accounting hides device history immediately and clears it durably on
the next collector round. Migrations only add tables and indexes; older readers
can ignore them when rolling back the application.

A fixed FIREWALL_COUNTERS Unix RPC extends the existing authenticated helper
allowlist. router-applyd runs a fixed nft command, retains only two aggregate
packet counters and a boot/table generation, and never returns raw rules or
addresses. Results are cached briefly. Observation chains run before and after
the original input/forward filter chains; they do not override default-deny
verdicts. routerd samples once a minute and retains at most 25 hours of aggregate
history, independently of optional per-device accounting. Resets, unavailable
reads and long gaps leave missing intervals. No new listener or privilege grant
is introduced.

The authenticated trusted-network API exposes bounded periods with no-store
responses. Charts separate missing samples from measured zero traffic. Overview
uses recent positive deltas, not DHCP expiry, as evidence of recent activity.
Activity is address-based, labels come from current configuration, and quiet or
local-only devices may be absent. It is not an online/offline guarantee.

## Consequences and validation

Storage and collection costs remain bounded; no browsing classification or
external telemetry is collected. Packet totals describe this router's input and
forward chains, not an application security verdict. Regression tests cover
counter projection, reset/gap handling, retention, disable behavior, authentication,
trusted-network access and preservation of default-deny policy. Signed update
and Golden-image requirements remain unchanged.
