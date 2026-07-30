import { describe, expect, it, vi } from "vitest";
import { createKidsProfile, describeSchedule } from "./deviceProfiles";

describe("device profiles", () => {
  it("creates a Kids profile with weekday hours and an all-day weekend", () => {
    vi.stubGlobal("crypto", { randomUUID: () => "profile-id" });
    const profile = createKidsProfile({
      addresses: ["192.168.1.50"],
      services: ["youtube", "steam"],
      weekdayStart: "17:00",
      weekdayEnd: "21:00",
      weekendAllDay: true,
    });
    expect(profile).toMatchObject({
      id: "kids-profile-id",
      name: "Kids",
      ip_addresses: ["192.168.1.50"],
      services: ["youtube", "steam"],
      schedule: {
        weekday_windows: [{ start: "17:00", end: "21:00" }],
        weekend_mode: "all_day",
      },
    });
    expect(describeSchedule(profile)).toBe("Pon–pet 17:00–21:00; vikend cijeli dan");
    vi.unstubAllGlobals();
  });

  it("rejects an empty device list and invalid time order", () => {
    expect(() => createKidsProfile({
      addresses: [], services: ["youtube"], weekdayStart: "17:00", weekdayEnd: "21:00", weekendAllDay: true,
    })).toThrow(/IP adresu/);
    expect(() => createKidsProfile({
      addresses: ["192.168.1.50"], services: ["youtube"], weekdayStart: "22:00", weekdayEnd: "20:00", weekendAllDay: true,
    })).toThrow(/Kraj radnog dana/);
  });
});
