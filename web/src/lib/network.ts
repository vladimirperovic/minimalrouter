import type { RouterConfig, StaticLease } from "../api-types";
import { isValidIPv4 } from "../components/deviceReservation";

export type Lease = {
  expires_at: number;
  mac: string;
  ip_address: string;
  hostname?: string;
};
export type LeaseStatus = {
  available: boolean;
  collected_at: string;
  truncated: boolean;
  rejected_entries: number;
  error?: string;
};
export type DeviceInventory = {
  revision: number;
  updated_at: string;
  leases: Lease[];
  lease_status: LeaseStatus;
  reservations: StaticLease[];
  activity: {
    available: boolean;
    enabled: boolean;
    devices: Array<{
      address: string;
      last_seen_epoch: number;
      month_bytes: number;
      new_this_month: boolean;
    }>;
  };
};
export type LANStatus = {
  revision: number;
  generated_at: string;
  runtime_interface: string;
  dhcp_enabled: boolean;
  lease_status: LeaseStatus;
  pool: {
    start: string;
    end: string;
    capacity: number;
    observed_leases: number;
    free: number | null;
    reservations: number;
  };
  dns: {
    enabled: boolean;
    encrypted_upstream_supported: boolean;
    client_resolver: string;
    upstreams: string[];
    local_records: number;
  };
  warnings: string[];
};
export type NetworkInterface = {
  name: string;
  mac_address?: string;
  up: boolean;
  carrier: boolean;
  carrier_known?: boolean;
  physical: boolean;
};
export type AddressAssessment = {
  revision: number;
  address: string;
  state: "conflict" | "unknown" | "no_known_conflict";
  conflicts: string[];
  suggested_address?: string;
  lease_status: LeaseStatus;
  limitation: string;
};

export const sameValue = (a: unknown, b: unknown) =>
  JSON.stringify(a) === JSON.stringify(b);
export function assertUnchanged(base: unknown, latest: unknown) {
  if (!sameValue(base, latest))
    throw new Error(
      "These settings changed in another session. Your draft is kept. Load the current settings and review them before saving.",
    );
}
export function ipNumber(value: string): number {
  return value.split(".").reduce((n, part) => n * 256 + Number(part), 0);
}
export function subnetBounds(
  cidr: string,
): { first: number; last: number } | null {
  const [ip, prefix] = cidr.split("/");
  if (
    !isValidIPv4(ip) ||
    !/^\d+$/.test(prefix) ||
    Number(prefix) < 1 ||
    Number(prefix) > 30
  )
    return null;
  const mask = (0xffffffff << (32 - Number(prefix))) >>> 0;
  const first = (ipNumber(ip) & mask) >>> 0;
  return { first, last: first + 2 ** (32 - Number(prefix)) - 1 };
}
export function addressInLAN(
  value: string,
  config: RouterConfig,
  gatewayAllowed = false,
): string {
  const bounds = subnetBounds(config.lan.cidr || "");
  if (
    !isValidIPv4(value) ||
    !bounds ||
    ipNumber(value) <= bounds.first ||
    ipNumber(value) >= bounds.last
  )
    return "Choose a usable IPv4 host address in the current LAN subnet.";
  if (!gatewayAllowed && value === config.lan.ip_address)
    return "This address belongs to the router gateway.";
  return "";
}
export function managementURL(
  config: Pick<RouterConfig, "lan" | "system">,
): string {
  const protocol = config.system.https_enabled ? "https:" : "http:";
  const port = config.system.https_enabled
    ? config.system.https_port || 8443
    : 8080;
  return `${protocol}//${config.lan.ip_address}:${port}/#network`;
}
