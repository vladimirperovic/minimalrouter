import { describe, expect, it } from "vitest";
import type { StaticLease } from "../api-types";
import { insidePool, isValidIPv4, reservationConflictMessage } from "./deviceReservation";

const leases: StaticLease[] = [
  { id: "one", hostname: "nas", mac: "aa:bb:cc:dd:ee:01", ip_address: "192.168.1.20" },
  { id: "two", hostname: "printer", mac: "aa:bb:cc:dd:ee:02", ip_address: "192.168.1.30" },
];

describe("device reservation validation", () => {
  it("accepts a free IPv4 address and MAC", () => {
    expect(reservationConflictMessage("192.168.1.40", "aa:bb:cc:dd:ee:40", leases)).toBe("");
  });

  it("rejects an invalid IPv4 address", () => {
    expect(isValidIPv4("192.168.1.999")).toBe(false);
    expect(reservationConflictMessage("192.168.1.999", "aa:bb:cc:dd:ee:40", leases)).toContain("valid IPv4");
  });

  it("blocks an address that is already reserved", () => {
    expect(reservationConflictMessage("192.168.1.20", "aa:bb:cc:dd:ee:40", leases)).toBe(
      "192.168.1.20 is already reserved for nas. Choose another address.",
    );
  });

  it("blocks a MAC that already has a reservation", () => {
    expect(reservationConflictMessage("192.168.1.40", "aa:bb:cc:dd:ee:01", leases)).toBe(
      "That MAC address already has a reservation.",
    );
  });

  it("detects addresses inside the dynamic DHCP pool", () => {
    expect(insidePool("192.168.1.120", "192.168.1.100", "192.168.1.200")).toBe(true);
    expect(insidePool("192.168.1.20", "192.168.1.100", "192.168.1.200")).toBe(false);
  });
});
