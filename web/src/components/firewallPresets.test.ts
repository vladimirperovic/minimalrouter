import { describe, expect, it } from "vitest";
import { PRESETS, ruleNameFor } from "./FirewallPresets";

// The appliance validates every custom rule name against safeNamePattern in
// internal/config/validation.go:
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
