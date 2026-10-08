import { useCallback, useState } from "react";
import { apiFetch, responseError } from "../lib/api";
import { useDNSRisk, riskCoverage } from "../lib/dnsRisk";
import { useVisiblePolling } from "../lib/useVisiblePolling";
import type { DNSRiskException, DNSRiskPage } from "../api-types";
import "./DNSRiskPanel.css";

const categories = [["adult", "Adult"], ["phishing", "Phishing"], ["malware", "Malware"], ["fraud", "Fraud"], ["gambling", "Gambling"]];
const label = (category: string) => categories.find(([key]) => key === category)?.[1] ?? category;
const when = (epoch: number) => epoch ? new Date(epoch * 1000).toLocaleString() : "Not yet";

export function DNSRiskCallout({ onOpen }: { onOpen: () => void }) {
  const { summary, error } = useDNSRisk();
  const count = summary?.new_count ?? 0;
  return <aside className={`dns-risk-callout ${count ? "has-alerts" : ""}`} aria-label="DNS risk overview">
    <div><strong>{count ? `${count.toLocaleString()} new DNS risk alerts` : "DNS risk monitoring"}</strong><p>{error || (!summary ? "Checking monitoring status…" : !summary.available ? "Monitoring is unavailable; DNS risk status is unknown." : summary.enabled ? `${riskCoverage(summary)} · adult, phishing, malware, fraud and gambling lists. A lookup does not prove a visit.` : "Enable DNS activity recording to check lookups across the whole network.")}</p></div>
    <button className="button secondary small" onClick={onOpen} type="button">Open DNS alerts</button>
  </aside>;
}

