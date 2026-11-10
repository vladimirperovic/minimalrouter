import { describe, expect, it } from "vitest";
import type { RouterConfig } from "../api-types";
import { MAX_DNS_EXEMPTIONS, exemptionBlocker, exemptionsOf, withExemption } from "./dnsExemptions";

const config = (adguard: Partial<RouterConfig["adguard"]> = {}) =>
  ({ adguard: { enabled: false, blocklist_url: "", last_updated: "", device_profiles: [], ...adguard } }) as unknown as RouterConfig;
const laptop = { ip: "192.168.1.14", name: "Laptop", reserved: true };

describe("DNS exemptions", () => {
  it("adds and removes a device without touching the original", () => {
    const before = config();
    const added = withExemption(before, laptop, true);
    expect(exemptionsOf(added)).toEqual([{ ip_address: "192.168.1.14", hostname: "Laptop" }]);
    expect(before.adguard.exempt_devices).toBeUndefined();
    const removed = withExemption(added, laptop, false);
    expect("exempt_devices" in removed.adguard).toBe(false);
  });

  it("keeps the list ordered by address and free of duplicates", () => {
    let current = config();
    for (const ip of ["192.168.1.14", "192.168.1.9", "192.168.1.100", "192.168.1.14"]) current = withExemption(current, { ip, name: ip }, true);
    expect(exemptionsOf(current).map((item) => item.ip_address)).toEqual(["192.168.1.9", "192.168.1.14", "192.168.1.100"]);
  });

  it("explains why a device cannot be exempted", () => {
    expect(exemptionBlocker(config(), laptop)).toBe("");
    expect(exemptionBlocker(config(), { ...laptop, reserved: false })).toBe("Needs a DHCP reservation first");
    expect(exemptionBlocker(config({ device_profiles: [{ ip_addresses: [laptop.ip] }] }), laptop)).toMatch(/device profile/);
    const full = Array.from({ length: MAX_DNS_EXEMPTIONS }, (_, index) => ({ ip_address: `192.168.1.${100 + index}`, hostname: "" }));
    expect(exemptionBlocker(config({ exempt_devices: full }), laptop)).toMatch(/Limit of 32/);
  });

  it("never blocks removing an existing exemption", () => {
    const current = config({ exempt_devices: [{ ip_address: laptop.ip, hostname: laptop.name }], device_profiles: [{ ip_addresses: [laptop.ip] }] });
    expect(exemptionBlocker(current, { ...laptop, reserved: false })).toBe("");
  });
});
