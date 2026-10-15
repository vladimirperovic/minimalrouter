import type { FirewallCustomRule, RouterConfig } from "../api-types";
import publicResolvers from "./publicResolvers.json";

export type FirewallProps = {
  config: RouterConfig;
  busy: boolean;
  applyConfig: (
    mutate: (next: RouterConfig) => void,
    success: string,
  ) => Promise<boolean>;
};
export type FirewallStatus = {
  demo?: boolean;
  revision: number;
  runtime: {
    available: boolean;
    observed_at: string | null;
    default_deny: boolean;
    policies: Record<string, string>;
    loaded_revision: number | null;
    revision_matches: boolean;
    configuration_match: string;
  };
  rules: Array<{
    id: string;
    name: string;
    scope: string;
    packets: number | null;
    last_increase_observed_at: string | null;
  }>;
  warnings: string[];
  pending_transaction: { id: string; state: string } | null;
  engine: { recovery_required?: boolean; apply_in_progress?: boolean };
};
export function ruleScope(rule: FirewallCustomRule) {
  return rule.direction === "input"
    ? "router"
    : rule.action === "allow" || rule.scope === "wan"
      ? "wan"
      : "all";
}
export const scopeLabel = (rule: FirewallCustomRule) =>
  ({
    router: "To router",
    wan: "LAN → Internet",
    all: "LAN → All routed networks",
  })[ruleScope(rule)];
export const safeRuleName = (value: string) =>
  /^[\p{L}\p{N}][\p{L}\p{N} ._()/-]{0,63}$/u.test(value);
function ipv4(value: string): number | null {
  const parts = value.split(".");
  if (
    parts.length !== 4 ||
    parts.some((p) => !/^(0|[1-9]\d{0,2})$/.test(p) || +p > 255)
  )
    return null;
  return parts.reduce((n, p) => (n * 256 + +p) >>> 0, 0);
}
export function validLANSource(value: string, lan: string) {
  if (!value) return true;
  const source = value.split("/"),
    network = lan.split("/");
  if (source.length > 2 || network.length !== 2) return false;
  const ip = ipv4(source[0]),
    net = ipv4(network[0]);
  const bits =
    source.length === 1 ? 32 : /^\d{1,2}$/.test(source[1]) ? +source[1] : -1;
  const prefix = +network[1];
  if (
    ip === null ||
    net === null ||
    bits < prefix ||
    bits > 32 ||
    prefix < 0 ||
    prefix > 32
  )
    return false;
  const mask = prefix === 0 ? 0 : (0xffffffff << (32 - prefix)) >>> 0;
  return (ip & mask) === (net & mask);
}
export function validateRule(rule: FirewallCustomRule, config: RouterConfig) {
  if (!safeRuleName(rule.name))
    return "Use 1–64 characters: letters, digits, spaces, . _ ( ) / or -. Start with a letter or digit.";
  if (!validLANSource(rule.src_ip || "", config.lan.cidr))
    return `Source must be an IPv4 address or subnet within ${config.lan.cidr}. Leave blank for all LAN devices.`;
  if (rule.direction === "input" && rule.action === "deny")
    return "Router-input denies are unavailable because they can lock out management.";
  if (
    rule.direction === "input" &&
    config.system.management_access === "wireguard_only"
  )
    return "LAN input allowances are unavailable while management is WireGuard-only.";
  if (
    (rule.protocol === "tcp" || rule.protocol === "udp") &&
    (!Number.isInteger(rule.dst_port) ||
      rule.dst_port! < 1 ||
      rule.dst_port! > 65535)
  )
    return "Destination port must be between 1 and 65535.";
  return "";
}
export type Preset = {
  id: string;
  title: string;
  detail: string;
  caution?: string;
  protocol: "tcp" | "udp";
  protocols?: Array<"tcp" | "udp">;
  // A single-port preset writes dst_port; a multi-port preset writes
  // dst_ports instead, and a destination-scoped preset also writes dst_ips.
  port?: number;
  ports?: readonly number[];
  destinations?: readonly string[];
  direction: "forward";
  group: string;
};

// Well-known public resolvers that answer DNS-over-HTTPS, DNS-over-TLS/QUIC or
// alternate-port DNS on their own addresses. Blocking their names in the DNS
// filter does not stop a device that connects to https://1.1.1.1/dns-query by
// address. The list, with each provider's source, lives in
// publicResolvers.json; an internal/services test checks that the appliance
// accepts and renders it unchanged.
export const PUBLIC_RESOLVER_PROVIDERS: readonly string[] =
  publicResolvers.providers.map((provider) => provider.name);
export const PUBLIC_RESOLVER_ADDRESSES: readonly string[] =
  publicResolvers.providers.flatMap((provider) => provider.addresses);
