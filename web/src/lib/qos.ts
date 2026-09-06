import type { RouterConfig, SystemStatus } from "../api-types";

type QoSRuntime = NonNullable<SystemStatus["runtime"]>["qos"];

export function qosStatus(config: Pick<RouterConfig, "qos" | "wan">, runtime?: QoSRuntime) {
  if (!config.qos.enabled) return "Off";
  if (!runtime?.available || !Array.isArray(runtime.devices)) return "QoS unavailable";
  const target = config.wan.enabled ? "ppp0" : config.wan.interface;
  const shaped = (iface: string) => {
    const devices = runtime.devices.filter(device => device.interface === iface);
    if (config.qos.algorithm === "cake") return devices.some(device => device.root && device.kind === "cake");
    if (config.qos.algorithm !== "fq_codel") return false;
    return devices.some(device => device.root && device.kind === "htb")
      && devices.some(device => !device.root && device.kind === "fq_codel" && device.parent === "1:10");
  };
  return target && shaped(target) && shaped("ifb0")
    && runtime.devices.some(device => device.interface === target && device.kind === "ingress")
    ? "Active" : "Not applied";
}