export default function DNSRiskPanel({ busy }: { busy: boolean }) {
  const { summary, error: statusError, refresh, revision } = useDNSRisk();
  const [view, setView] = useState("new");
  const [category, setCategory] = useState("");
  const [offset, setOffset] = useState(0);
  const [page, setPage] = useState<DNSRiskPage | null>(null);
  const [exceptions, setExceptions] = useState<DNSRiskException[]>([]);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [working, setWorking] = useState(false);
  const enabled = Boolean(summary?.available && summary.enabled);
  const load = useCallback(async (signal: AbortSignal) => {
    try {
      const params = new URLSearchParams({ view, category, offset: String(offset), limit: "25" });
      const [alerts, excluded] = await Promise.all([
        apiFetch(`/api/v1/dns-activity/alerts?${params}`, { signal }),
        apiFetch("/api/v1/dns-activity/alerts/exceptions", { signal }),
      ]);
      if (!alerts.ok || !excluded.ok) throw new Error("DNS alerts could not be loaded. Retry or check the router connection.");
      const next = await alerts.json() as DNSRiskPage;
      const entries = await excluded.json() as { exceptions: DNSRiskException[] };
      if (!Array.isArray(next.alerts) || !Array.isArray(entries.exceptions)) throw new Error("DNS alert response is unavailable");
      if (signal.aborted) return;
      if (offset > 0 && offset >= next.total) { setOffset(Math.max(0, Math.floor((next.total - 1) / 25) * 25)); return; }
      setPage(next); setExceptions(entries.exceptions); setError("");
    } catch (e) {
      if (signal.aborted) return;
      setPage(null); setExceptions([]); setError(e instanceof Error ? e.message : "DNS alerts unavailable");
    }
  }, [view, category, offset]);
  useVisiblePolling(load, 60000, Boolean(summary?.available), `${revision}:${summary?.last_checked_at}:${enabled}`);

  const mutate = async (path: string, method: string, message: string) => {
    setWorking(true); setNotice("");
    try {
      const response = await apiFetch(`/api/v1/dns-activity/alerts/${path}`, { method });
      if (!response.ok) throw new Error(await responseError(response, "DNS alert action failed"));
      setNotice(message); setError(""); refresh();
    } catch (e) { setError(e instanceof Error ? e.message : "DNS alert action failed"); }
    finally { setWorking(false); }
  };
  const coverage = riskCoverage(summary);
  const incomplete = enabled && coverage !== "Monitoring";
  const disabled = busy || working;

  return <article className="insights-card dns-risk-panel" aria-label="DNS risk alerts">
    <header className="insights-heading">
      <div><p className="insights-eyebrow">WHOLE NETWORK</p><h3>DNS risk alerts</h3><p>Known domains in adult, phishing, malware, fraud and gambling lists. Checks work even when device addresses change.</p></div>
      <span className={`classic-status-chip ${coverage !== "Monitoring" ? "is-off" : ""}`}>{coverage}</span>
    </header>
    <p className="dns-risk-explanation">A match means the router saw a DNS request. Apps, adverts and background traffic can generate it; it does not prove someone visited the site, viewed content or that access was blocked. This monitor does not change DNS filtering.</p>
    {(statusError || error) && <div className="dashboard-callout" role="alert"><p>{statusError || error}</p><button className="button secondary small" onClick={refresh} type="button">Retry alerts</button></div>}
    {notice && <p role="status">{notice}</p>}
    {!summary && !statusError && <p className="insights-empty">Loading monitoring status…</p>}
    {summary && !summary.available && <p className="insights-empty">Risk monitoring is unavailable. No conclusion can be drawn from missing alerts.</p>}
    {summary?.available && !summary.enabled && <p className="insights-empty">Turn on “Record DNS activity” to download the public category lists and monitor new lookups.</p>}
    {incomplete && <div className="dashboard-callout" role="status"><strong>Coverage is incomplete.</strong><p>{summary?.error || summary?.collection.error || "Collection or one or more category lists are unavailable, stale or still loading."} Absence of alerts does not mean the traffic is safe.</p></div>}
    {enabled && <>
      <div className="dns-risk-facts">
        <div><strong>{summary?.new_count.toLocaleString()}</strong><span>New alerts</span></div>
        <div><strong>{summary?.total.toLocaleString()}</strong><span>Retained alerts</span></div>
        <div><strong>{summary?.sources.filter(source => source.entries > 0 && !source.stale).length} / 5</strong><span>Current category lists</span></div>
      </div>
      <div className="dns-activity-filters">
        <label>Show <select aria-label="Alert view" value={view} onChange={event => { setView(event.target.value); setOffset(0); setPage(null); }}><option value="new">New alerts</option><option value="all">All alerts</option></select></label>
        <label>Category <select aria-label="Alert category" value={category} onChange={event => { setCategory(event.target.value); setOffset(0); setPage(null); }}><option value="">All categories</option>{categories.map(([id, name]) => <option key={id} value={id}>{name}</option>)}</select></label>
        <button className="button secondary small" onClick={refresh} disabled={working} type="button">Refresh alerts</button>
      </div>
      {!page && !error && <p className="insights-empty">Loading alerts…</p>}
      {page && page.alerts.length === 0 && <p className="insights-empty">No {view === "new" ? "new " : ""}matching alerts in retained history. Only lookups observed after the category lists loaded can be checked.</p>}
      <ul className="dns-risk-alerts">
        {(page?.alerts ?? []).map(alert => <li key={alert.id} className={alert.severity === "high" ? "is-high" : ""}>
          <div className="dns-risk-alert-heading"><code>{alert.domain}</code><span className="dns-risk-category">{label(alert.category)}{alert.severity === "high" ? " · High risk" : ""}</span></div>
          <p>{alert.lookups.toLocaleString()} DNS requests · First {when(alert.first_seen)} · Last {when(alert.last_seen)}</p>
          <p>Last observed IP: <code>{alert.last_address}</code> · Source: Block List Project</p>
          <div className="dns-activity-actions">
            {alert.acknowledged_at > 0 ? <span>Reviewed</span> : <button className="button secondary small" disabled={disabled} onClick={() => void mutate(`${alert.id}/acknowledge`, "POST", "Alert marked as reviewed. A new day can create a new alert.")} type="button">Mark reviewed</button>}
            {alert.ignored ? <span>Ignored by exception</span> : <button className="button secondary small" disabled={disabled} onClick={() => { if (window.confirm(`Ignore alerts for ${alert.domain} in ${label(alert.category)}? This affects this domain/category only and does not allow or block DNS traffic.`)) void mutate(`${alert.id}/ignore`, "POST", "Exception saved. Manage it below to restore alerts."); }} type="button">Ignore domain/category</button>}
          </div>
        </li>)}
      </ul>
      {page && page.total > 0 && <div className="dns-risk-pagination"><button className="button secondary small" disabled={offset === 0 || working} onClick={() => { setOffset(Math.max(0, offset - 25)); setPage(null); }} type="button">Previous alerts</button><span>{offset + 1}–{Math.min(offset + 25, page.total)} of {page.total.toLocaleString()}</span><button className="button secondary small" disabled={offset + 25 >= page.total || working} onClick={() => { setOffset(offset + 25); setPage(null); }} type="button">Next alerts</button></div>}
      <p className="insights-note">One alert per matched domain, category and UTC day across all devices. “All alerts” includes reviewed and ignored matches. IP addresses may be reassigned and do not identify a child. Alerts follow DNS history retention, up to 10,000 records.</p>
      {(summary?.dropped_lookups ?? 0) + (summary?.collection.dropped_lookups ?? 0) > 0 && <p className="insights-note">{((summary?.dropped_lookups ?? 0) + (summary?.collection.dropped_lookups ?? 0)).toLocaleString()} lookups could not be fully checked since this router service started or history was cleared.</p>}
      {(summary?.removed_alerts ?? 0) > 0 && <p className="insights-note">{summary?.removed_alerts.toLocaleString()} oldest alerts removed at the storage limit.</p>}
    </>}
    {summary?.available && <details className="dns-risk-details"><summary>Category lists &amp; exceptions</summary>
      <p>Public lists from <a href="https://github.com/blocklistproject/Lists" target="_blank" rel="noreferrer">Block List Project</a> (Unlicense), refreshed daily while recording is on. Checks happen locally; your DNS lookups are not sent to the list provider. Updates may take several minutes.</p>
      <ul className="dns-risk-sources">{summary.sources.map(source => <li key={source.category}><strong>{source.label}</strong><span>{source.updating ? "Updating…" : source.entries === 0 ? "Not loaded" : source.stale ? "Stale" : "Current"} · {source.entries.toLocaleString()} domains</span><small>Last verified: {when(source.updated_at)}{source.error && ` · ${source.error}`}</small></li>)}</ul>
      <button className="button secondary small" disabled={disabled || !enabled || summary.sources.some(source => source.updating)} onClick={() => void mutate("refresh", "POST", "List update queued. Status refreshes automatically; retry is limited to once every five minutes.")} type="button">Update category lists</button>
      <h4>Ignored domain/category pairs</h4><p>Exceptions suppress new-alert notifications, remain visible in “All alerts”, and survive history deletion. They do not change blocking rules.</p>
      {exceptions.length === 0 ? <p>No exceptions.</p> : <ul className="dns-risk-exceptions">{exceptions.map(entry => <li key={entry.id}><span><code>{entry.domain}</code> · {label(entry.category)}</span><button className="button secondary small" disabled={disabled} onClick={() => void mutate(`exceptions/${entry.id}`, "DELETE", "Exception removed. Unreviewed retained matches can alert again.")} type="button">Remove exception</button></li>)}</ul>}
    </details>}
  </article>;
}
