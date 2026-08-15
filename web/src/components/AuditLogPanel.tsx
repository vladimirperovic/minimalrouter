import { useCallback, useEffect, useMemo, useState } from "react";
import { apiFetch } from "../lib/api";

type AuditEvent = {
  id: string;
  event_type: string;
  actor: string;
  timestamp: string;
  details: Record<string, string>;
};

type LogFilter = "all" | "security" | "configuration" | "network" | "recovery";

function category(event: AuditEvent): Exclude<LogFilter, "all"> {
  const value = `${event.event_type} ${event.details?.path ?? ""}`.toLowerCase();
  if (value.includes("auth") || value.includes("session") || value.includes("login") || value.includes("totp")) return "security";
  if (value.includes("backup") || value.includes("restore") || value.includes("snapshot") || value.includes("update") || value.includes("factory")) return "recovery";
  if (value.includes("wireguard") || value.includes("firewall") || value.includes("dhcp") || value.includes("dns") || value.includes("wifi") || value.includes("squid") || value.includes("qos")) return "network";
  return "configuration";
}

function details(value: Record<string, string> = {}) {
  const entries = Object.entries(value).filter(([, item]) => item !== "").slice(0, 8);
  return entries.length === 0 ? "Recorded" : entries.map(([key, item]) => `${key}: ${item}`).join(" · ");
}

export default function AuditLogPanel() {
  const [events, setEvents] = useState<AuditEvent[]>([]);
  const [filter, setFilter] = useState<LogFilter>("all");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [updated, setUpdated] = useState<Date | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const response = await apiFetch("/api/v1/audit/events?limit=250");
      if (!response.ok) throw new Error(`Audit events unavailable (${response.status})`);
      const body = await response.json();
      setEvents(Array.isArray(body.events) ? body.events : []);
      setUpdated(new Date());
    } catch (loadError) {
      setError(loadError instanceof Error ? loadError.message : "Audit events unavailable");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
    const timer = window.setInterval(() => void load(), 30000);
    return () => window.clearInterval(timer);
  }, [load]);

  const visible = useMemo(() => events.filter((event) => filter === "all" || category(event) === filter), [events, filter]);

  const exportJSON = () => {
    const blob = new Blob([JSON.stringify({
      exported_at: new Date().toISOString(),
      note: "Redacted Minimal Router audit metadata. Secrets and request bodies are excluded.",
      events: visible,
    }, null, 2)], { type: "application/json;charset=utf-8" });
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = `minimalrouter-audit-${new Date().toISOString().replace(/[:.]/g, "-")}.json`;
    anchor.click();
    URL.revokeObjectURL(url);
  };

  return (
    <section className="dashboard-section" id="logs">
      <div className="dashboard-section-heading has-facts">
        <div className="subpage-hero-head"><div><p className="eyebrow">Local metadata</p><h2>Security and activity log</h2><p className="section-copy">Bounded SQLite events without passwords, keys, request bodies, or generated configurations.</p></div><div className="toolbar"><button className="button secondary" disabled={loading} onClick={() => void load()} type="button">{loading ? "Refreshing…" : "Refresh"}</button><button className="button secondary" disabled={visible.length === 0} onClick={exportJSON} type="button">Export JSON</button></div></div>
        <dl className="subpage-hero-facts"><div><dt>Events loaded</dt><dd>{events.length}</dd><small>{visible.length} currently displayed</small></div><div><dt>Security</dt><dd>{events.filter((event) => category(event) === "security").length}</dd><small>authentication events</small></div><div><dt>Network</dt><dd>{events.filter((event) => category(event) === "network").length}</dd><small>service policy events</small></div><div><dt>Last refresh</dt><dd>{updated ? updated.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }) : "Not loaded"}</dd><small>stored locally</small></div></dl>
      </div>
      {error && <div className="dashboard-alert is-error" role="alert">{error}</div>}
      <article className="card table-card">
        <div className="card-title-row"><div><h3>Audit events</h3><p>{visible.length} displayed of {events.length} loaded.</p></div><div className="filter-buttons">{(["all", "security", "configuration", "network", "recovery"] as LogFilter[]).map((item) => <button className={filter === item ? "button primary small" : "button secondary small"} key={item} onClick={() => setFilter(item)} type="button">{item}</button>)}</div></div>
        <div className="table-scroll audit-table-scroll"><table><thead><tr><th>Time</th><th>Category</th><th>Event</th><th>Actor</th><th>Details</th></tr></thead><tbody>{visible.length === 0 ? <tr><td className="empty-state" colSpan={5}>{loading ? "Loading audit events…" : "No events match this filter."}</td></tr> : visible.map((event) => <tr key={event.id}><td>{new Date(event.timestamp).toLocaleString()}</td><td><span className="audit-category">{category(event)}</span></td><td><code>{event.event_type}</code></td><td>{event.actor}</td><td>{details(event.details)}</td></tr>)}</tbody></table></div>
      </article>
    </section>
  );
}
