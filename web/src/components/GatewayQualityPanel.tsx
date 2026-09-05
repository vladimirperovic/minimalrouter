import { FormEvent, useEffect, useMemo, useState } from "react";
import type { GatewayHistoryPoint, GatewaySettings, GatewaySummary } from "../api-types";
import { apiFetch } from "../lib/api";
import "./GatewayQualityPanel.css";

type WindowName = "1h" | "24h" | "7d" | "30d";

const WINDOWS: Array<{ value: WindowName; label: string; note: string }> = [
  { value: "1h", label: "1 hour", note: "live samples" },
  { value: "24h", label: "24 hours", note: "live samples" },
  { value: "7d", label: "7 days", note: "live samples" },
  { value: "30d", label: "30 days", note: "hourly averages" },
];

type Props = {
  summary: GatewaySummary | null;
  settings: GatewaySettings;
  busy: boolean;
  onApply: (settings: GatewaySettings) => void;
  onError: (message: string) => void;
};

type DiagnosticResult = {
  overall: "healthy" | "degraded" | "failed";
  cause: string;
  checks: Record<string, { ok: boolean; detail: string }>;
};

type GatewayInsights = {
  available: boolean;
  sampled_hours: number;
  samples: number;
  up_samples: number;
  uptime_percent: number;
  outages: number;
  public_ip_changes: Array<{ timestamp: string; old_ip: string; new_ip: string }>;
};

type ServiceAction = "wan-reconnect" | "dns-dhcp-restart" | "wireguard-restart";

function stateLabel(state?: GatewaySummary["state"]) {
  return state ? state.charAt(0).toUpperCase() + state.slice(1) : "Unknown";
}

function metric(value?: number, suffix = "") {
  return Number.isFinite(value) ? `${Math.round(value || 0)}${suffix}` : "—";
}

function formatInsightCoverage(insights: GatewayInsights | null) {
  if (!insights?.available || insights.sampled_hours <= 0) return { value: "Collecting", note: "30-day sampled history" };
  const outageLabel = `${insights.outages} outage${insights.outages === 1 ? "" : "s"}`;
  if (insights.sampled_hours >= 29 * 24) {
    return { value: `${insights.uptime_percent.toFixed(2)}%`, note: `30 days · ${outageLabel}` };
  }
  const days = Math.max(1, Math.floor(insights.sampled_hours / 24));
  return { value: `${insights.uptime_percent.toFixed(2)}%`, note: `${days}d coverage · ${outageLabel}` };
}

function HistoryChart({ points }: { points: GatewayHistoryPoint[] }) {
  const width = 760;
  const height = 240;
  const padding = 30;
  const plotWidth = width - padding * 2;
  const plotHeight = height - padding * 2;
  const maxLatency = Math.max(100, ...points.map((point) => point.latency_ms || 0));
  const latencyPath = useMemo(() => {
    let penDown = false;
    return points.map((point, index) => {
      if (typeof point.latency_ms !== "number") {
        penDown = false;
        return "";
      }
      const x = padding + (points.length <= 1 ? 0 : index / (points.length - 1)) * plotWidth;
      const y = padding + plotHeight - Math.min(1, point.latency_ms / maxLatency) * plotHeight;
      const command = penDown ? "L" : "M";
      penDown = true;
      return `${command}${x.toFixed(1)},${y.toFixed(1)}`;
    }).filter(Boolean).join(" ");
  }, [points, maxLatency, plotHeight, plotWidth]);
  const lossPath = useMemo(() => points.map((point, index) => {
    const x = padding + (points.length <= 1 ? 0 : index / (points.length - 1)) * plotWidth;
    const y = padding + plotHeight - Math.min(1, point.packet_loss_percent / 100) * plotHeight;
    return `${index === 0 ? "M" : "L"}${x.toFixed(1)},${y.toFixed(1)}`;
  }).join(" "), [points, plotHeight, plotWidth]);

  if (points.length === 0) {
    return <div className="gateway-chart-empty">History will appear after the first monitoring samples.</div>;
  }
  return <div className="gateway-chart-wrap">
    <svg aria-label="Latency and packet loss history" className="gateway-chart" role="img" viewBox={`0 0 ${width} ${height}`}>
      {[0, 0.25, 0.5, 0.75, 1].map((ratio) => <line className="gateway-chart-grid" key={ratio} x1={padding} x2={width - padding} y1={padding + plotHeight * ratio} y2={padding + plotHeight * ratio} />)}
      <path className="gateway-chart-latency" d={latencyPath} fill="none" />
      <path className="gateway-chart-loss" d={lossPath} fill="none" />
    </svg>
    <div className="gateway-chart-legend"><span className="is-latency">Latency, max {Math.round(maxLatency)} ms</span><span className="is-loss">Packet loss, 0–100%</span></div>
  </div>;
}

