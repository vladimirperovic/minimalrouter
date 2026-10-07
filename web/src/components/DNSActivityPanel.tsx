import { useCallback, useMemo, useState } from "react";
import { apiFetch, responseError } from "../lib/api";
import { useVisiblePolling } from "../lib/useVisiblePolling";
import { HistoryBars } from "./TrafficInsightsPanel";
import type { DNSActivity, DNSRecentLookups } from "../api-types";
import "./DNSActivityPanel.css";

// DNS activity is opt-in browsing statistics. routerd stores daily lookup
// counts per device and site; full hostnames exist only in the in-memory
// recent list. Categories come from the device-profile service lists, so the
// same "adult" list that profiles can block is what is highlighted here.

type Props = {
  busy: boolean;
};

type Settings = { available: boolean; enabled: boolean; retention_days: number };

const periods = [["today", "Today"], ["yesterday", "Yesterday"], ["7d", "Last 7 days"], ["30d", "Last 30 days"]] as const;
const retentionChoices = [7, 14, 30, 60, 90];
const categoryLabels: Record<string, string> = {
  adult: "Adult", youtube: "YouTube", tiktok: "TikTok", instagram: "Instagram", facebook: "Facebook",
  roblox: "Roblox", epic: "Epic Games", twitch: "Twitch", steam: "Steam", wiki: "Wikipedia",
};

function categoryLabel(category?: string) {
  return category ? categoryLabels[category] ?? category : "";
}

function count(value: number) {
  return value.toLocaleString();
}

function seen(epoch: number) {
  return epoch > 0 ? new Date(epoch * 1000).toLocaleString([], { day: "numeric", month: "short", hour: "2-digit", minute: "2-digit" }) : "—";
}

