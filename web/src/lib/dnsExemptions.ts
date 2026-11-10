import type { RouterConfig } from "../api-types";
import type { DNSDevice } from "./dnsProtection";

export type DNSExemption = { ip_address: string; hostname: string };

/** Matches MAX_DNS_EXEMPT_DEVICES in the router configuration validator. */
export const MAX_DNS_EXEMPTIONS = 32;

const ipv4Order = (ip: string) => ip.split(".").reduce((sum, part) => sum * 256 + Number(part), 0);

export const exemptionsOf = (config: RouterConfig | null | undefined): DNSExemption[] => config?.adguard.exempt_devices ?? [];

/** Why a device cannot be exempted right now; empty when it can. */
export function exemptionBlocker(config: RouterConfig | null | undefined, device: DNSDevice): string {
  if (!config) return "";
  if (exemptionsOf(config).some((item) => item.ip_address === device.ip)) return "";
  if (!device.reserved) return "Needs a DHCP reservation first";
  const profiled = (config.adguard.device_profiles as Array<{ ip_addresses?: string[] }>).some((profile) => profile.ip_addresses?.includes(device.ip));
  if (profiled) return "Used by a device profile, which needs router DNS";
  if (exemptionsOf(config).length >= MAX_DNS_EXEMPTIONS) return `Limit of ${MAX_DNS_EXEMPTIONS} devices reached`;
  return "";
}

/**
 * A candidate configuration with the device added to or removed from the
 * exemption list. Nothing is sent: the caller previews and applies it like any
 * other network change. An empty list is omitted, as the router stores it.
 */
export function withExemption(config: RouterConfig, device: Pick<DNSDevice, "ip" | "name">, exempt: boolean): RouterConfig {
  const candidate = structuredClone(config);
  const others = exemptionsOf(candidate).filter((item) => item.ip_address !== device.ip);
  const next = exempt ? [...others, { ip_address: device.ip, hostname: device.name }] : others;
  next.sort((a, b) => ipv4Order(a.ip_address) - ipv4Order(b.ip_address));
  if (next.length) candidate.adguard = { ...candidate.adguard, exempt_devices: next };
  else delete candidate.adguard.exempt_devices;
  return candidate;
}