export const PUBLIC_RESOLVER_PORTS: readonly number[] = publicResolvers.ports;
export const PUBLIC_RESOLVER_PRESET_ID = "public-resolvers";

export const presetPorts = (preset: Preset): readonly number[] =>
  preset.ports || (preset.port ? [preset.port] : []);
export const PRESETS: readonly Preset[] = [
  {
    id: "smb",
    title: "Block Windows file sharing leaving the network",
    detail:
      "Block Internet-bound SMB. File sharing within the LAN and private tunnels is unaffected.",
    protocol: "tcp",
    port: 445,
    direction: "forward",
    group: "Legacy services",
  },
  {
    id: "netbios",
    title: "Block outbound NetBIOS",
    detail:
      "Prevent UDP name-service traffic from being forwarded to the Internet. Local broadcasts are unaffected.",
    protocol: "udp",
    port: 137,
    direction: "forward",
    group: "Legacy services",
  },
  {
    id: "telnet",
    title: "Block Telnet",
    detail:
      "Prevent unencrypted Telnet connections from LAN devices to the Internet.",
    protocol: "tcp",
    port: 23,
    direction: "forward",
    group: "Legacy services",
  },
  {
    id: "smtp",
    title: "Block direct outbound mail",
    detail:
      "Block direct SMTP on TCP 25. Authenticated mail submission on other ports is unaffected.",
    caution: "Review this if you run a mail server.",
    protocol: "tcp",
    port: 25,
    direction: "forward",
    group: "Internet access",
  },
  {
    id: "ftp",
    title: "Block plain FTP",
    detail:
      "Block outbound FTP control connections on TCP 21. This does not identify FTP on other ports.",
    protocol: "tcp",
    port: 21,
    direction: "forward",
    group: "Legacy services",
  },
  {
    id: "rdp",
    title: "Block Remote Desktop leaving the network",
    detail:
      "Block Internet-bound RDP on TCP 3389. Private tunnel routes remain outside this preset.",
    protocol: "tcp",
    port: 3389,
    direction: "forward",
    group: "Internet access",
  },
  {
    id: "dns",
    title: "Block direct external DNS",
    detail:
      "Block both UDP and TCP 53 toward the Internet. Router DNS remains available; this does not block encrypted DNS or VPNs.",
    caution:
      "Devices with a fixed external resolver may lose DNS. This blocks traffic; it does not redirect it.",
    protocol: "udp",
    protocols: ["udp", "tcp"],
    port: 53,
    direction: "forward",
    group: "DNS & transport",
  },
  {
    id: "dot",
    title: "Block DNS-over-TLS and DNS-over-QUIC",
    detail:
      "Block DNS-over-TLS (TCP 853) and DNS-over-QUIC (UDP 853) toward the Internet. DoH and VPNs are separate.",
    protocol: "tcp",
    protocols: ["tcp", "udp"],
    port: 853,
    direction: "forward",
    group: "DNS & transport",
  },
  {
    id: "quic",
    title: "Block QUIC",
    detail:
      "Block UDP 443 toward the Internet. DNS-over-HTTPS over TCP 443 still works; this is not a complete encrypted-DNS control.",
    caution: "Some applications may not support TCP fallback.",
    protocol: "udp",
    port: 443,
    direction: "forward",
    group: "DNS & transport",
  },
  {
    id: PUBLIC_RESOLVER_PRESET_ID,
    title: "Block public DNS resolvers by address",
    detail: `Block DNS-over-HTTPS, DNS-over-TLS/QUIC and alternate-port DNS (TCP and UDP ${PUBLIC_RESOLVER_PORTS.join(", ")}) to ${PUBLIC_RESOLVER_ADDRESSES.length} published resolver addresses and networks from ${PUBLIC_RESOLVER_PROVIDERS.join(", ")}. Router DNS and the router's own upstream lookups are unaffected. Self-hosted resolvers, other providers and VPNs are not covered.`,
    caution:
      "HTTPS to these addresses is blocked too, such as https://1.1.1.1. A device locked to one of these resolvers loses DNS instead of bypassing the router.",
    protocol: "tcp",
    protocols: ["tcp", "udp"],
    ports: PUBLIC_RESOLVER_PORTS,
    destinations: PUBLIC_RESOLVER_ADDRESSES,
    direction: "forward",
    group: "DNS & transport",
  },
  {
    id: "plex-relay",
    title: "Block outbound Plex service port",
    detail:
      "Block Internet destinations on TCP 32400. This port rule does not guarantee that Plex Relay or remote streaming is disabled.",
    protocol: "tcp",
    port: 32400,
    direction: "forward",
    group: "Internet access",
  },
];
export const ruleNameFor = (preset: Preset) => `${preset.title} (preset)`;
export const presetRules = (rules: FirewallCustomRule[], preset: Preset) =>
  rules.filter(
    (r) =>
      r.preset_id === preset.id ||
      (!r.preset_id && r.id.startsWith(`preset-${preset.id}-`)),
  );
