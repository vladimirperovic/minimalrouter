import type { AccountingSnapshot, DeviceUsage } from "../api-types";

export const DEVICE_ACTIVITY_WINDOW_SECONDS = 10 * 60;

// Accounting timestamps mark a collection with a positive byte delta, not a
// DHCP expiry or a live connectivity probe. Include the preceding month so a
// device does not disappear at midnight on the first day of a month.
export function recentDeviceActivity(snapshot: AccountingSnapshot | null, now: number): DeviceUsage[] {
  if (!snapshot?.available || !snapshot.enabled || !Number.isFinite(now)) return [];
  const devices = new Map<string, DeviceUsage>();
  for (const month of snapshot.months || []) {
    for (const device of month.devices || []) {
      const seen = device.last_seen_epoch;
      if (!seen || !Number.isFinite(seen) || seen > now || now - seen > DEVICE_ACTIVITY_WINDOW_SECONDS) continue;
      const previous = devices.get(device.address);
      if (!previous || seen > previous.last_seen_epoch!) devices.set(device.address, device);
    }
  }
  return [...devices.values()].sort((a, b) => b.last_seen_epoch! - a.last_seen_epoch!);
}
