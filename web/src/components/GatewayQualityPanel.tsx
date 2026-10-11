import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import type { GatewaySettings, GatewaySummary } from "../api-types";
import { apiFetch, responseError } from "../lib/api";
import { useVisiblePolling } from "../lib/useVisiblePolling";
import { date, diagnosticAdvice, duration, finite, localTime, metric, record, summaryState, validDiagnostic, validEvents, validInsights, validPoints, validRecovery, validSummary, type Diagnostic } from "../lib/gatewayQuality";
import GatewayHistoryChart from "./GatewayHistoryChart";
import "./GatewayQualityPanel.css";

type Props = { summary: GatewaySummary | null; settings: GatewaySettings; busy: boolean; onApply: (settings: GatewaySettings) => void; onError: (message: string) => void };
type WindowName = "1h" | "24h" | "7d" | "30d";
const hours = { "1h": 1, "24h": 24, "7d": 168, "30d": 720 };

function useGatewayData<T>(path: string, validate: (value: unknown) => value is T, refresh: string) {
  const [result, setResult] = useState<{ path: string; data: T | null; error: string; at: number }>({ path: "", data: null, error: "", at: Date.now() });
  const load = useCallback(async (signal: AbortSignal) => {
    try {
      const response = await apiFetch(path, { signal });
      if (!response.ok) throw new Error("Data unavailable. Try checking again.");
      const body: unknown = await response.json();
      if (!validate(body)) throw new Error("Invalid response. Data unavailable.");
      if (!signal.aborted) setResult({ path, data: body, error: "", at: Date.now() });
    } catch (e) { if (!signal.aborted) setResult({ path, data: null, error: e instanceof Error ? e.message : "Data unavailable.", at: Date.now() }); }
  }, [path, validate]);
  useVisiblePolling(load, 30_000, true, refresh);
  return result.path === path ? result : { path, data: null, error: "", at: Date.now() };
}

