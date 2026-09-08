import { describe, expect, it } from "vitest";
import { qosStatus } from "./qos";

const config = {
  wan: { enabled: true, interface: "eth0", username: "", password: "", mtu: 1492 },
  qos: { enabled: true, algorithm: "cake", download_limit_mbps: 100, upload_limit_mbps: 20 },
};
const cake = [
  { interface: "ppp0", kind: "cake", root: true },
  { interface: "ppp0", kind: "ingress", root: false },
  { interface: "ifb0", kind: "cake", root: true },
];

describe("QoS runtime evidence", () => {
  it("distinguishes off, unavailable telemetry and failed attachment", () => {
    expect(qosStatus({ ...config, qos: { ...config.qos, enabled: false } })).toBe("Off");
    expect(qosStatus(config)).toBe("QoS unavailable");
    expect(qosStatus(config, { available: false, devices: cake })).toBe("QoS unavailable");
    expect(qosStatus(config, { available: true, devices: [] })).toBe("Not applied");
  });
  it("requires both CAKE roots and target ingress on the expected WAN interface", () => {
    expect(qosStatus(config, { available: true, devices: cake })).toBe("Active");
    for (let missing = 0; missing < cake.length; missing++) {
      expect(qosStatus(config, { available: true, devices: cake.filter((_, i) => i !== missing) })).toBe("Not applied");
    }
    const eth = cake.map(device => ({ ...device, interface: device.interface === "ppp0" ? "eth0" : device.interface }));
    expect(qosStatus(config, { available: true, devices: eth })).toBe("Not applied");
    expect(qosStatus({ ...config, wan: { ...config.wan, enabled: false } }, { available: true, devices: eth })).toBe("Active");
  });
  it("requires HTB and the expected fq_codel child on both devices", () => {
    const fqConfig = { ...config, qos: { ...config.qos, algorithm: "fq_codel" } };
    const devices = [cake[1], ...["ppp0", "ifb0"].flatMap(iface => [
      { interface: iface, kind: "htb", root: true },
      { interface: iface, kind: "fq_codel", root: false, parent: "1:10" },
    ])];
    expect(qosStatus(fqConfig, { available: true, devices })).toBe("Active");
    for (let missing = 0; missing < devices.length; missing++) {
      expect(qosStatus(fqConfig, { available: true, devices: devices.filter((_, i) => i !== missing) })).toBe("Not applied");
    }
    expect(qosStatus(fqConfig, { available: true, devices: devices.map(device => ({ ...device, parent: "9:10" })) })).toBe("Not applied");
    expect(qosStatus(fqConfig, { available: true, devices: cake })).toBe("Not applied");
  });
});
