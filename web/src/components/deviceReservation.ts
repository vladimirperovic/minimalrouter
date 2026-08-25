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
