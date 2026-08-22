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
