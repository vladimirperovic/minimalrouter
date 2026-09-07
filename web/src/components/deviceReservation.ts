import type { StaticLease } from "../api-types";

export const MAC_PATTERN = /^([0-9a-f]{2}:){5}[0-9a-f]{2}$/i;

export function isValidIPv4(value: string): boolean {
  const parts = value.trim().split(".");
  if (parts.length !== 4) return false;
  return parts.every((part) => /^\d{1,3}$/.test(part) && Number(part) >= 0 && Number(part) <= 255);
}

function ipv4ToNumber(value: string): number {
  return value.split(".").reduce((total, octet) => total * 256 + Number(octet), 0);
}

export function insidePool(ip: string, start: string, end: string): boolean {
  if (!isValidIPv4(ip) || !isValidIPv4(start) || !isValidIPv4(end)) return false;
  const target = ipv4ToNumber(ip.trim());
  return target >= ipv4ToNumber(start.trim()) && target <= ipv4ToNumber(end.trim());
}

function numberToIPv4(value: number): string {
  return [24, 16, 8, 0].map((shift) => (value >>> shift) & 255).join(".");
}

// suggestReservationAddress finds the lowest address below the dynamic pool that
// no reservation and no live lease is using. A device's current address always
// comes from the pool, and the appliance refuses a reservation that overlaps it,
// so offering that address back was offering a value that could never be saved.
export function suggestReservationAddress(
  lanIP: string,
  poolStart: string,
  takenIPs: readonly string[],
): string {
  if (!isValidIPv4(lanIP) || !isValidIPv4(poolStart)) return "";
  const gateway = ipv4ToNumber(lanIP.trim());
  const firstDynamic = ipv4ToNumber(poolStart.trim());
  const taken = new Set(takenIPs.map((item) => item.trim()).filter(Boolean));
  for (let candidate = gateway + 1; candidate < firstDynamic; candidate += 1) {
    const address = numberToIPv4(candidate);
    if (!taken.has(address)) return address;
  }
  return "";
}

export function reservationConflictMessage(ip: string, mac: string, leases: StaticLease[]): string {
  const normalisedIP = ip.trim();
  const normalisedMac = mac.trim().toLowerCase();

  if (!isValidIPv4(normalisedIP)) {
    return "Reserved address must be a valid IPv4 address.";
  }
  if (!MAC_PATTERN.test(normalisedMac)) {
    return "MAC address must look like aa:bb:cc:dd:ee:ff.";
  }

  const duplicateIP = leases.find((lease) => lease.ip_address.trim() === normalisedIP);
  if (duplicateIP) {
    const owner = duplicateIP.hostname?.trim() || duplicateIP.mac;
    return `${normalisedIP} is already reserved for ${owner}. Choose another address.`;
  }

  if (leases.some((lease) => lease.mac.trim().toLowerCase() === normalisedMac)) {
    return "That MAC address already has a reservation.";
  }

  return "";
}

export type LiveLease = {
  ip_address: string;
  mac: string;
  hostname?: string;
  /** Lease expiry as unix epoch seconds; missing or zero means unknown. */
  expires_at?: number;
};

// liveLeaseConflictMessage blocks a reservation only while another device
// actively holds the address. A stale (expired) lease entry stays on file
// in dnsmasq after a device is forgotten, but the address is effectively
// free, so re-reserving it must not wait for housekeeping. Unknown expiry
// stays blocked: failing closed beats guessing.
export function liveLeaseConflictMessage(
  ip: string,
  targetMac: string,
  leases: readonly LiveLease[],
  now: number = Date.now(),
): string {
  const normalisedIP = ip.trim();
  const normalisedMac = targetMac.trim().toLowerCase();
  const collision = leases.find(
    (lease) => lease.ip_address.trim() === normalisedIP && lease.mac.trim().toLowerCase() !== normalisedMac,
  );
  if (!collision) return "";
  if (typeof collision.expires_at === "number" && collision.expires_at > 0 && collision.expires_at * 1000 <= now) {
    return "";
  }
  const holder = collision.hostname?.trim() || collision.mac;
  const expiry =
    typeof collision.expires_at === "number" && collision.expires_at > 0
      ? `, ${formatLeaseCountdown(collision.expires_at, now)}`
      : "";
  return `${normalisedIP} is currently leased to ${holder} (${collision.mac}${expiry}). Choose another address.`;
}

function formatLeaseCountdown(expiresAt: number, now: number): string {
  const diff = expiresAt * 1000 - now;
  if (diff <= 0) return "expired";
  const minutes = Math.floor(diff / 60000);
  if (minutes < 60) return `in ${Math.max(1, minutes)} min`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `in ${hours} h`;
  return `in ${Math.floor(hours / 24)} d`;
}
