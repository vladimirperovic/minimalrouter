import { apiFetch } from "./api";
import { auditQuery, type AuditFilters, type AuditPage } from "./logs";

/** Bounded cursor traversal. An upper timestamp excludes later live arrivals;
 * retention may still remove records while exporting, so report count changes. */
export async function collectAuditHistory(filters: AuditFilters, signal: AbortSignal) {
  const params = new URLSearchParams(auditQuery(filters, ""));
  params.set("limit", "500");
  const startedAt = new Date().toISOString();
  let first: AuditPage | null = null;
  const events: AuditPage["events"] = [], ids = new Set<string>(), cursors = new Set<string>();
  let changed = false;
  for (let pageNumber = 0; pageNumber < 10; pageNumber++) {
    const response = await apiFetch(`/api/v1/audit/events?${params}`, { signal, cache: "no-store" });
    if (!response.ok) throw new Error(`Diagnostic export unavailable (${response.status})`);
    const page = await response.json() as AuditPage;
    if (!Array.isArray(page.events) || typeof page.has_more !== "boolean") throw new Error("This router does not support complete audit exports");
    if (!first) {
      first = page;
      // Use router evidence, not the browser clock. Include retained timestamps
      // from before a router clock correction as well as the current server time.
      const upper = Math.max(...[page.generated_at, page.newest_at].filter((value): value is string => !!value).map(Date.parse).filter(Number.isFinite));
      if (!Number.isFinite(upper)) throw new Error("Router did not provide an audit export time boundary");
      if (!params.get("until") || Date.parse(params.get("until")!) > upper) params.set("until", new Date(upper).toISOString());
    }
    changed ||= page.matching_count !== first.matching_count;
    for (const event of page.events) {
      if (!ids.has(event.id)) { ids.add(event.id); events.push(event); }
    }
    if (!page.has_more) return {
      ...first, events, has_more: false, next_cursor: undefined,
      filters: Object.fromEntries(new URLSearchParams(auditQuery(filters, ""))),
      export_until: params.get("until"), export_started_at: startedAt, export_completed_at: new Date().toISOString(),
      history_changed_during_export: changed || events.length !== first.matching_count,
    };
    if (!page.next_cursor || cursors.has(page.next_cursor)) throw new Error("Diagnostic export pagination did not advance");
    cursors.add(page.next_cursor);
    params.set("cursor", page.next_cursor);
  }
  throw new Error("Retained history changed during export; refresh and retry");
}
