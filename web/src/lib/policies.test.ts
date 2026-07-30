import test from "node:test";
import assert from "node:assert/strict";
import {
  buildDeviceScheduleProfile,
  idFromName,
  ipv4IsValid,
  macIsValid,
  netmaskFromCIDR,
  normalizedHostname,
} from "./policies.ts";

test("policy identifiers and hostnames remain bounded and safe", () => {
  assert.equal(idFromName("Kids Tablet", "profile-abc"), "kids-tablet-profile-abc");
  assert.equal(normalizedHostname("  Dječiji tablet  "), "djeciji-tablet");
  assert.match(idFromName("x".repeat(100), "suffix"), /^[a-z0-9][a-z0-9-]{0,47}$/);
});

test("MAC, IPv4 and CIDR helpers reject malformed values", () => {
  assert.equal(macIsValid("02:00:00:00:00:10"), true);
  assert.equal(macIsValid("02:00:00:00:00"), false);
  assert.equal(ipv4IsValid("192.168.30.1"), true);
  assert.equal(ipv4IsValid("192.168.30.999"), false);
  assert.equal(netmaskFromCIDR("192.168.30.1/24"), "255.255.255.0");
  assert.equal(netmaskFromCIDR("10.20.0.1/20"), "255.255.240.0");
  assert.equal(netmaskFromCIDR("192.168.30.1/31"), null);
});

test("kids evening template blocks weekdays before 19:00 and permits weekends all day", () => {
  const profile = buildDeviceScheduleProfile({
    id: "kids-evening",
    name: "Kids evening",
    accessMode: "allow_services",
    allowedServices: ["youtube", "steam"],
    weekdayStart: "19:00",
    weekdayEnd: "23:59",
    weekendAllDay: true,
  });
  assert.deepEqual(profile.allowed_services, ["youtube", "steam"]);
  assert.deepEqual(profile.windows[0], {
    days: ["monday", "tuesday", "wednesday", "thursday", "friday"],
    start: "19:00",
    end: "23:59",
    all_day: false,
  });
  assert.deepEqual(profile.windows[1], {
    days: ["saturday", "sunday"],
    all_day: true,
  });
});
