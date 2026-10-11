import type { RouterConfig } from "../api-types";
import type { Lease } from "./network";
import { ipNumber } from "./network";
import {
  insidePool,
  liveLeaseConflictMessage,
  reservationConflictMessage,
  suggestReservationAddress,
} from "../components/deviceReservation";

let pauses: Array<{ ip: string; until_unix: number }> = [];
export function demoNetworkRequest(
  url: URL,
  init: RequestInit,
  config: RouterConfig,
  leases: Lease[],
): Response | null {
  const path = url.pathname,
    method = init.method || "GET";
  const json = (value: unknown) =>
    new Response(JSON.stringify(value), {
      headers: { "Content-Type": "application/json" },
    });
  const now = new Date().toISOString();
  const lease_status = {
    available: true,
    collected_at: now,
    truncated: false,
    rejected_entries: 0,
  };
  if (path === "/api/v1/system/interfaces")
    return json({
      interfaces: [config.wan.interface, config.lan.interface].map((name) => ({
        name,
        up: true,
        carrier: true,
        carrier_known: true,
        physical: true,
      })),
    });
  if (path === "/api/v1/network/lan") {
    const capacity =
      ipNumber(config.dhcp.range_end) - ipNumber(config.dhcp.range_start) + 1;
    const observed = leases.filter((lease) =>
      insidePool(
        lease.ip_address,
        config.dhcp.range_start,
        config.dhcp.range_end,
      ),
    ).length;
    return json({
      revision: config.revision,
      generated_at: now,
      lan: config.lan,
      runtime_interface: config.lan.interface,
      dhcp_enabled: config.dhcp.enabled,
      lease_status,
      warnings: [],
      pool: {
        start: config.dhcp.range_start,
        end: config.dhcp.range_end,
        capacity,
        observed_leases: observed,
        free: capacity - observed,
        reservations: config.dhcp.static_leases?.length || 0,
      },
      dns: {
        enabled: true,
        encrypted_upstream_supported: false,
        client_resolver: config.lan.ip_address,
        upstreams: config.dhcp.dns_servers,
        local_records: config.dns?.records?.length || 0,
      },
    });
  }
  if (path === "/api/v1/network/devices")
    return json({
      revision: config.revision,
      updated_at: now,
      lease_status,
      leases,
      reservations: config.dhcp.static_leases,
      activity: {
        available: true,
        enabled: config.accounting?.enabled,
        devices: leases
          .slice(0, 3)
          .map((lease, i) => ({
            address: lease.ip_address,
            last_seen_epoch: Math.floor(Date.now() / 1000) - 42 - i * 130,
            month_bytes: 6900000000 - i * 10000000,
            new_this_month: false,
          })),
      },
    });
  if (path === "/api/v1/network/address-check") {
    const ip = url.searchParams.get("ip") || "",
      mac = url.searchParams.get("mac") || "",
      exclude = url.searchParams.get("exclude_reservation_id");
    const conflict =
      reservationConflictMessage(
        ip,
        mac,
        (config.dhcp.static_leases || []).filter((item) => item.id !== exclude),
      ) ||
      liveLeaseConflictMessage(ip, mac, leases) ||
      (insidePool(ip, config.dhcp.range_start, config.dhcp.range_end)
        ? "Address is in the DHCP pool"
        : "");
    return json({
      revision: config.revision,
      address: ip,
      state: conflict ? "conflict" : "no_known_conflict",
      conflicts: conflict ? [conflict] : [],
      lease_status,
      suggested_address: suggestReservationAddress(
        config.lan.ip_address,
        config.dhcp.range_start,
        [
          ...leases.map((item) => item.ip_address),
          ...(config.dhcp.static_leases || []).map((item) => item.ip_address),
        ],
        config.lan.cidr,
        config.dhcp.range_end,
      ),
      limitation: "Demonstration only. No real network was checked.",
    });
  }
  pauses = pauses.filter(
    (item) => !item.until_unix || item.until_unix > Date.now() / 1000,
  );
  if (path === "/api/v1/devices/pauses") return json({ pauses });
  if (
    (path === "/api/v1/devices/pause" || path === "/api/v1/devices/resume") &&
    method === "POST"
  ) {
    const { ip, seconds } = JSON.parse(String(init.body));
    pauses = pauses.filter((item) => item.ip !== ip);
    if (path.endsWith("/pause"))
      pauses.push({
        ip,
        until_unix: seconds ? Math.floor(Date.now() / 1000) + seconds : 0,
      });
    return json({ pauses });
  }
  if (path === "/api/v1/network/wol")
    return json({ success: true, demo: true });
  return null;
}
