import { describe, expect, it } from "vitest";
import { ddnsPublicNote, ddnsState } from "./ddnsStatus";

describe("Dynamic DNS state", () => {
  it("claims sync only when the published address matches", () => {
    expect(ddnsState(true, { running: true, last_ip: "192.0.2.1", resolved_ip: "192.0.2.1", in_sync: true })).toBe("in-sync");
    expect(ddnsState(true, { running: true, last_ip: "192.0.2.1", resolved_ip: "198.51.100.9" })).toBe("mismatch");
  });

  it("treats a missing lookup as unverified, not as a mismatch", () => {
    expect(ddnsState(true, { running: true, last_ip: "192.0.2.1" })).toBe("running");
  });

  it("reports disabled, stopped and unknown services", () => {
    expect(ddnsState(false, { running: true, in_sync: true })).toBe("disabled");
    expect(ddnsState(true, { running: false, resolved_ip: "192.0.2.1" })).toBe("stopped");
    expect(ddnsState(true, undefined)).toBe("unknown");
  });
});

describe("Dynamic DNS public note", () => {
  it("names the published address only when it differs", () => {
    expect(ddnsPublicNote(undefined)).toMatch(/not verified/);
    expect(ddnsPublicNote({ running: true, last_ip: "192.0.2.1", resolved_ip: "192.0.2.1", in_sync: true })).toMatch(/matches/);
    expect(ddnsPublicNote({ running: true, last_ip: "192.0.2.1", resolved_ip: "198.51.100.9" })).toContain("198.51.100.9");
  });
});
