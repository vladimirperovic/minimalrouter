import { describe, expect, it } from "vitest";
import { ipv4NetworkOf, trustedNetworksCover, untrustedWireGuardNetwork } from "./trustedNetworks";

describe("trusted networks", () => {
  it("normalises an interface address to its network", () => {
    expect(ipv4NetworkOf("10.8.0.1/24")).toBe("10.8.0.0/24");
    expect(ipv4NetworkOf("192.168.1.1/32")).toBe("192.168.1.1/32");
    expect(ipv4NetworkOf("not-an-address")).toBeNull();
  });

  it("accepts only networks that contain the whole target", () => {
    expect(trustedNetworksCover(["10.8.0.0/24"], "10.8.0.0/24")).toBe(true);
    expect(trustedNetworksCover(["10.0.0.0/8"], "10.8.0.0/24")).toBe(true);
    expect(trustedNetworksCover(["10.8.0.2/32"], "10.8.0.0/24")).toBe(false);
    expect(trustedNetworksCover(["192.168.1.0/24"], "10.8.0.0/24")).toBe(false);
  });

  // Live test v0.3.1 (M3): a provisioned peer reached the dashboard page but
  // every API call was refused, because the default list holds only the LAN.
  it("flags an enabled WireGuard tunnel that is not trusted", () => {
    const base = { trusted_networks: ["192.168.1.0/24"], wireguard: { enabled: true, address: "10.8.0.1/24" } };
    expect(untrustedWireGuardNetwork(base)).toBe("10.8.0.0/24");
    expect(untrustedWireGuardNetwork({ ...base, trusted_networks: ["192.168.1.0/24", "10.8.0.0/24"] })).toBeNull();
    expect(untrustedWireGuardNetwork({ ...base, wireguard: { enabled: false, address: "10.8.0.1/24" } })).toBeNull();
    // Management limited to the LAN never needs the tunnel to be trusted.
    expect(untrustedWireGuardNetwork({ ...base, system: { management_access: "lan_only" } })).toBeNull();
  });
});
