import { describe, expect, it } from "vitest";
import type { RouterConfig } from "../api-types";
import { PRESETS, ruleNameFor } from "./FirewallPresets";
import { dnsEnforcementState, setDNSEnforcement } from "../lib/firewall";

const withRules = (rules: RouterConfig["firewall"]["custom_rules"] = []) =>
  ({ firewall: { custom_rules: rules } }) as unknown as RouterConfig;

describe("router DNS enforcement", () => {
  it("starts off and becomes active with deny rules for udp/tcp 53 and udp/tcp 853", () => {
    const config = withRules();
    expect(dnsEnforcementState(config.firewall.custom_rules)).toBe("Off");
    setDNSEnforcement(config, true);
    expect(dnsEnforcementState(config.firewall.custom_rules)).toBe("Active");
    const ports = config.firewall.custom_rules
      .filter((rule) => rule.enabled && rule.action === "deny" && rule.scope === "wan")
      .map((rule) => `${rule.protocol}/${rule.dst_port}`)
      .sort();
    expect(ports).toEqual(["tcp/53", "tcp/853", "udp/53", "udp/853"]);
  });

  it("treats a TCP-only DNS-over-TLS rule from older releases as partial and completes it", () => {
    const legacy = (id: string, protocol: string, port: number, preset: string) =>
      ({ id, preset_id: preset, name: "Legacy (preset)", action: "deny", direction: "forward", protocol, src_ip: "", dst_port: port, scope: "wan", enabled: true });
    const config = withRules([
      legacy("preset-dns-udp", "udp", 53, "dns"),
      legacy("preset-dns-tcp", "tcp", 53, "dns"),
      legacy("preset-dot-tcp", "tcp", 853, "dot"),
    ] as RouterConfig["firewall"]["custom_rules"]);
    expect(dnsEnforcementState(config.firewall.custom_rules)).toBe("Partial");
    setDNSEnforcement(config, true);
    expect(dnsEnforcementState(config.firewall.custom_rules)).toBe("Active");
    expect(config.firewall.custom_rules.filter((rule) => rule.preset_id === "dot").map((rule) => rule.id).sort()).toEqual(["preset-dot-tcp", "preset-dot-udp"]);
  });

  it("pauses its own rules when switched off and leaves other rules alone", () => {
    const other = { id: "keep", name: "Keep", action: "deny", direction: "forward", protocol: "tcp", dst_port: 25, enabled: true, scope: "wan" };
    const config = withRules([other] as RouterConfig["firewall"]["custom_rules"]);
    setDNSEnforcement(config, true);
    setDNSEnforcement(config, false);
    expect(dnsEnforcementState(config.firewall.custom_rules)).toBe("Off");
    expect(config.firewall.custom_rules.find((rule) => rule.id === "keep")?.enabled).toBe(true);
    expect(config.firewall.custom_rules.filter((rule) => rule.preset_id).every((rule) => !rule.enabled)).toBe(true);
  });

  it("reports partial enforcement when only one of the presets is active", () => {
    const config = withRules();
    setDNSEnforcement(config, true);
    config.firewall.custom_rules = config.firewall.custom_rules.map((rule) =>
      rule.preset_id === "dot" ? { ...rule, enabled: false } : rule,
    );
    expect(dnsEnforcementState(config.firewall.custom_rules)).toBe("Partial");
  });
});

// The appliance validates every custom rule name against safeNamePattern in
// rust/crates/mr-config/src/config/validation.rs:
//   ^[\pL\pN][\pL\pN ._()/-]{0,63}$
// A preset whose generated name falls outside it cannot be switched on at all:
// the write is refused and the operator only sees a banner at the top of the
// page. An apostrophe in one title shipped exactly that.
const SAFE_NAME = /^[\p{L}\p{N}][\p{L}\p{N} ._()/-]{0,63}$/u;

describe("firewall preset rule names", () => {
  it("every preset produces a name the appliance accepts", () => {
    for (const preset of PRESETS) {
      const name = ruleNameFor(preset);
      expect(name, `preset "${preset.id}" generates a rejected rule name: ${name}`).toMatch(SAFE_NAME);
    }
  });

  it("every preset name fits the 64 character limit", () => {
    for (const preset of PRESETS) {
      expect(ruleNameFor(preset).length, `preset "${preset.id}" name is too long`).toBeLessThanOrEqual(64);
    }
  });

  it("preset ids are unique so a rule can be matched back to its preset", () => {
    const ids = PRESETS.map((preset) => preset.id);
    expect(new Set(ids).size).toBe(ids.length);
  });
});
