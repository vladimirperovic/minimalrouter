import { beforeEach, expect, it, vi } from "vitest";
import { apiFetch } from "./api";
import { EMPTY_AUDIT_FILTERS } from "./logs";
import { collectAuditHistory } from "./logsExport";

vi.mock("./api", () => ({ apiFetch: vi.fn() }));
beforeEach(() => vi.mocked(apiFetch).mockReset());

it("uses router time, follows cursors and reports history changes", async () => {
  const serverTime = "2035-01-02T12:00:00.000Z";
  vi.mocked(apiFetch)
    .mockResolvedValueOnce(Response.json({ events: [{ id: "first" }], matching_count: 3, generated_at: serverTime, has_more: true, next_cursor: "next" }))
    .mockResolvedValueOnce(Response.json({ events: [{ id: "second" }], matching_count: 2, generated_at: serverTime, has_more: false }));
  const signal = new AbortController().signal;
  const result = await collectAuditHistory(EMPTY_AUDIT_FILTERS, signal);
  const second = new URL(String(vi.mocked(apiFetch).mock.calls[1][0]), "https://router.example.test");
  expect(second.searchParams.get("until")).toBe(serverTime);
  expect(second.searchParams.get("cursor")).toBe("next");
  expect(result.events).toHaveLength(2);
  expect(result.history_changed_during_export).toBe(true);
  expect(vi.mocked(apiFetch).mock.calls[1][1]?.signal).toBe(signal);
});

it("never silently downloads a truncated or looping export", async () => {
  vi.mocked(apiFetch).mockResolvedValueOnce(Response.json({ events: [] }));
  await expect(collectAuditHistory(EMPTY_AUDIT_FILTERS, new AbortController().signal)).rejects.toThrow("does not support complete");
  vi.mocked(apiFetch).mockImplementation(async () => Response.json({ events: [], generated_at: "2026-10-09T08:00:00Z", has_more: true, next_cursor: "repeat" }));
  await expect(collectAuditHistory(EMPTY_AUDIT_FILTERS, new AbortController().signal)).rejects.toThrow("did not advance");
});
