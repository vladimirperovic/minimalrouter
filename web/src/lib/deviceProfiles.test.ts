import { describe, expect, it, vi } from "vitest";
import {
  createDefaultKidsGrid,
  createKidsProfile,
  describeSchedule,
  gridToDayWindows,
  slotsToWindows,
} from "./deviceProfiles";

describe("device profiles", () => {
  it("creates the default Kids profile with evening weekdays and a full weekend", () => {
    vi.stubGlobal("crypto", { randomUUID: () => "profile-id" });
    const profile = createKidsProfile({
      addresses: ["192.168.1.50"],
      services: ["youtube", "steam", "wiki"],
      dayWindows: gridToDayWindows(createDefaultKidsGrid()),
    });
    expect(profile).toMatchObject({
      id: "kids-profile-id",
      name: "Kids",
      ip_addresses: ["192.168.1.50"],
      services: ["youtube", "steam", "wiki"],
      schedule: {
        day_windows: {
          monday: [{ start: "19:00", end: "23:59" }],
          saturday: [{ start: "00:00", end: "23:59" }],
        },
      },
    });
    expect(describeSchedule(profile)).toBe("Mon–Fri 19:00–23:59; Sat–Sun all day");
    vi.unstubAllGlobals();
  });

  it("compresses selected hour cells into access windows", () => {
    const slots = Array(24).fill(false);
    slots[8] = true;
    slots[9] = true;
    slots[18] = true;
    expect(slotsToWindows(slots)).toEqual([
      { start: "08:00", end: "10:00" },
      { start: "18:00", end: "19:00" },
    ]);
  });

  it("rejects an empty device list", () => {
    expect(() => createKidsProfile({
      addresses: [], services: ["youtube"], dayWindows: gridToDayWindows(createDefaultKidsGrid()),
    })).toThrow(/static device IP address/);
  });
});