export default function DNSActivityPanel({ busy }: Props) {
  const [settings, setSettings] = useState<Settings | null>(null);
  const [saving, setSaving] = useState(false);
  const enabled = Boolean(settings?.enabled);
  const retention = settings?.retention_days || 30;
  const [period, setPeriod] = useState("today");
  const [device, setDevice] = useState("");
  const [search, setSearch] = useState("");
  const [data, setData] = useState<DNSActivity | null>(null);
  const [recent, setRecent] = useState<DNSRecentLookups | null>(null);
  const [error, setError] = useState("");
  const [clearing, setClearing] = useState(false);
  const [notice, setNotice] = useState("");

  const query = useMemo(() => {
    const params = new URLSearchParams({ period });
    if (device) params.set("device", device);
    if (search) params.set("q", search);
    return params.toString();
  }, [period, device, search]);

  const load = useCallback(async (signal: AbortSignal) => {
    try {
      const recentParams = new URLSearchParams({ limit: "100" });
      if (device) recentParams.set("device", device);
      const [summary, latest] = await Promise.all([
        apiFetch(`/api/v1/dns-activity?${query}`, { signal }),
        apiFetch(`/api/v1/dns-activity/recent?${recentParams}`, { signal }),
      ]);
      if (!summary.ok) throw new Error(await responseError(summary, "DNS activity is unavailable"));
      const body = await summary.json() as DNSActivity;
      if (!Array.isArray(body.sites) || !Array.isArray(body.points)) throw new Error("DNS activity is not available on this firmware");
      if (signal.aborted) return;
      setData(body);
      setRecent(latest.ok ? await latest.json() as DNSRecentLookups : null);
      setError("");
    } catch (e) {
      if (signal.aborted) return;
      setData(null);
      setRecent(null);
      setError(e instanceof Error ? e.message : "DNS activity is unavailable");
    }
  }, [query, device]);
  useVisiblePolling(load, 60000, enabled, query);

  const loadSettings = useCallback(async (signal: AbortSignal) => {
    try {
      const response = await apiFetch("/api/v1/dns-activity/settings", { signal });
      if (!response.ok) throw new Error(await responseError(response, "DNS activity settings are unavailable"));
      const body = await response.json() as Settings;
      if (!signal.aborted) setSettings(body);
    } catch (e) {
      if (!signal.aborted) setError(e instanceof Error ? e.message : "DNS activity settings are unavailable");
    }
  }, []);
  useVisiblePolling(loadSettings, 60000);

  const saveSettings = async (next: Settings, success: string) => {
    setSaving(true);
    try {
      const response = await apiFetch("/api/v1/dns-activity/settings", { method: "PUT", body: JSON.stringify({ enabled: next.enabled, retention_days: next.retention_days }) });
      if (!response.ok) throw new Error(await responseError(response, "DNS activity settings could not be saved"));
      setSettings(await response.json() as Settings);
      setData(null);
      setRecent(null);
      setError("");
      setNotice(success);
    } catch (e) {
      setNotice("");
      setError(e instanceof Error ? e.message : "DNS activity settings could not be saved");
    } finally {
      setSaving(false);
    }
  };

  const toggle = (next: boolean) => {
    if (!next && !window.confirm("Stop recording DNS activity? All recorded domain history is deleted.")) return;
    void saveSettings({ available: true, enabled: next, retention_days: retention }, next ? "DNS activity recording enabled. The first lookups appear within a minute." : "DNS activity recording disabled; history is being deleted.");
  };

  const setRetention = (days: number) => {
    if (days < retention && !window.confirm(`Keep only ${days} days? Older domain history is deleted within the hour.`)) return;
    void saveSettings({ available: true, enabled, retention_days: days }, `DNS activity is kept for ${days} days.`);
  };

  const clearHistory = async () => {
    if (!window.confirm("Delete all recorded DNS activity now? Recording continues if it is enabled.")) return;
    setClearing(true);
    try {
      const response = await apiFetch("/api/v1/dns-activity/clear", { method: "POST" });
      if (!response.ok) throw new Error(await responseError(response, "DNS activity could not be deleted"));
      setData(null);
      setRecent(null);
      setError("");
    } catch (e) {
      setError(e instanceof Error ? e.message : "DNS activity could not be deleted");
    } finally {
      setClearing(false);
    }
  };

  const current = enabled && data?.enabled && data.period === period ? data : null;
  const observed = current?.points.some((point) => point.observed) ?? false;
  const labels = current?.points.map((point) => new Date(point.start).toLocaleString([], period === "today" || period === "yesterday"
    ? { hour: "2-digit", minute: "2-digit", timeZone: "UTC", hour12: false }
    : { day: "numeric", month: "short", timeZone: "UTC" })) ?? [];
  const flaggedByCategory = useMemo(() => {
    const groups = new Map<string, DNSActivity["flagged"]>();
    for (const item of current?.flagged ?? []) {
      const key = item.category || "other";
      groups.set(key, [...(groups.get(key) ?? []), item]);
    }
    return [...groups.entries()].sort(([a], [b]) => (a === "adult" ? -1 : b === "adult" ? 1 : a.localeCompare(b)));
  }, [current]);
  const topDevices = current?.devices.slice(0, 8) ?? [];
  const deviceTotal = Math.max(1, ...topDevices.map((item) => item.lookups));
  const deviceOptions = data?.devices ?? [];

  return (
    <section className="dashboard-section" id="dns-activity">
      <div className="dashboard-section-heading has-facts">
        <div className="subpage-hero-head">
          <div>
            <p className="eyebrow">Privacy-sensitive</p>
            <h2>DNS activity</h2>
            <p className="section-copy">See which sites each device looks up. Daily counts per device and site are kept on the router only; individual lookups stay in memory and are never written to disk.</p>
          </div>
          <span className={`classic-status-chip ${enabled ? "" : "is-off"}`}>Recording {enabled ? "On" : "Off"}</span>
        </div>
        <dl className="subpage-hero-facts">
          <div><dt>Collection</dt><dd>{enabled ? error ? "Unavailable" : "Active" : "Disabled"}</dd><small>dnsmasq query log in RAM</small></div>
          <div><dt>Lookups</dt><dd>{current ? count(current.total_lookups) : "—"}</dd><small>{periods.find(([value]) => value === period)?.[1].toLowerCase()}</small></div>
          <div><dt>Sites</dt><dd>{current ? count(current.site_count) : "—"}</dd><small>registrable domains</small></div>
          <div><dt>Retention</dt><dd>{retention} days</dd><small>written every 5 minutes</small></div>
        </dl>
      </div>

      <article className="service-inline-control">
        <div>
          <strong>Record domain lookups</strong>
          <p>Stores which sites each device looks up. Turning it off deletes the recorded history.</p>
        </div>
        <label className="checkbox-row"><input checked={enabled} disabled={busy || saving || !settings?.available} onChange={(event) => toggle(event.target.checked)} type="checkbox" /><span>Record DNS activity</span></label>
      </article>

      {notice && <div className="dashboard-callout" role="status"><p>{notice}</p></div>}
      {settings && !settings.available && (
        <div className="dashboard-callout"><strong>DNS activity is unavailable.</strong><p>The local statistics store could not be opened, so nothing is recorded.</p></div>
      )}
      {!enabled && error && <div className="dashboard-callout" role="alert"><p>{error}</p></div>}

      {enabled && (
        <article className="service-inline-control">
          <div>
            <strong>History</strong>
            <p>Older days are deleted automatically. Lookups from encrypted DNS in the browser, VPNs or mobile data never reach the router.</p>
          </div>
          <div className="dns-activity-actions">
            <label className="insights-period"><span className="sr-only">Retention</span>
              <select disabled={busy || saving} value={retention} onChange={(event) => setRetention(Number(event.target.value))}>
                {retentionChoices.map((days) => <option key={days} value={days}>Keep {days} days</option>)}
              </select>
            </label>
            <button className="button secondary small" disabled={busy || clearing} onClick={() => void clearHistory()} type="button">{clearing ? "Deleting…" : "Delete history"}</button>
          </div>
        </article>
      )}

      {enabled && (
        <div className="traffic-insights">
          <article className="insights-card insights-primary">
            <header className="insights-heading">
              <div><p className="insights-eyebrow">LOOKUPS OVER TIME</p><h3>{observed && current ? `${count(current.total_lookups)} lookups` : "DNS lookups, over time."}</h3></div>
              <div className="dns-activity-filters">
                <label className="insights-period"><span className="sr-only">Device</span>
                  <select value={device} onChange={(event) => { setDevice(event.target.value); setData(null); }}>
                    <option value="">All devices</option>
                    {device && !deviceOptions.some((item) => item.address === device) && <option value={device}>{device}</option>}
                    {deviceOptions.map((item) => <option key={item.address} value={item.address}>{item.hostname ? `${item.hostname} (${item.address})` : item.address}</option>)}
                  </select>
                </label>
                <label className="insights-period"><span className="sr-only">Period</span>
                  <select value={period} onChange={(event) => { setPeriod(event.target.value); setData(null); }}>
                    {periods.map(([value, label]) => <option key={value} value={value}>{label}</option>)}
                  </select>
                </label>
              </div>
            </header>
            {error ? <p className="insights-empty" role="alert">{error}.</p>
              : !current ? <p className="insights-empty">Loading DNS activity…</p>
              : !observed ? <p className="insights-empty">No lookups recorded for this period yet. The first counts appear within a few minutes of enabling recording.</p>
              : null}
            {current && <HistoryBars format={(value) => `${count(value)} lookups`} labels={labels} points={current.points.map((point) => ({ value: point.lookups, observed: point.observed }))} />}
            <p className="insights-note">UTC · lookups counted when collected (every minute) · sites grouped by registrable domain{current?.unitemized_lookups ? ` · ${count(current.unitemized_lookups)} lookups over the storage bound are counted without a site` : ""}{current?.history_started_at ? ` · recording since ${new Date(current.history_started_at).toLocaleString()}` : ""}.</p>
          </article>

          {current && (
            <article className="insights-card dns-activity-flagged">
              <header className="insights-heading"><div><h3>Categorized sites</h3><p>Sites from the device profile service lists, newest first.</p></div></header>
              {flaggedByCategory.length === 0 ? <p className="insights-empty">No categorized sites were looked up in this period.</p> : flaggedByCategory.map(([category, items]) => (
                <div className={`dns-activity-category ${category === "adult" ? "is-sensitive" : ""}`} key={category}>
                  <h4>{categoryLabel(category)} <small>{count(items.reduce((sum, item) => sum + item.lookups, 0))} lookups</small></h4>
                  <ul>
                    {items.map((item) => (
                      <li key={`${item.address}-${item.site}`}>
                        <span>{item.hostname || item.address}<small>{item.hostname ? item.address : ""}</small></span>
                        <code>{item.site}</code>
                        <strong>{count(item.lookups)}</strong>
                        <small>last {seen(item.last_seen)}</small>
                      </li>
                    ))}
                  </ul>
                </div>
              ))}
            </article>
          )}

          {current && (
            <div className="insights-two-columns">
              <article className="insights-card">
                <header className="insights-heading"><div><h3>Most active devices</h3><p>Lookups per device.</p></div></header>
                <div className="insights-ranking">
                  {topDevices.map((item, index) => (
                    <div key={item.address}>
                      <div><span>{item.hostname || item.address}<small>{item.hostname ? item.address : ""} · {count(item.sites)} sites</small></span><strong>{count(item.lookups)}</strong></div>
                      <progress aria-label={`${item.hostname || item.address} lookups`} className={`insight-color-${index + 1}`} max={deviceTotal} value={item.lookups} />
                    </div>
                  ))}
                  {topDevices.length === 0 && <p className="insights-empty">No device lookups in this period.</p>}
                </div>
              </article>
              <article className="insights-card">
                <header className="insights-heading"><div><h3>Recent lookups</h3><p>Full hostnames, kept in memory only.</p></div></header>
                <ul className="dns-activity-recent">
                  {(recent?.entries ?? []).slice(0, 25).map((entry, index) => (
                    <li className={entry.category === "adult" ? "is-sensitive" : ""} key={`${entry.at}-${entry.address}-${entry.name}-${index}`}>
                      <time dateTime={entry.at}>{new Date(entry.at).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}</time>
                      <span>{entry.hostname || entry.address}</span>
                      <code title={entry.name}>{entry.name}</code>
                      {entry.count > 1 && <small>×{entry.count}</small>}
                    </li>
                  ))}
                  {(recent?.entries.length ?? 0) === 0 && <li className="insights-empty">No lookups since routerd started.</li>}
                </ul>
              </article>
            </div>
          )}

          {current && (
            <article className="card table-card">
              <div className="card-title-row">
                <div><h3>Sites</h3><p>{count(current.site_count)} sites{device ? ` for ${current.devices[0]?.hostname || device}` : ""}, busiest first (top {current.sites.length}).</p></div>
                <input aria-label="Filter sites" className="dns-activity-search" maxLength={64} onChange={(event) => setSearch(event.target.value.toLowerCase().replace(/[^a-z0-9._-]/g, ""))} placeholder="Filter sites" type="search" value={search} />
              </div>
              <div className="elegant-table-container traffic-table-scroll">
                <table className="elegant-device-table">
                  <thead><tr><th>Site</th><th>Category</th><th>Lookups</th><th>Devices</th><th>Last seen</th></tr></thead>
                  <tbody>
                    {current.sites.map((site) => (
                      <tr className={site.category === "adult" ? "is-sensitive" : ""} key={site.site}>
                        <td className="elegant-cell-name"><code>{site.site}</code></td>
                        <td>{categoryLabel(site.category) || "—"}</td>
                        <td className="elegant-cell-data"><strong>{count(site.lookups)}</strong></td>
                        <td className="elegant-cell-data">{site.devices}</td>
                        <td className="elegant-cell-data">{seen(site.last_seen)}</td>
                      </tr>
                    ))}
                    {current.sites.length === 0 && <tr><td colSpan={5}>{search ? "No sites match the filter." : "No sites recorded for this period."}</td></tr>}
                  </tbody>
                </table>
              </div>
            </article>
          )}
        </div>
      )}
    </section>
  );
}
