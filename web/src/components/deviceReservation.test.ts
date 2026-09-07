import { describe, expect, it } from "vitest";
import type { StaticLease } from "../api-types";
import { insidePool, isValidIPv4, liveLeaseConflictMessage, reservationConflictMessage } from "./deviceReservation";
import type { LiveLease } from "./deviceReservation";

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

describe("live lease collisions", () => {
  const now = 1_800_000_000_000;
  const live: LiveLease[] = [
    { ip_address: "192.168.1.14", mac: "aa:bb:cc:dd:ee:09", hostname: "Vladimirs-Air", expires_at: 1_800_000_600 },
    { ip_address: "192.168.1.15", mac: "aa:bb:cc:dd:ee:0a", hostname: "Old-Phone", expires_at: 1_799_999_000 },
  ];

  it("allows a free address", () => {
    expect(liveLeaseConflictMessage("192.168.1.16", "3c:a6:f6:40:41:12", live, now)).toBe("");
  });

  it("allows an address whose lease already expired", () => {
    expect(liveLeaseConflictMessage("192.168.1.15", "3c:a6:f6:40:41:12", live, now)).toBe("");
  });

  it("blocks an actively leased address and names the holder", () => {
    expect(liveLeaseConflictMessage("192.168.1.14", "3c:a6:f6:40:41:12", live, now)).toBe(
      "192.168.1.14 is currently leased to Vladimirs-Air (aa:bb:cc:dd:ee:09, in 10 min). Choose another address.",
    );
  });

  it("ignores the target device's own lease", () => {
    expect(liveLeaseConflictMessage("192.168.1.14", "aa:bb:cc:dd:ee:09", live, now)).toBe("");
  });

  it("stays blocked when expiry is unknown", () => {
    const unknown: LiveLease[] = [{ ip_address: "192.168.1.14", mac: "aa:bb:cc:dd:ee:09" }];
    expect(liveLeaseConflictMessage("192.168.1.14", "3c:a6:f6:40:41:12", unknown, now)).toContain("Choose another address.");
  });
});
