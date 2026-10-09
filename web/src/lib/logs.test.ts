import { describe, expect, it } from "vitest";
import { EMPTY_AUDIT_FILTERS, auditDetails, auditQuery, bootStatus } from "./logs";

describe("logs data contracts", () => {
  it("formats nullable metadata and retains every field", () => {
    expect(auditDetails(null)).toBe("Recorded");
    expect(auditDetails(undefined)).toBe("Recorded");
    const fields = Object.fromEntries(Array.from({ length: 10 }, (_, index) => [`key${index}`, `value${index}`]));
    expect(auditDetails(fields)).toContain("key9: value9");
  });
  it("encodes filters and timezone-bearing bounds without query injection", () => {
    const query = new URLSearchParams(auditQuery({ ...EMPTY_AUDIT_FILTERS, q: " test&actor=someone ", since: "2026-10-09T10:00", category: "network" }, "cursor+/"));
    expect(query.get("q")).toBe("test&actor=someone");
    expect(query.has("actor")).toBe(false);
    expect(query.get("since")).toBe(new Date("2026-10-09T10:00").toISOString());
    expect(query.get("cursor")).toBe("cursor+/");
  });
  it("does not equate a legacy completed capture with a ready router", () => {
    const boot = { id: "test", started_at: "2026-10-09T00:00:00Z", completed: true };
    expect(bootStatus(boot)).toBe("unknown");
    expect(bootStatus({ ...boot, status: "timeout" })).toBe("timeout");
    expect(bootStatus({ ...boot, completion_reason: "ready" })).toBe("ready");
    expect(bootStatus({ ...boot, completed: false })).toBe("capturing");
  });
});
