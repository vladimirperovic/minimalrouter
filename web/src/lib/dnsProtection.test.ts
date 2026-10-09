import { describe, expect, it } from "vitest";
import { normalizedException, parseDNSStatus, sameDNSPolicy } from "./dnsProtection";
import { hourCoverage, normalizeDayWindows, paintScheduleHour } from "./deviceProfiles";

describe("DNS protection", () => {
  it("compares policies across omitted false categories and empty reasons", () => {
    expect(sameDNSPolicy({ revision: 1, categories: {}, exceptions: [{ domain: "school.example.com", reason: "" }] }, { revision: 2, categories: { ads: false }, exceptions: [{ domain: "school.example.com" }] })).toBe(true);
    expect(sameDNSPolicy({ revision: 1, categories: {}, exceptions: [] }, { revision: 1, categories: { ads: true }, exceptions: [] })).toBe(false);
  });
  it("rejects incomplete status rather than displaying a successful zero", () => {
    for (const value of [{}, null, { policy: { revision: 1, categories: {}, exceptions: [] }, healthy: true }]) expect(() => parseDNSStatus(value)).toThrow(/invalid response/);
  });
  it("normalizes public exceptions and rejects URLs, local names and IP addresses", () => {
    expect(normalizedException(" School.Example.com. ")).toBe("school.example.com");
    for (const value of ["https://example.com", "*.example.com", "192.0.2.4", "router.lan", "a..com"]) expect(() => normalizedException(value)).toThrow();
  });
  it("changes only the painted hour and preserves partial windows elsewhere", () => {
    const days = normalizeDayWindows({ day_windows: { monday: [{ start: "19:30", end: "22:30" }], tuesday: [{ start: "07:15", end: "08:45" }] } });
    expect(hourCoverage(days.monday,19)).toBe(0.5);
    expect(paintScheduleHour(days.monday,21,false)).toEqual([{start:"19:30",end:"21:00"},{start:"22:00",end:"22:30"}]);
    expect(paintScheduleHour(days.monday,19,true)).toEqual([{start:"19:00",end:"22:30"}]);
    expect(days.tuesday).toEqual([{start:"07:15",end:"08:45"}]);
    expect(days.monday).toEqual([{start:"19:30",end:"22:30"}]);
    expect(paintScheduleHour([{start:"22:15",end:"23:59"}],23,false)).toEqual([{start:"22:15",end:"23:00"}]);
    expect(paintScheduleHour([],23,true)).toEqual([{start:"23:00",end:"23:59"}]);
  });
});
