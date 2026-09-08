import { describe, expect, it } from "vitest";
import type { AccountingSnapshot, DeviceUsage } from "../api-types";
import { recentDeviceActivity } from "./deviceActivity";

const now = Date.parse("2026-10-01T00:02:00Z") / 1000;
const device = (address: string, seen?: number): DeviceUsage => ({ address, last_seen_epoch: seen, rx_bytes: 1, tx_bytes: 0, total_bytes: 1 });
const snapshot: AccountingSnapshot = {
  available: true, enabled: true, updated_at: new Date(now * 1000).toISOString(),
  months: [
    { month: "2026-10", total_bytes: 1, devices: [device("recent", now), device("stale", now - 601), device("missing"), device("future", now + 1), device("invalid", NaN)] },
    { month: "2026-09", total_bytes: 1, devices: [device("recent", now - 300), device("boundary", now - 600)] },
  ],
};
describe("recent routed activity", () => {
  it("keeps the newest sample per address across a month boundary, excluding stale, missing and future samples", () => {
    expect(recentDeviceActivity(snapshot, now).map(d => d.address)).toEqual(["recent", "boundary"]);
  });
  it("ages out devices without needing a fresh successful API response", () => {
    expect(recentDeviceActivity(snapshot, now + 602)).toEqual([]);
  });
  it("does not use retained history when measurement is disabled or unavailable", () => {
    expect(recentDeviceActivity({ ...snapshot, enabled: false }, now)).toEqual([]);
    expect(recentDeviceActivity({ ...snapshot, available: false }, now)).toEqual([]);
    expect(recentDeviceActivity(null, now)).toEqual([]);
  });
});
