// IPv4 helpers for the trusted-network boundary. The router admits the
// dashboard on the WireGuard interface, but its API still answers only
// sources inside trusted_networks, so a tunnel subnet missing from that
// list leaves WireGuard clients with a page that cannot sign in.

function ipv4ToNumber(address: string): number | null {
  const parts = address.split(".");
  if (parts.length !== 4) return null;
  let value = 0;
  for (const part of parts) {
    if (!/^\d{1,3}$/.test(part)) return null;
    const octet = Number(part);
    if (octet > 255) return null;
    value = value * 256 + octet;
  }
  return value;
}

function parseIPv4Cidr(cidr: string): { network: number; prefix: number } | null {
  const [address, rawPrefix] = cidr.trim().split("/");
  if (rawPrefix === undefined || !/^\d{1,2}$/.test(rawPrefix)) return null;
  const prefix = Number(rawPrefix);
  const value = ipv4ToNumber(address);
  if (value === null || prefix > 32) return null;
  const size = 2 ** (32 - prefix);
  return { network: value - (value % size), prefix };
}

function formatIPv4(value: number): string {
  return [24, 16, 8, 0].map((shift) => Math.floor(value / 2 ** shift) % 256).join(".");
}

/** Normalises an interface address such as 10.8.0.1/24 to its network, 10.8.0.0/24. */
export function ipv4NetworkOf(cidr: string): string | null {
  const parsed = parseIPv4Cidr(cidr);
  return parsed ? `${formatIPv4(parsed.network)}/${parsed.prefix}` : null;
}

/** True when one trusted IPv4 network contains the whole target network. */
export function trustedNetworksCover(trusted: string[], target: string): boolean {
  const wanted = parseIPv4Cidr(target);
  if (!wanted) return false;
  return trusted.some((entry) => {
    const candidate = parseIPv4Cidr(entry);
    if (!candidate || candidate.prefix > wanted.prefix) return false;
    const size = 2 ** (32 - candidate.prefix);
    return wanted.network - (wanted.network % size) === candidate.network;
  });
}

/**
 * Returns the WireGuard tunnel network when WireGuard is enabled for
 * administration but its clients are not trusted, otherwise null.
 */
export function untrustedWireGuardNetwork(config: {
  trusted_networks?: string[];
  system?: { management_access?: string };
  wireguard?: { enabled: boolean; address: string };
}): string | null {
  const wireguard = config.wireguard;
  if (!wireguard?.enabled || config.system?.management_access === "lan_only") return null;
  const tunnel = ipv4NetworkOf(wireguard.address);
  if (!tunnel) return null;
  return trustedNetworksCover(config.trusted_networks ?? [], tunnel) ? null : tunnel;
}
