import type { FirewallCustomRule, RouterConfig } from "../api-types";

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
  port: number;
  direction: "forward";
  group: string;
};
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
    title: "Block DNS-over-TLS",
    detail:
      "Block standard DNS-over-TLS on TCP 853 toward the Internet. DoH, DNS-over-QUIC and VPNs are separate.",
    protocol: "tcp",
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
          r.dst_port === preset.port &&
          !r.src_ip &&
          r.scope === "wan",
      ),
    )
    ? "Active"
    : "Modified";
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
        dst_port: preset.port,
        scope: "wan",
        enabled: true,
      });
  }
  config.firewall.custom_rules = rules;
}