export function presetState(
  rules: FirewallCustomRule[],
  preset: Preset,
): "Active" | "Off" | "Modified" {
  const owned = presetRules(rules, preset),
    protocols = preset.protocols || [preset.protocol];
  if (!owned.length || owned.every((r) => !r.enabled)) return "Off";
  return owned.length === protocols.length &&
    protocols.every((protocol) =>
      owned.some(
        (r) =>
          r.enabled &&
          r.action === "deny" &&
          r.direction === "forward" &&
          r.protocol === protocol &&
          samePorts(r, preset) &&
          sameSet(r.dst_ips || [], preset.destinations || []) &&
          !r.src_ip &&
          r.scope === "wan",
      ),
    )
    ? "Active"
    : "Modified";
}
const sameSet = <T>(a: readonly T[], b: readonly T[]) =>
  a.length === b.length && b.every((value) => a.includes(value));
const samePorts = (rule: FirewallCustomRule, preset: Preset) =>
  preset.ports
    ? !rule.dst_port && sameSet(rule.dst_ports || [], preset.ports)
    : rule.dst_port === preset.port && !rule.dst_ports?.length;
// DNS categories, exceptions and device schedules only see lookups that reach
// the router's resolver. These presets stop LAN devices from using another
// resolver over plain DNS, DNS-over-TLS or DNS-over-QUIC, which a live test
// showed bypassed every category (e.g. `nslookup bet365.com 8.8.8.8`). Older
// releases created only the TCP 853 rule; that reads as Partial until the
// operator applies enforcement again, which adds the UDP rule.
export const DNS_ENFORCEMENT_PRESET_IDS: readonly string[] = ["dns", "dot"];
export const dnsEnforcementPresets = () =>
  PRESETS.filter((preset) => DNS_ENFORCEMENT_PRESET_IDS.includes(preset.id));

export function dnsEnforcementState(
  rules: FirewallCustomRule[],
): "Active" | "Off" | "Partial" {
  const states = dnsEnforcementPresets().map((preset) =>
    presetState(rules, preset),
  );
  if (states.every((state) => state === "Active")) return "Active";
  if (states.every((state) => state === "Off")) return "Off";
  return "Partial";
}

// Turning enforcement off pauses the owned rules, exactly like pausing the
// presets on the Firewall page, so their ownership and history are kept.
export function setDNSEnforcement(config: RouterConfig, enforced: boolean) {
  setPresetsEnabled(config, dnsEnforcementPresets(), enforced);
}

// Offered beside DNS enforcement but tracked separately: it closes DoH by IP
// address and alternate-port DNS to well-known resolvers, which port 53/853
// rules cannot see, and it has its own cost (HTTPS to those addresses).
export const publicResolverPreset = () =>
  PRESETS.find((preset) => preset.id === PUBLIC_RESOLVER_PRESET_ID)!;
export const publicResolverBlockingState = (rules: FirewallCustomRule[]) =>
  presetState(rules, publicResolverPreset());
export function setPublicResolverBlocking(
  config: RouterConfig,
  blocked: boolean,
) {
  setPresetsEnabled(config, [publicResolverPreset()], blocked);
}

function setPresetsEnabled(
  config: RouterConfig,
  presets: readonly Preset[],
  enabled: boolean,
) {
  if (enabled) {
    applyPresets(config, presets);
    return;
  }
  const rules = config.firewall.custom_rules || [];
  const owned = new Set(
    presets.flatMap((preset) => presetRules(rules, preset).map((r) => r.id)),
  );
  config.firewall.custom_rules = rules.map((r) =>
    owned.has(r.id) ? { ...r, enabled: false } : r,
  );
}

export function applyPresets(config: RouterConfig, presets: readonly Preset[]) {
  let rules = config.firewall.custom_rules || [];
  for (const preset of presets) {
    const owned = new Set(presetRules(rules, preset).map((r) => r.id));
    rules = rules.filter((r) => !owned.has(r.id));
    for (const protocol of preset.protocols || [preset.protocol])
      rules.push({
        id: `preset-${preset.id}-${protocol}`,
        preset_id: preset.id,
        name: ruleNameFor(preset),
        action: "deny",
        direction: "forward",
        protocol,
        src_ip: "",
        ...(preset.ports
          ? { dst_ports: [...preset.ports] }
          : { dst_port: preset.port }),
        ...(preset.destinations && { dst_ips: [...preset.destinations] }),
        scope: "wan",
        enabled: true,
      });
  }
  config.firewall.custom_rules = rules;
}