export default function GatewayQualityPanel({ summary, settings, busy, onApply, onError }: Props) {
  const [windowName, setWindowName] = useState<WindowName>("1h");
  const [points, setPoints] = useState<GatewayHistoryPoint[]>([]);
  const [insights, setInsights] = useState<GatewayInsights | null>(null);
  const [diagnosing, setDiagnosing] = useState(false);
  const [diagnostics, setDiagnostics] = useState<DiagnosticResult | null>(null);
  const [serviceAction, setServiceAction] = useState<ServiceAction | null>(null);
  const [serviceNotice, setServiceNotice] = useState("");

  useEffect(() => {
    let mounted = true;
    const controller = new AbortController();
    const loadHistory = async (attempt = 0) => {
      try {
        const response = await apiFetch(`/api/v1/gateway/history?window=${windowName}`, { signal: controller.signal });
        if (!response.ok) throw new Error(`Gateway history unavailable (${response.status})`);
        const body = (await response.json()) as { points?: GatewayHistoryPoint[] };
        if (mounted) setPoints(Array.isArray(body.points) ? body.points : []);
      } catch (error) {
        // Opening the section can abort the first mount-time request (the
        // previous section unmounts mid-flight). Retry once while mounted so
        // the chart does not stay empty until the 30s poll refires.
        if (mounted && (error as Error).name === "AbortError" && attempt < 1) {
          window.setTimeout(() => { if (mounted) void loadHistory(attempt + 1); }, 500);
          return;
        }
        if (mounted && (error as Error).name !== "AbortError") onError(error instanceof Error ? error.message : "Gateway history unavailable");
      }
    };
    void loadHistory();
    const timer = window.setInterval(loadHistory, 30000);
    return () => { mounted = false; controller.abort(); window.clearInterval(timer); };
  }, [windowName, onError]);

  useEffect(() => {
    let mounted = true;
    const controller = new AbortController();
    const loadInsights = async () => {
      try {
        const response = await apiFetch("/api/v1/gateway/insights", { signal: controller.signal });
        if (!response.ok) throw new Error(`Gateway insights unavailable (${response.status})`);
        const body = await response.json() as GatewayInsights;
        if (mounted) setInsights(body);
      } catch (error) {
        if (mounted && (error as Error).name !== "AbortError") setInsights(null);
      }
    };
    void loadInsights();
    const timer = window.setInterval(loadInsights, 30000);
    return () => { mounted = false; controller.abort(); window.clearInterval(timer); };
  }, []);

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    onApply({
      enabled: form.get("enabled") === "on",
      targets: [String(form.get("target_1") || "").trim(), String(form.get("target_2") || "").trim()],
      interval_seconds: Number(form.get("interval_seconds")) || 30,
    });
  };

  const diagnose = async () => {
    setDiagnosing(true);
    setDiagnostics(null);
    try {
      const response = await apiFetch("/api/v1/gateway/diagnose", { method: "POST" });
      const body = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(body.error || `Diagnostics failed (${response.status})`);
      setDiagnostics(body as DiagnosticResult);
    } catch (error) {
      onError(error instanceof Error ? error.message : "Network diagnostics failed");
    } finally {
      setDiagnosing(false);
    }
  };

  const runServiceAction = async (action: ServiceAction) => {
    const labels: Record<ServiceAction, string> = {
      "wan-reconnect": "WAN reconnect completed.",
      "dns-dhcp-restart": "DNS & DHCP restarted.",
      "wireguard-restart": "WireGuard restarted.",
    };
    setServiceAction(action);
    setServiceNotice("");
    try {
      const response = await apiFetch(`/api/v1/system/actions/${action}`, { method: "POST" });
      const body = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(body.error || `Service action failed (${response.status})`);
      setServiceNotice(labels[action]);
    } catch (error) {
      onError(error instanceof Error ? error.message : "Service action failed");
    } finally {
      setServiceAction(null);
    }
  };

  const availability = formatInsightCoverage(insights);

  return <section className="dashboard-section gateway-quality" id="gateway">
    <div className="dashboard-section-heading has-facts">
      <div className="subpage-hero-head"><div><p className="eyebrow">WAN observability & recovery</p><h2>Gateway quality</h2><small>Continuous health monitoring with conservative PPPoE auto-recovery after a verified 3-minute link outage.</small></div><span className={`classic-status-chip ${summary?.state === "healthy" ? "" : "is-warning"}`}>{summary?.enabled ? stateLabel(summary.state) : "Monitoring off"}</span></div>
      <dl className="subpage-hero-facts six">
        <div><dt>State</dt><dd>{summary?.enabled ? stateLabel(summary.state) : "Disabled"}</dd><small>{summary?.link?.connected ? summary.link?.local_ip || "PPPoE connected" : "PPPoE disconnected"}</small></div>
        <div><dt>Latency</dt><dd>{metric(summary?.latency_ms, " ms")}</dd><small>reachable targets</small></div>
        <div><dt>Jitter</dt><dd>{metric(summary?.jitter_ms, " ms")}</dd><small>reply variation</small></div>
        <div><dt>Packet loss</dt><dd>{metric(summary?.packet_loss_percent, "%")}</dd><small>target average</small></div>
        <div><dt>PPPoE uptime</dt><dd>{summary?.pppoe_uptime_seconds ? `${Math.floor(summary.pppoe_uptime_seconds / 3600)}h ${Math.floor((summary.pppoe_uptime_seconds % 3600) / 60)}m` : "—"}</dd><small>{summary?.link?.peer_ip ? `Peer ${summary.link.peer_ip}` : "Peer unavailable"}</small></div>
        <div><dt>Availability</dt><dd>{availability.value}</dd><small>{availability.note}</small></div>
      </dl>
    </div>

    <article className="card gateway-history-card">
      <div className="card-title-row"><div><h3>Quality history</h3><p>Bounded local SQLite history with no cloud telemetry. {windowName === "30d" ? "30-day view uses hourly averages." : "Live sample resolution."}</p></div><div className="gateway-window-tabs">{WINDOWS.map((item) => <button className={windowName === item.value ? "is-active" : ""} key={item.value} onClick={() => setWindowName(item.value)} title={item.note} type="button">{item.value}</button>)}</div></div>
      <HistoryChart points={points} />
    </article>

    <article className="card">
      <div className="card-title-row"><div><h3>Network diagnostics</h3><p>One bounded check across PPPoE, public reachability, DNS and HTTPS. No configuration is changed.</p></div><button className="button secondary" disabled={busy || diagnosing} onClick={() => void diagnose()} type="button">{diagnosing ? "Diagnosing…" : "Diagnose connection"}</button></div>
      {diagnostics && (
        <div className="diag">
          <div className={`diag-result ${diagnostics.overall === "healthy" ? "is-good" : "is-bad"}`}>
            <span className="diag-result-icon" aria-hidden="true">{diagnostics.overall === "healthy" ? "✓" : "!"}</span>
            <div><small>Result</small><strong>{diagnostics.overall === "healthy" ? "Internet path is healthy" : `Likely problem: ${diagnostics.cause.replaceAll("_", " ")}`}</strong></div>
          </div>
          <div className="diag-checks">
            {Object.entries(diagnostics.checks).map(([name, check]) => (
              <article key={name} className={`diag-check ${check.ok ? "is-good" : "is-bad"}`}>
                <span className="diag-check-icon" aria-hidden="true">{check.ok ? "✓" : "✕"}</span>
                <div><b>{name.toUpperCase()}</b><small>{check.detail}</small></div>
              </article>
            ))}
          </div>
        </div>
      )}
    </article>

    <article className="card gateway-service-controls">
      <div className="card-title-row"><div><h3>Service recovery</h3><p>Fixed, allowlisted recovery actions through router-applyd. They do not expose arbitrary service or shell execution.</p></div></div>
      <div className="gateway-service-grid">
        <button className="gateway-action-tile" disabled={busy || serviceAction !== null} onClick={() => void runServiceAction("wan-reconnect")} type="button">
          <span className="gateway-action-icon" aria-hidden="true"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round"><path d="M23 4v6h-6M1 20v-6h6" /><path d="M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15" /></svg></span>
          <span className="gateway-action-text"><strong>{serviceAction === "wan-reconnect" ? "Reconnecting…" : "Reconnect WAN"}</strong><small>Renegotiate the PPPoE session on ppp0</small></span>
        </button>
        <button className="gateway-action-tile" disabled={busy || serviceAction !== null} onClick={() => void runServiceAction("dns-dhcp-restart")} type="button">
          <span className="gateway-action-icon" aria-hidden="true"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round"><rect x="2" y="4" width="20" height="7" rx="2" /><rect x="2" y="13" width="20" height="7" rx="2" /><path d="M6 7.5h.01M6 16.5h.01" /></svg></span>
          <span className="gateway-action-text"><strong>{serviceAction === "dns-dhcp-restart" ? "Restarting…" : "Restart DNS & DHCP"}</strong><small>Reload dnsmasq; leases are kept</small></span>
        </button>
        <button className="gateway-action-tile" disabled={busy || serviceAction !== null} onClick={() => void runServiceAction("wireguard-restart")} type="button">
          <span className="gateway-action-icon" aria-hidden="true"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round"><path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z" /></svg></span>
          <span className="gateway-action-text"><strong>{serviceAction === "wireguard-restart" ? "Restarting…" : "Restart WireGuard"}</strong><small>Bring wg0 down and re-key the tunnel</small></span>
        </button>
      </div>
      {serviceNotice && <p className="gateway-service-notice" role="status">{serviceNotice}</p>}
    </article>

    <article className="card gateway-ip-history">
      <div className="card-title-row"><div><h3>Public IP history</h3><p>Only address changes are retained locally; no browsing destinations or traffic metadata are recorded.</p></div>{insights?.public_ip_changes?.length ? <span className="quiet-meta">{insights.public_ip_changes.length} change{insights.public_ip_changes.length === 1 ? "" : "s"}</span> : null}</div>
      {insights?.public_ip_changes?.length ? (
        <div className="gateway-ip-scroll">
          {insights.public_ip_changes.map((change) => <div className="gateway-ip-event" key={`${change.timestamp}-${change.new_ip}`}><code className="is-old">{change.old_ip}</code><span aria-hidden="true">→</span><code className="is-new">{change.new_ip}</code><time dateTime={change.timestamp}>{new Date(change.timestamp).toLocaleString([], { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" })}</time></div>)}
        </div>
      ) : <p className="gateway-empty-copy">No public-IP change recorded yet.</p>}
    </article>

    <article className="card rec-card">
      <div className="rec-head">
        <div><h3>Automatic recovery</h3><p>Conservative PPPoE auto-recovery after a verified 3-minute link outage — it never reacts to packet loss, DNS failure or one unreachable website.</p></div>
        <span className={`rec-chip ${settings.enabled ? "is-armed" : "is-off"}`}><i aria-hidden="true" />{settings.enabled ? "Armed" : "Paused"}</span>
      </div>
      <p className="rec-note">Recovery re-applies the canonical last-known-good configuration through the existing verified privilege boundary. Attempts are rate-limited to once every 10 minutes and are suspended while a configuration change or recovery is already in progress. Current reconnect counters: {summary?.reconnects_1h || 0} / {summary?.reconnects_24h || 0} (1h / 24h).</p>
    </article>

    <form className="settings-form gateway-settings" key={`${settings.enabled}-${settings.targets.join("-")}-${settings.interval_seconds}`} onSubmit={submit}>
      <fieldset aria-labelledby="gateway-targets-title"><div className="fieldset-title" id="gateway-targets-title">Monitoring targets</div><label className="checkbox-row"><input defaultChecked={settings.enabled} name="enabled" type="checkbox" /><span>Enable gateway monitoring and link auto-recovery</span></label><div className="form-grid two"><label className="field"><span>Primary public IPv4</span><input defaultValue={settings.targets[0] || "1.1.1.1"} inputMode="decimal" name="target_1" required /></label><label className="field"><span>Secondary public IPv4</span><input defaultValue={settings.targets[1] || "8.8.8.8"} inputMode="decimal" name="target_2" required /></label><label className="field"><span>Sample interval</span><select defaultValue={String(settings.interval_seconds || 30)} name="interval_seconds"><option value="15">15 seconds</option><option value="30">30 seconds</option><option value="60">60 seconds</option><option value="120">2 minutes</option><option value="300">5 minutes</option></select></label></div><p className="form-note">Targets must be two different public IPv4 addresses. Target failures are diagnostic only; automatic recovery is triggered solely by a sustained PPPoE link-down state.</p></fieldset>
      <div className="form-actions"><button className="button primary" disabled={busy} type="submit">Apply monitoring settings</button></div>
    </form>
  </section>;
}
