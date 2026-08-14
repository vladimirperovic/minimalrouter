import { FormEvent, useEffect, useMemo, useState } from "react";
import type { GatewayHistoryPoint, GatewaySettings, GatewaySummary } from "../api-types";
import { apiFetch } from "../lib/api";
import "./GatewayQualityPanel.css";

// 30d is served from the hourly rollup in the gateway store: raw samples are
// pruned after 7 days, so anything longer would otherwise return a truncated
// series. Proving "the line was bad last month" needs this window.
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

function stateLabel(state?: GatewaySummary["state"]) {
  return state ? state.charAt(0).toUpperCase() + state.slice(1) : "Unknown";
}

function metric(value?: number, suffix = "") {
  return Number.isFinite(value) ? `${Math.round(value || 0)}${suffix}` : "—";
}

function HistoryChart({ points }: { points: GatewayHistoryPoint[] }) {
  const width = 760;
  const height = 240;
  const padding = 30;
  const plotWidth = width - padding * 2;
  const plotHeight = height - padding * 2;
  const maxLatency = Math.max(100, ...points.map((point) => point.latency_ms || 0));
  // Samples without a successful probe break the line instead of dropping it to
  // zero, which would render an outage as perfect latency.
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

  useEffect(() => {
    let mounted = true;
    const controller = new AbortController();
    const loadHistory = async () => {
      try {
        const response = await apiFetch(`/api/v1/gateway/history?window=${windowName}`, { signal: controller.signal });
        if (!response.ok) throw new Error(`Gateway history unavailable (${response.status})`);
        const body = (await response.json()) as { points?: GatewayHistoryPoint[] };
        if (mounted) setPoints(Array.isArray(body.points) ? body.points : []);
      } catch (error) {
        if (mounted && (error as Error).name !== "AbortError") onError(error instanceof Error ? error.message : "Gateway history unavailable");
      }
    };
    void loadHistory();
    const timer = window.setInterval(loadHistory, 30000);
    return () => { mounted = false; controller.abort(); window.clearInterval(timer); };
  }, [windowName, onError]);

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    onApply({
      enabled: form.get("enabled") === "on",
      targets: [String(form.get("target_1") || "").trim(), String(form.get("target_2") || "").trim()],
      interval_seconds: Number(form.get("interval_seconds")) || 30,
    });
  };

  return <section className="dashboard-section gateway-quality" id="gateway">
    <div className="dashboard-section-heading has-facts">
      <div className="subpage-hero-head"><div><p className="eyebrow">WAN observability</p><h2>Gateway quality</h2><small>Read-only monitoring; it never restarts PPPoE automatically.</small></div><span className={`classic-status-chip ${summary?.state === "healthy" ? "" : "is-warning"}`}>{summary?.enabled ? stateLabel(summary.state) : "Monitoring off"}</span></div>
      <dl className="subpage-hero-facts six">
        <div><dt>State</dt><dd>{summary?.enabled ? stateLabel(summary.state) : "Disabled"}</dd><small>{summary?.link?.connected ? summary.link?.local_ip || "PPPoE connected" : "PPPoE disconnected"}</small></div>
        <div><dt>Latency</dt><dd>{metric(summary?.latency_ms, " ms")}</dd><small>reachable targets</small></div>
        <div><dt>Jitter</dt><dd>{metric(summary?.jitter_ms, " ms")}</dd><small>reply variation</small></div>
        <div><dt>Packet loss</dt><dd>{metric(summary?.packet_loss_percent, "%")}</dd><small>target average</small></div>
        <div><dt>PPPoE uptime</dt><dd>{summary?.pppoe_uptime_seconds ? `${Math.floor(summary.pppoe_uptime_seconds / 3600)}h ${Math.floor((summary.pppoe_uptime_seconds % 3600) / 60)}m` : "—"}</dd><small>{summary?.link?.peer_ip ? `Peer ${summary.link.peer_ip}` : "Peer unavailable"}</small></div>
        <div><dt>Reconnects</dt><dd>{summary?.reconnects_1h || 0} / {summary?.reconnects_24h || 0}</dd><small>1h / 24h</small></div>
      </dl>
    </div>

    <article className="card gateway-history-card">
      <div className="card-title-row"><div><h3>Quality history</h3><p>Bounded local SQLite history with no cloud telemetry. {windowName === "30d" ? "30-day view uses hourly averages." : "Live sample resolution."}</p></div><div className="gateway-window-tabs">{WINDOWS.map((item) => <button className={windowName === item.value ? "is-active" : ""} key={item.value} onClick={() => setWindowName(item.value)} title={item.note} type="button">{item.value}</button>)}</div></div>
      <HistoryChart points={points} />
    </article>

    <form className="settings-form gateway-settings" key={`${settings.enabled}-${settings.targets.join("-")}-${settings.interval_seconds}`} onSubmit={submit}>
      <fieldset><legend>Monitoring targets</legend><label className="checkbox-row"><input defaultChecked={settings.enabled} name="enabled" type="checkbox" /><span>Enable gateway quality monitoring</span></label><div className="form-grid two"><label className="field"><span>Primary public IPv4</span><input defaultValue={settings.targets[0] || "1.1.1.1"} inputMode="decimal" name="target_1" required /></label><label className="field"><span>Secondary public IPv4</span><input defaultValue={settings.targets[1] || "8.8.8.8"} inputMode="decimal" name="target_2" required /></label><label className="field"><span>Sample interval</span><select defaultValue={String(settings.interval_seconds || 30)} name="interval_seconds"><option value="15">15 seconds</option><option value="30">30 seconds</option><option value="60">60 seconds</option><option value="120">2 minutes</option><option value="300">5 minutes</option></select></label></div><p className="form-note">Targets must be two different public IPv4 addresses. The PPPoE peer is checked separately and does not control the health result because some providers block ICMP to the peer.</p></fieldset>
      <div className="form-actions"><button className="button primary" disabled={busy} type="submit">Apply monitoring settings</button></div>
    </form>
  </section>;
}
