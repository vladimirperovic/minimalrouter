import { describe, expect, it } from "vitest";
import { assertUnchanged, subnetBounds, managementURL } from "./network";
import { suggestReservationAddress } from "../components/deviceReservation";
import type { RouterConfig } from "../api-types";

describe("LAN editing safety", () => {
  it("rejects concurrent changes instead of overwriting the fresh revision", () => {
    expect(() => assertUnchanged([{ name: "nas.lan", ip: "192.168.1.20" }], [{ name: "nas.lan", ip: "192.168.1.21" }])).toThrow("another session");
    expect(() => assertUnchanged({ id: "removed" }, undefined)).toThrow("another session");
    expect(() => assertUnchanged({ enabled: true }, { enabled: true })).not.toThrow();
  });
  it("finds usable space on both sides of the pool with any gateway position", () => {
    expect(suggestReservationAddress("192.168.1.254", "192.168.1.100", [], "192.168.1.254/24", "192.168.1.200")).toBe("192.168.1.1");
    expect(suggestReservationAddress("192.168.1.1", "192.168.1.2", ["192.168.1.251"], "192.168.1.1/24", "192.168.1.250")).toBe("192.168.1.252");
    expect(suggestReservationAddress("192.168.1.1", "192.168.1.2", [], "192.168.1.1/24", "192.168.1.254")).toBe("");
  });
  it("handles actual CIDR bounds without scanning a large subnet", () => {
    expect(subnetBounds("192.168.1.254/24")).toEqual({ first: 3232235776, last: 3232236031 });
    expect(subnetBounds("192.168.1.1/99")).toBeNull();
    expect(suggestReservationAddress("10.0.0.1", "10.0.0.2", [], "10.0.0.1/8", "10.255.255.253")).toBe("10.255.255.254");
  });
  it("constructs the explicit candidate management address", () => {
    expect(managementURL({ lan: { ip_address: "192.168.1.254" }, system: { https_enabled: true, https_port: 8443 } } as RouterConfig)).toBe("https://192.168.1.254:8443/#network");
  });
});
