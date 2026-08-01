# Storage pressure and bounded local state

Minimal Router OS treats local storage as a finite appliance resource. Routing and the already-active kernel/network state must continue even when the management plane is short on disk space.

## Pressure levels

The filesystem containing the canonical router data directory is classified as:

- **Normal**: below 80% used.
- **Warning**: 80% to below 90% used. Durable configuration changes remain available, while operators should free space.
- **Critical**: 90% used or higher. New management mutations that require durable state are rejected with HTTP 507 rather than reporting success that cannot be persisted.
- **Unknown**: storage telemetry is unavailable. Development/non-Linux builds do not invent a pressure state.

Critical pressure does not deliberately restart PPPoE, flush nftables, stop DHCP/DNS, or otherwise mutate the active forwarding plane.

## Bounded writers

The appliance keeps bounded local history:

- canonical configuration revisions: latest 100;
- configuration snapshots: latest 20;
- audit events: latest 5,000;
- gateway samples: at most 41,000 and seven days;
- gateway reconnect events: at most 2,048 and seven days;
- routerd/router-applyd service logs: 1 MiB per active log with four compressed rotations.

Gateway quality samples and reconnect-history writes are nonessential and are shed during critical pressure. Gateway probing and the live in-memory summary continue.

## SQLite maintenance

`routerd` reapplies the bounded retention rules and issues a passive WAL checkpoint at startup and every 15 minutes. The maintenance path does not run `VACUUM`, does not take the forwarding plane offline, and does not rewrite canonical configuration.

## Recovery behavior

Read-only status, diagnostic/preview operations and encrypted backup export remain available under pressure. Recovery/configuration operations that need durable writes remain blocked until space is freed; recovery must not claim success if its durable evidence cannot be recorded.
