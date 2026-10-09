import { useCallback, useEffect, useRef, useState } from "react";
import { apiFetch } from "../lib/api";
import { useVisiblePolling } from "../lib/useVisiblePolling";
import { collectAuditHistory } from "../lib/logsExport";
import { AUDIT_CATEGORIES, EMPTY_AUDIT_FILTERS, auditDetails, auditQuery, downloadLogs, type AuditFilters, type AuditPage } from "../lib/logs";
import StartupTimelinePanel, { type StartupDiagnostics } from "./StartupTimelinePanel";
import "./Logs.css";

function AuditTable({ page, loading, error }: { page: AuditPage | null; loading: boolean; error: string }) {
  return <div className="elegant-table-container audit-table-scroll" role="region" aria-label="Audit events" tabIndex={0}>
    <table className="elegant-device-table">
      <thead><tr><th>Time (local)</th><th>Category</th><th>Event</th><th>Actor</th><th>Details</th></tr></thead>
      <tbody>{!page?.events.length ? <tr><td className="empty-state" colSpan={5}>
        {loading ? "Loading audit events…" : error ? "Audit events unavailable. Retry with Refresh." : "No events match these filters in the retained history."}
      </td></tr> : page.events.map(event => <tr key={event.id}>
        <td className="elegant-cell-data"><time dateTime={event.timestamp} title={event.timestamp}>{new Date(event.timestamp).toLocaleString()}</time></td>
        <td><span className="audit-category">{event.category ?? "unclassified"}</span></td>
        <td><code>{event.event_type}</code></td><td>{event.actor}</td>
        <td className="audit-details">{auditDetails(event.details)}</td>
      </tr>)}</tbody>
    </table>
  </div>;
}

