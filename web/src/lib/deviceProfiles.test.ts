import { describe, expect, it, vi } from "vitest";
import { createKidsProfile, describeSchedule } from "./deviceProfiles";

describe("device profiles", () => {
  it("creates the default Kids profile with access after 19:00 and an all-day weekend", () => {
    vi.stubGlobal("crypto", { randomUUID: () => "profile-id" });
    const profile = createKidsProfile({
      addresses: ["192.168.1.50"],
      services: ["youtube", "steam", "wiki"],
      weekdayStart: "19:00",
      weekdayEnd: "23:59",
      weekendAllDay: true,
    });
    expect(profile).toMatchObject({
      id: "kids-profile-id",
      name: "Kids",
      ip_addresses: ["192.168.1.50"],
      services: ["youtube", "steam", "wiki"],
      schedule: {
        weekday_windows: [{ start: "19:00", end: "23:59" }],
        weekend_mode: "all_day",
      },
    });
    expect(describeSchedule(profile)).toBe("Pon–pet 19:00–23:59; vikend cijeli dan");
    vi.unstubAllGlobals();
  });

  it("rejects an empty device list and invalid time order", () => {
    expect(() => createKidsProfile({
      addresses: [], services: ["youtube"], weekdayStart: "19:00", weekdayEnd: "23:59", weekendAllDay: true,
    })).toThrow(/IP adresu/);
    expect(() => createKidsProfile({
      addresses: ["192.168.1.50"], services: ["youtube"], weekdayStart: "22:00", weekdayEnd: "20:00", weekendAllDay: true,
    })).toThrow(/Kraj radnog dana/);
  });
});
