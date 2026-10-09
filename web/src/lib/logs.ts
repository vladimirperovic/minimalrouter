export type AuditCategory = "all" | "security" | "configuration" | "network" | "recovery";
export const AUDIT_CATEGORIES: AuditCategory[] = ["all", "security", "configuration", "network", "recovery"];
export type AuditEvent = { id: string; event_type: string; actor: string; timestamp: string; category?: Exclude<AuditCategory, "all">; details?: Record<string, string> | null };
export type AuditFilters = { category: AuditCategory; q: string; actor: string; event_type: string; since: string; until: string };
export const EMPTY_AUDIT_FILTERS: AuditFilters = { category: "all", q: "", actor: "", event_type: "", since: "", until: "" };
export type AuditPage = {
  events: AuditEvent[]; matching_count?: number; category_counts?: Partial<Record<AuditCategory, number>>;
  retained_count?: number; retention_limit?: number; has_more?: boolean; next_cursor?: string;
  oldest_at?: string; newest_at?: string; generated_at?: string; notice?: string;
};

export function auditQuery(filters: AuditFilters, cursor: string) {
  const query = new URLSearchParams({ limit: "100" });
  for (const [key, value] of Object.entries(filters)) {
    if (!value.trim() || (key === "category" && value === "all")) continue;
    const date = key === "since" || key === "until" ? new Date(value) : null;
    query.set(key, date && !Number.isNaN(date.getTime()) ? date.toISOString() : value.trim());
  }
  if (cursor) query.set("cursor", cursor);
  return query.toString();
}

export function auditDetails(details?: Record<string, string> | null) {
  const entries = Object.entries(details ?? {}).filter(([, value]) => value !== "");
  return entries.length ? entries.map(([key, value]) => `${key}: ${value}`).join(" · ") : "Recorded";
}

export type BootSample = { offset_seconds: number; cpu_percent: number; memory_used_mb: number; memory_total_mb: number };
export type Boot = {
  id: string; started_at: string; completed: boolean; status?: string; completion_reason?: string;
  finished_seconds?: number; updated_at?: string; expected?: string[];
  readiness?: Partial<Record<"management_seconds" | "pppoe_seconds" | "dns_seconds" | "internet_seconds" | "wireguard_seconds", number>>;
  events?: { offset_seconds: number; kind: string; message: string }[] | null;
  samples?: BootSample[] | null; sample_count?: number; last_sample?: BootSample;
};

export function bootStatus(boot: Boot) {
  const status = boot.status ?? boot.completion_reason;
  if (["ready", "timeout", "interrupted", "capturing"].includes(status ?? "")) return status!;
  return boot.completed ? "unknown" : "capturing";
}

export function downloadLogs(value: unknown, name: string) {
  const url = URL.createObjectURL(new Blob([JSON.stringify(value, null, 2)], { type: "application/json;charset=utf-8" }));
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = `minimalrouter-${name}-${new Date().toISOString().replace(/[:.]/g, "-")}.json`;
  document.body.append(anchor);
  anchor.click();
  anchor.remove();
  window.setTimeout(() => URL.revokeObjectURL(url), 1000);
}
