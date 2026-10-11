import type { RouterConfig } from "../../src/api-types";

export type TestLease = {
  hostname?: string;
  mac: string;
  ip_address: string;
  expires_at: number;
};
export function networkFixture(
  path: string,
  config: RouterConfig,
  leases: TestLease[] = [],
) {
  const status = {
    available: true,
    collected_at: new Date().toISOString(),
    truncated: false,
    rejected_entries: 0,
  };
  if (path === "/api/v1/network/devices")
    return {
      revision: config.revision,
      updated_at: new Date().toISOString(),
      leases,
      lease_status: status,
      reservations: config.dhcp.static_leases,
      activity: { available: false, enabled: false, devices: [] },
    };
  if (path === "/api/v1/network/lan")
    return {
      revision: config.revision,
      generated_at: new Date().toISOString(),
      runtime_interface: config.lan.interface,
      dhcp_enabled: config.dhcp.enabled,
      lease_status: status,
      pool: {
        start: config.dhcp.range_start,
        end: config.dhcp.range_end,
        capacity: 101,
        observed_leases: leases.length,
        free: 101 - leases.length,
        reservations: config.dhcp.static_leases?.length || 0,
      },
      dns: {
        enabled: true,
        encrypted_upstream_supported: false,
        client_resolver: config.lan.ip_address,
        upstreams: config.dhcp.dns_servers,
        local_records: config.dns?.records?.length || 0,
      },
      warnings: [],
    };
  if (path === "/api/v1/system/interfaces")
    return {
      interfaces: [
        { name: "eth0", up: true, carrier: true, carrier_known: true },
        { name: "eth1", up: true, carrier: true, carrier_known: true },
      ],
    };
  if (path === "/api/v1/network/address-check")
    return {
      revision: config.revision,
      state: "no_known_conflict",
      conflicts: [],
      lease_status: status,
      limitation: "No active scan was performed.",
    };
  return undefined;
}