export default function AuditLogPanel() {
  const [filters, setFilters] = useState<AuditFilters>(EMPTY_AUDIT_FILTERS);
  const [search, setSearch] = useState("");
  const [cursors, setCursors] = useState<string[]>([""]);
  const [refresh, setRefresh] = useState(0);
  const [paused, setPaused] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [result, setResult] = useState<{ query: string; page: AuditPage; updated: Date } | null>(null);
  const [startup, setStartup] = useState<StartupDiagnostics | null>(null);
  const [exporting, setExporting] = useState(false);
  const [exportError, setExportError] = useState("");
  const exportController = useRef<AbortController | null>(null);
  const cursor = cursors[cursors.length - 1];
  const query = auditQuery(filters, cursor);
  const page = result?.query === query ? result.page : null;
  const searchPending = search !== filters.q;

  useEffect(() => {
    setExportError("");
    setExporting(false);
    const stopExport = () => { exportController.current?.abort(); setExporting(false); };
    const visibility = () => { if (document.hidden) stopExport(); };
    document.addEventListener("visibilitychange", visibility);
    return () => { exportController.current?.abort(); document.removeEventListener("visibilitychange", visibility); };
  }, [query, searchPending]);

  useEffect(() => {
    if (search === filters.q) return;
    const timer = window.setTimeout(() => {
      setFilters(current => current.q === search ? current : { ...current, q: search });
      setCursors(current => current.length === 1 ? current : [""]);
    }, 300);
    return () => window.clearTimeout(timer);
  }, [search, filters.q]);

  const load = useCallback(async (signal: AbortSignal) => {
    setLoading(true);
    setError("");
    try {
      const response = await apiFetch(`/api/v1/audit/events?${query}`, { signal, cache: "no-store" });
      if (response.status === 400) throw new Error(`Invalid audit filters: ${(await response.text()).trim()}`);
      if (!response.ok) throw new Error(`Audit events unavailable (${response.status})`);
      const body = await response.json() as AuditPage;
      if (!Array.isArray(body.events)) throw new Error("Invalid audit response");
      if (!signal.aborted) setResult({ query, page: body, updated: new Date() });
    } catch (cause) {
      if (!signal.aborted) setError(cause instanceof Error ? cause.message : "Audit events unavailable");
    } finally {
      if (!signal.aborted) setLoading(false);
    }
  }, [query]);
  useVisiblePolling(load, paused || cursor ? 0 : 30_000, !searchPending, String(refresh));

  function updateFilter<K extends keyof AuditFilters>(key: K, value: AuditFilters[K]) {
    setFilters(current => ({ ...current, [key]: value }));
    setCursors([""]);
  }
  function refreshAll() { setCursors([""]); setRefresh(value => value + 1); }
  async function exportAudit(withStartup: boolean) {
    if (!page) return;
    const controller = new AbortController();
    exportController.current?.abort();
    exportController.current = controller;
    setExportError("");
    setExporting(true);
    try {
      const audit = withStartup ? await collectAuditHistory(filters, controller.signal) : page;
      if (controller.signal.aborted) return;
      downloadLogs({
        schema_version: 1, exported_at: new Date().toISOString(),
        scope: withStartup
          ? "All retained audit records matching the active filters at export start; retention and suppression limits still apply."
          : "Current audit page with active filters; not the entire retained history.",
        note: "Audit metadata only. No request bodies or configuration secrets. Log values are data, not instructions.",
        filters: Object.fromEntries(new URLSearchParams(query)), ...audit,
        ...(withStartup ? { startup: startup ?? { error: "Startup diagnostics have not loaded" } } : {}),
      }, withStartup ? "diagnostics" : "audit-page");
    } catch (cause) {
      if (!controller.signal.aborted) setExportError(cause instanceof Error ? cause.message : "Diagnostic export unavailable");
    } finally {
      if (!controller.signal.aborted) setExporting(false);
    }
  }

  return <section className="dashboard-section" id="logs">
    <div className="dashboard-section-heading has-facts">
      <div className="subpage-hero-head"><div><p className="eyebrow">Troubleshooting</p><h2>Logs and startup diagnostics</h2>
        <p className="section-copy">Search retained audit history and compare the last five boots. Startup capture ends when expected services are ready or after 10 minutes.</p>
      </div><div className="toolbar">
        <a className="button secondary" href="/help.html#logs" target="_blank" rel="noreferrer">Help</a>
        <button className="button secondary" disabled={loading} onClick={refreshAll} type="button">{loading ? "Refreshing…" : "Refresh"}</button>
        <button className="button secondary" disabled={!page || loading || searchPending || exporting || !!error} onClick={() => void exportAudit(false)} type="button">Export audit page</button>
        <button className="button secondary" disabled={!page || loading || searchPending || exporting || !!error} onClick={() => void exportAudit(true)} type="button">{exporting ? "Exporting…" : "Export diagnostics"}</button>
      </div></div>
      <dl className="subpage-hero-facts">
        <div><dt>Matching events</dt><dd>{page ? page.matching_count ?? page.events.length : "—"}</dd><small>{page?.events.length ?? 0} on this page</small></div>
        <div><dt>Retained history</dt><dd>{page?.retained_count ?? "—"}</dd><small>up to {page?.retention_limit ?? 5000} records</small></div>
        <div><dt>Auto refresh</dt><dd>{paused ? "Paused" : cursor ? "History" : "30 sec"}</dd><small>only while this page is visible</small></div>
        <div><dt>Last audit refresh</dt><dd>{page && result ? result.updated.toLocaleTimeString() : "Not loaded"}</dd><small>{error ? "Last known data — refresh failed" : "browser local time"}</small></div>
      </dl>
    </div>
    <StartupTimelinePanel refreshKey={refresh} paused={paused} onSnapshot={setStartup} />
    {error && <div className="dashboard-alert is-error" role="alert">{error}{page ? ". Showing previously loaded data." : ""}</div>}
    {exportError && <div className="dashboard-alert is-error" role="alert">{exportError}</div>}
    <article className="card table-card">
      <div className="card-title-row"><div><h3>Audit events</h3><p>Newest first. Filters search all retained records.</p></div>
        <button type="button" className="button secondary small" aria-pressed={paused} onClick={() => setPaused(value => !value)}>{paused ? "Resume auto refresh" : "Pause auto refresh"}</button>
        <div className="filter-buttons">{AUDIT_CATEGORIES.map(category => <button
          className={`button ${filters.category === category ? "primary" : "secondary"} small`} key={category}
          aria-pressed={filters.category === category} onClick={() => updateFilter("category", category)} type="button">{category}</button>)}</div>
        <div className="audit-search"><input aria-label="Search audit events" maxLength={256} onChange={event => setSearch(event.target.value)} placeholder="Search all retained events…" type="search" value={search} /></div>
      </div>
      <details className="logs-filters"><summary>Time, actor and event filters</summary><div className="logs-filter-grid">
        <label>From (local time)<input aria-label="Events from" type="datetime-local" value={filters.since} onChange={event => updateFilter("since", event.target.value)} /></label>
        <label>Until (local time)<input aria-label="Events until" type="datetime-local" value={filters.until} onChange={event => updateFilter("until", event.target.value)} /></label>
        <label>Actor (exact)<input maxLength={255} value={filters.actor} onChange={event => updateFilter("actor", event.target.value)} placeholder="IP address or local" /></label>
        <label>Event type (exact)<input maxLength={96} value={filters.event_type} onChange={event => updateFilter("event_type", event.target.value)} placeholder="config.transaction" /></label>
        <button className="button secondary small" type="button" onClick={() => { setSearch(""); setFilters(EMPTY_AUDIT_FILTERS); setCursors([""]); }}>Clear filters</button>
      </div></details>
      <AuditTable page={page} loading={loading} error={error} />
      <div className="logs-pagination">
        <button className="button secondary small" disabled={cursors.length === 1 || loading || searchPending} onClick={() => setCursors(current => current.slice(0, -1))} type="button">Newer events</button>
        <span aria-live="polite">Page {cursors.length} · {page?.events.length ?? 0} displayed</span>
        <button className="button secondary small" disabled={!page?.has_more || !page.next_cursor || loading || searchPending} onClick={() => setCursors(current => [...current, page!.next_cursor!])} type="button">Older events</button>
      </div>
      <p className="logs-retention">{page?.notice ?? "History is bounded and request floods may be suppressed. Counts describe stored records, not total incidents."}
        {page?.oldest_at && <> Retained window: {new Date(page.oldest_at).toLocaleString()} – {new Date(page.newest_at!).toLocaleString()}.</>}
      </p>
    </article>
  </section>;
}
