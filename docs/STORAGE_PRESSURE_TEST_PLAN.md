# Storage pressure validation plan

Automated and appliance validation for bounded storage must cover these boundaries:

1. Unit-test 80% warning and 90% critical classification.
2. Verify read-only requests are not blocked by the storage guard.
3. Verify configuration, snapshot, transaction-confirm, gateway-settings and recovery mutations are classified as durable writes.
4. Verify backup export and preview/verification operations remain available.
5. Fill an Alpine test filesystem past 90%, confirm mutations return HTTP 507, and confirm existing nftables/PPPoE/DHCP/DNS state remains active.
6. Confirm gateway live probing continues while history row growth stops.
7. Confirm logrotate keeps the four router service logs within the configured bounded rotation policy.
8. Confirm retention remains 100 config revisions, 20 snapshots, 5,000 audit events, 41,000 gateway samples, and 2,048 gateway reconnect events.
9. Confirm passive WAL checkpoint completes without taking routerd offline.
10. Free storage below 90% and verify durable mutations become available without reboot.

Real full-disk/inode exhaustion and read-only-filesystem tests remain target-appliance gates before production-readiness claims.
