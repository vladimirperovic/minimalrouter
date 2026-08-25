import { describe, expect, it } from "vitest";
import { insidePool, suggestReservationAddress } from "./deviceReservation";

// These mirror the appliance's own rules in internal/config/validation.go. A
// control that offers a value the appliance refuses is a dead end for the
// operator: the save fails and the error lands in a banner at the top of the
// page, far from the field that caused it.

const CREDENTIAL_NAME = /^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$/;
const LEASE_TIME = /^\s*\d+\s*[mh]\s*$/;
const FQDN = /^[A-Za-z0-9][A-Za-z0-9.-]*\.[A-Za-z0-9][A-Za-z0-9.-]*$/;

describe("DHCP reservation addresses", () => {
  // dhcp.static_leases[].ip_address must not overlap the dynamic pool, so the
  // dialog must never open on the device's current lease.
  it("suggests an address below the pool rather than the current lease", () => {
    const suggested = suggestReservationAddress("192.168.1.1", "192.168.1.100", []);
    expect(suggested).toBe("192.168.1.2");
    expect(insidePool(suggested, "192.168.1.100", "192.168.1.200")).toBe(false);
  });

  it("skips addresses that are already reserved or leased", () => {
    const suggested = suggestReservationAddress("192.168.1.1", "192.168.1.100", [
      "192.168.1.2",
      "192.168.1.3",
    ]);
    expect(suggested).toBe("192.168.1.4");
  });

  it("returns nothing when the subnet has no room below the pool", () => {
    expect(suggestReservationAddress("192.168.1.1", "192.168.1.2", [])).toBe("");
  });

  it("never suggests the gateway itself", () => {
    // dhcp.static_leases[].ip_address "cannot use the router LAN address"
    expect(suggestReservationAddress("192.168.1.1", "192.168.1.100", [])).not.toBe("192.168.1.1");
  });
});

describe("lease time", () => {
  // time.ParseDuration has no day unit, and validation caps the value at 168h.
  it("accepts the units the appliance parses", () => {
    for (const value of ["30m", "12h", "168h", "1m"]) {
      expect(value, value).toMatch(LEASE_TIME);
    }
  });

  it("rejects days, which time.ParseDuration cannot read", () => {
    expect("7d").not.toMatch(LEASE_TIME);
  });
});

describe("proxy credentials", () => {
  it("accepts what credentialNamePattern accepts", () => {
    for (const value of ["proxyadmin", "proxy.admin", "proxy_admin", "proxy-admin", "a1"]) {
      expect(value, value).toMatch(CREDENTIAL_NAME);
    }
  });

  it("rejects what the appliance refuses", () => {
    for (const value of ["proxy admin", "_proxy", ".proxy", "proxy@admin", ""]) {
      expect(value, value).not.toMatch(CREDENTIAL_NAME);
    }
  });
});

describe("dynamic DNS hostnames", () => {
  // cloudflare.domain and zone_name must contain a dot.
  it("requires a fully qualified name", () => {
    expect("router.example.net").toMatch(FQDN);
    expect("example.com").toMatch(FQDN);
    expect("myrouter").not.toMatch(FQDN);
  });
});
