import { describe, expect, it } from "vitest";
import { CONFIG } from "../../e2e/fixtures/router";
import type { FirewallCustomRule, RouterConfig } from "../api-types";
import {
  applyPresets,
  PRESETS,
  PUBLIC_RESOLVER_ADDRESSES,
  PUBLIC_RESOLVER_PORTS,
  PUBLIC_RESOLVER_PRESET_ID,
  presetState,
  presetRules,
  ruleNameFor,
  validLANSource,
  validateRule,
} from "./firewall";

const makeConfig = () => structuredClone(CONFIG) as RouterConfig;
describe("Firewall preset ownership and semantics", () => {
  it("applies a DNS pair and another preset as one candidate without touching a manually named rule", () => {
    const cfg = makeConfig(),
      dns = PRESETS.find((p) => p.id === "dns")!,
      telnet = PRESETS.find((p) => p.id === "telnet")!;
    cfg.firewall.custom_rules = [
      {
        id: "manual",
        name: ruleNameFor(dns),
        action: "allow",
        direction: "forward",
        protocol: "tcp",
        dst_port: 999,
        enabled: true,
      },
    ];
    applyPresets(cfg, [dns, telnet]);
    expect(cfg.firewall.custom_rules).toHaveLength(4);
    expect(presetState(cfg.firewall.custom_rules, dns)).toBe("Active");
    expect(
      presetRules(cfg.firewall.custom_rules, dns)
        .map((r) => r.protocol)
        .sort(),
    ).toEqual(["tcp", "udp"]);
    expect(cfg.firewall.custom_rules[0].dst_port).toBe(999);
    expect(
      presetRules(cfg.firewall.custom_rules, dns).every(
        (r) => r.scope === "wan",
      ),
    ).toBe(true);
    applyPresets(cfg, [dns]);
    expect(cfg.firewall.custom_rules).toHaveLength(4);
  });
  it("detects edited match/action/scope and recognizes legacy identity without relying on title", () => {
    const preset = PRESETS[0],
      cfg = makeConfig();
    applyPresets(cfg, [preset]);
    const saved = structuredClone(cfg.firewall.custom_rules[0]);
    for (const change of [
      { action: "allow" },
      { dst_port: 44 },
      { src_ip: "192.168.1.2" },
      { scope: "all" as const },
      { direction: "input" },
      { protocol: "udp" },
    ]) {
      cfg.firewall.custom_rules = [{ ...saved, ...change }];
      expect(presetState(cfg.firewall.custom_rules, preset)).toBe("Modified");
    }
    cfg.firewall.custom_rules = [{ ...saved, name: "Renamed" }];
    expect(presetState(cfg.firewall.custom_rules, preset)).toBe("Active");
    cfg.firewall.custom_rules = [
      {
        ...saved,
        preset_id: undefined,
        scope: undefined,
        id: `preset-${preset.id}-legacy`,
      },
    ];
    expect(presetState(cfg.firewall.custom_rules, preset)).toBe("Modified");
    cfg.firewall.custom_rules = [{ ...saved, enabled: false }];
    expect(presetState(cfg.firewall.custom_rules, preset)).toBe("Off");
    cfg.firewall.custom_rules = [
      { ...saved, id: "manually-named", preset_id: undefined },
    ];
    expect(presetState(cfg.firewall.custom_rules, preset)).toBe("Off");
  });
});
describe("Firewall form validation", () => {
  it("requires the whole source network inside the LAN and rejects ambiguous IPv4", () => {
    for (const value of [
      "",
      "192.168.1.50",
      "192.168.1.64/26",
      "192.168.1.0/24",
    ])
      expect(validLANSource(value, "192.168.1.1/24")).toBe(true);
    for (const value of [
      "192.168.1.0/16",
      "192.168.0.0/16",
      "192.168.2.50",
      "192.168.01.2",
      "192.168.1.2/99",
      "::1",
    ])
      expect(validLANSource(value, "192.168.1.1/24")).toBe(false);
  });
  it("matches backend safety on input rules while accepting ICMP", () => {
    const cfg = makeConfig();
    const rule = {
      id: "r",
      name: "Ping",
      direction: "input",
      action: "deny",
      protocol: "icmp",
      enabled: true,
    };
    expect(validateRule(rule, cfg)).toContain("lock out");
    rule.action = "allow";
    expect(validateRule(rule, cfg)).toBe("");
    cfg.system.management_access = "wireguard_only";
    expect(validateRule(rule, cfg)).toContain("WireGuard-only");
  });
});
describe("Public resolver preset", () => {
  const preset = () =>
    PRESETS.find((p) => p.id === PUBLIC_RESOLVER_PRESET_ID)!;

  it("lists canonical, unique resolver addresses and the requested DNS ports", () => {
    expect(PUBLIC_RESOLVER_PORTS).toEqual([443, 853, 5353, 8853]);
    expect(new Set(PUBLIC_RESOLVER_ADDRESSES).size).toBe(
      PUBLIC_RESOLVER_ADDRESSES.length,
    );
    // internal/config caps one rule at 128 destinations.
    expect(PUBLIC_RESOLVER_ADDRESSES.length).toBeLessThanOrEqual(128);
    for (const address of [
      "1.1.1.1",
      "8.8.8.8",
      "9.9.9.9",
      "208.67.222.222",
      "94.140.14.14",
      "185.228.168.9",
      "45.90.28.0/24",
      "2606:4700:4700::1111",
    ])
      expect(PUBLIC_RESOLVER_ADDRESSES).toContain(address);
    expect(preset().group).toBe("DNS & transport");
    expect(preset().detail).toMatch(/Self-hosted resolvers.*VPNs are not covered/);
  });

  it("writes TCP and UDP deny rules with destinations and a port set, never a single port", () => {
    const cfg = makeConfig();
    applyPresets(cfg, [preset()]);
    const owned = presetRules(cfg.firewall.custom_rules, preset());
    expect(owned.map((r) => r.protocol).sort()).toEqual(["tcp", "udp"]);
    for (const rule of owned) {
      expect(rule).toMatchObject({
        action: "deny",
        direction: "forward",
        scope: "wan",
        enabled: true,
        src_ip: "",
        dst_ports: [443, 853, 5353, 8853],
        dst_ips: [...PUBLIC_RESOLVER_ADDRESSES],
      });
      expect(rule).not.toHaveProperty("dst_port");
    }
    expect(presetState(cfg.firewall.custom_rules, preset())).toBe("Active");
  });

  it("treats a changed address list or port set as modified, in any order", () => {
    const cfg = makeConfig();
    applyPresets(cfg, [preset()]);
    const saved = structuredClone(cfg.firewall.custom_rules);
    const tcp = saved.findIndex((r) => r.protocol === "tcp");
    const variants: Array<Partial<FirewallCustomRule>> = [
      { dst_ips: saved[tcp].dst_ips!.slice(1) },
      { dst_ips: [...saved[tcp].dst_ips!, "203.0.113.53"] },
      { dst_ports: [443, 853] },
      { dst_port: 443 },
      { dst_ips: undefined },
    ];
    for (const change of variants) {
      cfg.firewall.custom_rules = saved.map((r, i) =>
        i === tcp ? { ...r, ...change } : r,
      );
      expect(presetState(cfg.firewall.custom_rules, preset())).toBe("Modified");
    }
    cfg.firewall.custom_rules = saved.map((r) => ({
      ...r,
      dst_ips: [...r.dst_ips!].reverse(),
      dst_ports: [...r.dst_ports!].reverse(),
    }));
    expect(presetState(cfg.firewall.custom_rules, preset())).toBe("Active");
  });

  it("keeps single-port presets single-port and flags one that gained destinations", () => {
    const cfg = makeConfig(),
      dns = PRESETS.find((p) => p.id === "dns")!;
    applyPresets(cfg, [dns]);
    expect(
      cfg.firewall.custom_rules.every(
        (r) => r.dst_port === 53 && !("dst_ports" in r) && !("dst_ips" in r),
      ),
    ).toBe(true);
    cfg.firewall.custom_rules = cfg.firewall.custom_rules.map((r) => ({
      ...r,
      dst_ips: ["8.8.8.8"],
    }));
    expect(presetState(cfg.firewall.custom_rules, dns)).toBe("Modified");
  });
});
