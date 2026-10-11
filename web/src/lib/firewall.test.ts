import { describe, expect, it } from "vitest";
import { CONFIG } from "../../e2e/fixtures/router";
import type { RouterConfig } from "../api-types";
import {
  applyPresets,
  PRESETS,
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