export default function GatewayQualityPanel({ summary, settings, busy, onApply }: Props) {
  const [windowName, setWindowName] = useState<WindowName>("1h");
  const [refresh, setRefresh] = useState(0);
  const [measured, setMeasured] = useState<GatewaySummary | null>(null);
  const [now, setNow] = useState(Date.now());
  const [action, setAction] = useState("");
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [diagnostics, setDiagnostics] = useState<{ result: Diagnostic; at: string } | null>(null);
  const request = useRef<AbortController | null>(null);
  const settingsRef = useRef<HTMLDetailsElement>(null);
  const history = useGatewayData(`/api/v1/gateway/history?window=${windowName}`, validPoints, String(refresh));
  const insights = useGatewayData("/api/v1/gateway/insights", validInsights, String(refresh));
  const events = useGatewayData("/api/v1/gateway/events", validEvents, String(refresh));
  const recovery = useGatewayData("/api/v1/gateway/recovery", validRecovery, String(refresh));
  useEffect(() => { const timer = window.setInterval(() => setNow(Date.now()), 1000); return () => { window.clearInterval(timer); request.current?.abort(); }; }, []);
  const latest = measured && Date.parse(measured.timestamp || "") > Date.parse(summary?.timestamp || "1970-01-01") ? measured : summary;
  const status = summaryState(latest, settings.interval_seconds, now);
  const live = !["Unavailable", "Stale", "Monitoring off", "Unknown"].includes(status);
  const locked = busy || !!action;
  const coverage = insights.data;
  const advice = diagnostics ? diagnosticAdvice[diagnostics.result.cause] : undefined;
  const showSettings = () => { if (settingsRef.current) { settingsRef.current.open = true; settingsRef.current.scrollIntoView({ behavior: "smooth", block: "start" }); } };

  const run = async (kind: string, path: string) => {
    if (request.current || busy) return;
    const controller = new AbortController(); request.current = controller;
    setAction(kind); setError(""); setNotice("");
    if (kind === "diagnose") setDiagnostics(null);
    try {
      const response = await apiFetch(path, { method: "POST", signal: controller.signal });
      if (!response.ok) throw new Error(await responseError(response, "Check failed. Try again."));
      const body: unknown = await response.json();
      if (controller.signal.aborted) return;
      if (kind === "probe") {
        if (!validSummary(body) || !date(body.timestamp) || !body.available) throw new Error("No valid measurement returned.");
        setMeasured(body); setNotice("Fresh gateway measurement received.");
      } else if (kind === "diagnose") {
        if (!validDiagnostic(body)) throw new Error("Diagnostics returned an invalid response. Please try again.");
        setDiagnostics({ result: body, at: new Date().toISOString() });
      } else {
        if (!record(body) || body.success !== true) throw new Error("Service completion could not be verified. Check Logs before retrying.");
        setNotice("Service recovery completed. Check now to verify current connectivity.");
      }
    } catch (e) { if (!controller.signal.aborted) setError(e instanceof Error ? e.message : "Request failed."); }
    finally { if (!controller.signal.aborted) { request.current = null; setAction(""); setRefresh(v => v + 1); } }
  };
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault(); const form = new FormData(event.currentTarget);
    onApply({ enabled: form.get("enabled") === "on", targets: [String(form.get("target_1") || "").trim(), String(form.get("target_2") || "").trim()], interval_seconds: Number(form.get("interval_seconds")) || 30 });
  };
  const targets = Array.isArray(latest?.targets) ? latest.targets.filter(t => record(t) && typeof t.target === "string" && typeof t.reachable === "boolean") : [];

  return <section className="dashboard-section gateway-quality" id="gateway">
    <div className="dashboard-section-heading has-facts">
      <div className="subpage-hero-head"><div><p className="eyebrow">WAN health</p><h2>Gateway quality</h2><small>Connection quality, recent interruptions and recovery.</small></div><div className="gateway-hero-actions"><span className={`classic-status-chip ${status === "Healthy" ? "" : "is-warning"}`}>{status}</span><button className="button secondary" disabled={locked || !settings.enabled} onClick={() => void run("probe", "/api/v1/gateway/probe")}>{action === "probe" ? "Checking…" : "Check now"}</button></div></div>
      <p className="gateway-freshness">Last measured: {localTime(latest?.timestamp)} · {settings.enabled ? `Every ${duration(settings.interval_seconds)}` : "Monitoring disabled"}</p>
      <dl className="subpage-hero-facts six">
        <div><dt>State</dt><dd>{status}</dd><small>{live ? latest?.link.connected ? latest.link.local_ip || "PPPoE connected" : "PPPoE disconnected" : "No current link evidence"}</small></div>
        <div><dt>Latency</dt><dd>{live ? metric(latest?.latency_ms, " ms") : "—"}</dd><small>reachable targets</small></div>
        <div><dt>Jitter</dt><dd>{live ? metric(latest?.jitter_ms, " ms") : "—"}</dd><small>reply variation</small></div>
        <div><dt>Packet loss</dt><dd>{live ? metric(latest?.packet_loss_percent, "%") : "—"}</dd><small>latest sample average</small></div>
        <div><dt>PPPoE uptime</dt><dd>{live && finite(latest?.pppoe_uptime_seconds) ? duration(latest.pppoe_uptime_seconds) : "—"}</dd><small>{live && latest?.link.peer_ip ? `Peer ${latest.link.peer_ip}` : "Peer unavailable"}</small></div>
        <div><dt>Availability · 30d</dt><dd>{coverage?.available && coverage.samples > 0 ? `${coverage.uptime_percent.toFixed(2)}%` : insights.error ? "Unavailable" : coverage ? "Collecting" : "Loading…"}</dd><small>{coverage?.available ? `${duration(coverage.sampled_hours * 3600)} sampled · ${coverage.outages} outages` : "Based on observed link samples"}</small></div>
      </dl>
      {error && <p className="gateway-error" role="alert">{error}</p>}{notice && <p className="gateway-service-notice" role="status">{notice}</p>}
    </div>

    <article className="card gateway-history-card"><div className="card-title-row"><div><h3>Quality history</h3><p>{hours[windowName] >= 168 ? "Hourly rollups, grouped to fit the selected period." : "Measured samples, averaged when needed."} Local time.</p></div><div className="gateway-window-tabs" aria-label="History period">{(Object.keys(hours) as WindowName[]).map(w => <button key={w} type="button" aria-pressed={windowName === w} className={windowName === w ? "is-active" : ""} onClick={() => setWindowName(w)}>{w}</button>)}</div></div>
      {history.error ? <p className="gateway-error" role="status">History unavailable. Previous samples cleared.</p> : !history.data ? <p className="gateway-empty-copy">Loading history…</p> : <GatewayHistoryChart key={windowName} points={history.data.points} hours={hours[windowName]} now={history.at} interval={settings.interval_seconds}/>}
    </article>
    <article className="card gateway-target-results"><div className="card-title-row"><div><h3>Probe targets</h3><p>Each public target and the PPPoE peer.</p></div><button type="button" className="gateway-text-button" onClick={showSettings}>Edit targets →</button></div>
      <div className="gateway-target-list">{!live ? <p>Current target measurements unavailable.</p> : !targets.length ? <p>No target results returned.</p> : targets.map(t => <div className="gateway-target" key={t.target}><div><strong>{t.target}</strong><span className={t.reachable ? "" : "gateway-error"}>{t.reachable ? "Reachable" : "No reply"}</span></div><p>Ping {metric(t.latency_ms," ms")} · jitter {metric(t.jitter_ms," ms")} · loss {metric(t.packet_loss_percent,"%")}</p><small>{metric(t.packets_received)} / {metric(t.packets_sent)} packets{typeof t.error === "string" && t.error ? ` · ${t.error}` : ""}</small></div>)}
      {live && latest?.peer_probe && record(latest.peer_probe) && <p className="gateway-peer">Peer {latest.peer_probe.target}: {latest.peer_probe.reachable ? metric(latest.peer_probe.latency_ms," ms") : "No reply · peer ICMP may be filtered"}</p>}</div>
    </article>

    <article className="card gateway-diagnostics-card"><div className="card-title-row"><div><h3>Network diagnostics</h3><p>Fresh checks of PPPoE, public targets, DNS and HTTPS.</p></div><button className="button secondary" disabled={locked || !settings.enabled} onClick={() => void run("diagnose", "/api/v1/gateway/diagnose")} type="button">{action === "diagnose" ? "Diagnosing…" : "Diagnose connection"}</button></div>
      {diagnostics ? <div className="diag"><div className={`diag-result ${diagnostics.result.overall === "healthy" ? "is-good" : "is-bad"}`}><div><strong>{diagnostics.result.overall === "healthy" ? "Internet path is healthy" : `Likely problem: ${diagnostics.result.cause.replaceAll("_", " ")}`}</strong><small>{localTime(diagnostics.at)}</small></div></div><div className="diag-checks">{Object.entries(diagnostics.result.checks).map(([name,c]) => <article key={name} className={`diag-check ${c.ok ? "is-good" : "is-bad"}`}><span aria-hidden="true">{c.ok ? "✓" : "!"}</span><div><b>{name.toUpperCase()} · {c.ok ? "Passed" : "Failed"}</b><small>{c.detail}</small></div></article>)}</div>{advice && <p className="gateway-advice">{advice.text} {diagnostics.result.cause === "internet_reachability" ? <button className="gateway-text-button" onClick={showSettings}>{advice.label} →</button> : <a href={advice.href}>{advice.label} →</a>}</p>}</div> : <p className="gateway-empty-copy">Run a check to identify the failing step. Router settings stay unchanged.</p>}
    </article>
    <article className="card rec-card"><div className="rec-head"><div><h3>Automatic recovery</h3><p>{recovery.data?.reason || (recovery.error ? "Recovery status unavailable." : "Loading recovery status…")}</p></div><span className={`rec-chip ${recovery.data?.state === "armed" ? "is-armed" : "is-off"}`}>{recovery.data?.state || "Unknown"}</span></div>
      <p>{recovery.data?.next_attempt_at ? `Next eligible attempt: ${localTime(recovery.data.next_attempt_at)}` : "Only a verified PPPoE link outage can trigger recovery."}</p>
      <p className="gateway-freshness">Reconnects: {live ? `${metric(latest?.reconnects_1h)} / ${metric(latest?.reconnects_24h)} (1h / 24h)` : "Unavailable"}{recovery.data?.last_attempt_at && ` · Last attempt ${localTime(recovery.data.last_attempt_at)} (${recovery.data.last_result || "in progress"})`}</p>
      <details><summary>How recovery works</summary><p>After 3 minutes of fresh link-down evidence, WAN is reconnected through the existing verified service action. Attempts are at least 10 minutes apart and stop during configuration changes or recovery. A detected outage is sampled every 30 seconds or faster, even with a longer normal interval. DNS failures and packet loss do not trigger reconnection.</p></details>
    </article>

    <details className="card gateway-events"><summary>Interruptions & recovery history <span>Recent observed events</span></summary><div className="gateway-details-body">
      <h3>Link interruptions · last 7 days</h3><p>Up to 20 recent episodes in retained samples. Durations are approximate; gaps are not counted as confirmed downtime.</p>
      {events.error ? <p role="status">Event history unavailable.</p> : !events.data ? <p>Loading events…</p> : events.data.outages.length ? <ol className="gateway-event-list">{events.data.outages.map((o,i) => <li key={`${o.started_at}-${i}`}><strong>{localTime(o.started_at)} · {o.outcome === "recovered" ? "Link restored" : o.outcome === "ongoing" ? "Still down at last sample" : "Outcome unknown — measurement gap"}</strong><span>{duration(o.duration_seconds)} observed{o.partial ? " · partial start" : ""} · {o.ended_at ? `Restored ${localTime(o.ended_at)}` : `Last seen ${localTime(o.last_observed_at)}`}</span></li>)}</ol> : <p>No link interruptions in retained samples.</p>}
      <h3>WAN recovery attempts</h3>{events.data && !events.data.recovery_history_available ? <p>Recovery history unavailable.</p> : events.data?.recoveries?.length ? <ol className="gateway-event-list">{events.data.recoveries.map(e => <li key={e.id}><strong>{localTime(e.timestamp)} · {e.event_type.includes("auto_recovery") ? "Automatic" : "Manual"} · {e.event_type.endsWith("failed") ? "Failed" : "Completed"}</strong><span>{finite(Number(e.details.duration_seconds)) && e.details.duration_seconds !== undefined ? `Action duration ${duration(Number(e.details.duration_seconds))}` : "Action duration not recorded"}</span></li>)}</ol> : <p>{events.data ? "No retained WAN recovery attempts." : "Recovery history unavailable."}</p>}<a href="#logs">Open diagnostic logs →</a>
    </div></details>
    <details className="card gateway-ip-history"><summary>Public IP history <span>{coverage ? `${coverage.public_ip_changes.length} retained changes` : "Recent changes"}</span></summary><div className="gateway-details-body">{insights.error ? <p>Public-IP history unavailable.</p> : !coverage ? <p>Loading public-IP history…</p> : coverage.public_ip_changes.length ? <div className="gateway-ip-scroll" tabIndex={0} role="region" aria-label="Public IP address changes">{coverage.public_ip_changes.map(c => <div className="gateway-ip-event" key={`${c.timestamp}-${c.new_ip}`}><code>{c.old_ip}</code><span>→</span><code>{c.new_ip}</code><time dateTime={c.timestamp}>{localTime(c.timestamp)}</time></div>)}</div> : <p>No public-IP change recorded yet.</p>}</div></details>

    <details className="card gateway-settings-details" ref={settingsRef}><summary>Monitoring settings <span>{settings.enabled ? "Enabled" : "Disabled"} · {duration(settings.interval_seconds)}</span></summary><form className="settings-form gateway-settings" key={`${settings.enabled}-${settings.targets.join("-")}-${settings.interval_seconds}`} onSubmit={submit}><fieldset aria-labelledby="gateway-targets-title"><div className="fieldset-title" id="gateway-targets-title">Monitoring targets</div><label className="checkbox-row"><input defaultChecked={settings.enabled} name="enabled" type="checkbox"/><span>Enable gateway monitoring and link auto-recovery</span></label><div className="form-grid two"><label className="field"><span>Primary public IPv4</span><input defaultValue={settings.targets[0] || "1.1.1.1"} name="target_1" required/></label><label className="field"><span>Secondary public IPv4</span><input defaultValue={settings.targets[1] || "8.8.8.8"} name="target_2" required/></label><label className="field"><span>Sample interval</span><select defaultValue={String(settings.interval_seconds || 30)} name="interval_seconds">{[15,30,60,120,300].map(n => <option key={n} value={n}>{n < 60 ? `${n} seconds` : `${n/60} minute${n>60?"s":""}`}</option>)}</select></label></div><p className="form-note">Use two different public IPv4 addresses. Monitoring failures never trigger recovery while the PPPoE link is connected.</p></fieldset><div className="form-actions"><button className="button primary" disabled={locked} type="submit">Apply monitoring settings</button></div></form></details>
    <details className="card gateway-service-controls"><summary>Service recovery <span>Manual troubleshooting</span></summary><div className="gateway-details-body"><p>These actions can briefly disconnect active sessions.</p><div className="gateway-service-grid">{[["wan-reconnect","Reconnect WAN","Renegotiate the PPPoE session"],["dns-dhcp-restart","Restart DNS & DHCP","Restart dnsmasq; leases are kept"],["wireguard-restart","Restart WireGuard","Restart the configured tunnel"]].map(([id,label,note]) => <button key={id} className="gateway-action-tile" disabled={locked} onClick={() => void run(id, `/api/v1/system/actions/${id}`)} type="button"><span className="gateway-action-text"><strong>{action === id ? "Working…" : label}</strong><small>{note}</small></span></button>)}</div></div></details>
  </section>;
}
