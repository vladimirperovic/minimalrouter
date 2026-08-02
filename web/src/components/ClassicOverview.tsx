import { useEffect, useMemo, useState, useRef } from "react";
import type { GatewayHistoryPoint, GatewaySummary, RouterConfig, SystemStatus } from "../api-types";
import { apiFetch } from "../lib/api";

type Runtime = NonNullable<SystemStatus["runtime"]>;

type Props = {
  config: RouterConfig;
  system: SystemStatus;
  runtime: Runtime;
  gatewaySummary: GatewaySummary | null;
  memoryPercent: number;
  diskPercent: number;
  lastRefresh: Date | null;
};

function formatBytes(value = 0) {
  const units = ["B", "KB", "MB", "GB", "TB"];
  let amount = Math.max(0, value);
  let unit = 0;
  while (amount >= 1024 && unit < units.length - 1) {
    amount /= 1024;
    unit += 1;
  }
  return `${amount.toFixed(unit < 2 ? 0 : 1)} ${units[unit]}`;
}

function formatUptime(seconds = 0) {
  if (seconds <= 0) return "Unavailable";
  const days = Math.floor(seconds / 86400);
  const hours = Math.floor((seconds % 86400) / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  return days > 0 ? `${days}d ${hours}h ${minutes}m` : `${hours}h ${minutes}m`;
}

function metric(value: number | undefined, suffix: string, digits = 1) {
  return typeof value === "number" && Number.isFinite(value) ? `${value.toFixed(digits)}${suffix}` : "—";
}

function pathFor(points: number[], width: number, height: number) {
  if (points.length === 0) return "";
  const max = Math.max(...points, 1);
  const min = Math.min(...points, 0);
  const span = Math.max(1, max - min);
  return points.map((value, index) => {
    const x = points.length === 1 ? width / 2 : (index / (points.length - 1)) * width;
    const y = height - ((value - min) / span) * height;
    return `${index === 0 ? "M" : "L"}${x.toFixed(1)},${y.toFixed(1)}`;
  }).join(" ");
}

export default function ClassicOverview({
  config,
  system,
  runtime,
  gatewaySummary,
  memoryPercent,
  diskPercent,
  lastRefresh,
}: Props) {
  const [history, setHistory] = useState<GatewayHistoryPoint[]>([]);
  const [bandwidthHistory, setBandwidthHistory] = useState<{rx: number, tx: number}[]>([]);
  const lastBytesRef = useRef<{rx: number, tx: number, time: number} | null>(null);

  useEffect(() => {
    let active = true;
    const interval = setInterval(() => {
      void apiFetch("/api/v1/system")
        .then(res => res.ok ? res.json() : Promise.reject())
        .then((data: SystemStatus) => {
          if (!active || !data.runtime) return;
          const now = Date.now();
          const rx = data.runtime.rx_bytes || 0;
          const tx = data.runtime.tx_bytes || 0;
          if (lastBytesRef.current) {
            const dt = (now - lastBytesRef.current.time) / 1000;
            const rxRate = Math.max(0, (rx - lastBytesRef.current.rx) * 8 / (1024 * 1024 * dt)); // Mbps
            const txRate = Math.max(0, (tx - lastBytesRef.current.tx) * 8 / (1024 * 1024 * dt)); // Mbps
            setBandwidthHistory(prev => {
              const next = [...prev, { rx: rxRate, tx: txRate }];
              return next.length > 30 ? next.slice(next.length - 30) : next;
            });
          }
          lastBytesRef.current = { rx, tx, time: now };
        })
        .catch(() => undefined);
    }, 2000);
    return () => {
      active = false;
      clearInterval(interval);
    };
  }, []);

  const rxPath = useMemo(() => pathFor(bandwidthHistory.map((point) => point.rx), 1000, 130), [bandwidthHistory]);
  const txPath = useMemo(() => pathFor(bandwidthHistory.map((point) => point.tx), 1000, 130), [bandwidthHistory]);

  useEffect(() => {
    let active = true;
    const controller = new AbortController();
    void apiFetch("/api/v1/gateway/history?window=1h", { signal: controller.signal })
      .then(async (response) => {
        if (!response.ok) return;
        const body = await response.json() as { points?: GatewayHistoryPoint[] };
        if (active && Array.isArray(body.points)) setHistory(body.points);
      })
      .catch(() => undefined);
    return () => {
      active = false;
      controller.abort();
    };
  }, [lastRefresh]);

  const latencyPath = useMemo(() => pathFor(history.map((point) => point.latency_ms ?? 0), 1000, 130), [history]);
  const lossPath = useMemo(() => pathFor(history.map((point) => point.packet_loss_percent ?? 0), 1000, 130), [history]);

  const connected = useMemo(() => {
    return gatewaySummary && gatewaySummary.packet_loss_percent < 50;
  }, [gatewaySummary]);

  return <section className="classic-dashboard-overview" aria-label="System Overview">
    <article className="classic-hero-card">
      <div className="classic-hero-heading">
        <div>
          <div className="classic-kicker">{typeof runtime.cpu_load_percent === "number" ? `${(100 - runtime.cpu_load_percent).toFixed(0)}% idle · load ${(runtime.load_average || []).map((v) => v.toFixed(2)).join("/")}` : "Local appliance"}</div>
          <h1>{connected ? "Online and verified" : "Offline"}</h1>
        </div>
        {gatewaySummary ? (
          <span className={`classic-state-pill ${connected ? "is-good" : "is-bad"}`}>
            <span className="classic-dot" />{connected ? "PPPoE connected" : "PPPoE disconnected"}
          </span>
        ) : (
          <span className="classic-state-pill"><span className="classic-dot" />Checking PPPoE...</span>
        )}
      </div>

      <div className="classic-meta-row">
        <span>Public IP <b>{runtime.public_ip || "Unavailable"}</b></span>
        <span>WAN MAC <b>{runtime.wan_mac || "Unknown"}</b></span>
        <span>LAN MAC <b>{runtime.lan_mac || "Unknown"}</b></span>
        <span>Uptime <b>{formatUptime(runtime.uptime_seconds)}</b></span>
        <span>MTU <b>{config.wan.mtu || 1492}</b></span>
        <span>Revision <b>{config.revision}</b></span>
        <span className={system.update_trust_configured ? "is-positive" : "is-warning"}>{system.update_trust_configured ? "Signed updates enabled" : "Signed updates disabled"}</span>
      </div>
      <div className="classic-chips-row">
        <span className="classic-chip" title="Storage">Storage {runtime.storage ? `${runtime.storage.usage_percent.toFixed(1)}% used (${formatBytes(runtime.storage.used_bytes)} of ${formatBytes(runtime.storage.total_bytes)})` : "Unknown"}</span>
        <span className="classic-chip" title="Conntrack">Conntrack {runtime.conntrack_count ?? 0} / {runtime.conntrack_max ?? 0}</span>
        <span className="classic-chip" title="Time sync">Time Synchronized: {new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}, {new Date().toLocaleDateString('en-GB', { day: 'numeric', month: 'long', year: 'numeric' })}</span>
      </div>

      <div className="classic-live-grid" style={{ marginBottom: "20px" }}>
        <article className="classic-live-card"><span>Gateway latency</span><strong>{metric(gatewaySummary?.latency_ms, " ms")}</strong><small>Read-only WAN quality monitor</small></article>
        <article className="classic-live-card"><span>Packet loss</span><strong>{metric(gatewaySummary?.packet_loss_percent, "%")}</strong><small>Across configured probe targets</small></article>
        <article className="classic-live-card"><span>PPPoE uptime</span><strong>{formatUptime(gatewaySummary?.pppoe_uptime_seconds || 0)}</strong><small>{gatewaySummary?.reconnects_24h ?? 0} reconnects / 24h</small></article>
      </div>

      <div className="classic-resource-grid" style={{ marginBottom: "20px" }}>
        <article>
          <span>CPU</span>
          <div style={{ display: "flex", justifyContent: "space-between", alignItems: "baseline", width: "100%" }}>
            <strong>{(runtime.cpu_load_percent || 0).toFixed(2)}%</strong>
            {typeof runtime.temperature_c === "number" && (
              <span style={{ fontSize: "1.4rem", fontWeight: 700, color: "var(--classic-text)" }}>{runtime.temperature_c.toFixed(1)}°C</span>
            )}
          </div>
          <small>{runtime.cpu_count || 0} logical cores</small>
          <progress max="100" value={Math.min(100, runtime.cpu_load_percent || 0)} />
        </article>
        <article><span>Memory</span><strong>{formatBytes(runtime.memory_used_bytes)}</strong><small>{formatBytes(runtime.memory_used_bytes)} of {formatBytes(runtime.memory_total_bytes)}</small><progress max="100" value={Math.min(100, memoryPercent)} /></article>
        <article><span>Disk</span><strong>{formatBytes(runtime.disk_used_bytes)}</strong><small>{formatBytes(runtime.disk_used_bytes)} of {formatBytes(runtime.disk_total_bytes)}</small><progress max="100" value={Math.min(100, diskPercent)} /></article>
      </div>

      <div className="classic-charts-row" style={{ display: "flex", gap: "20px" }}>
        <div className="classic-chart-block" style={{ flex: 1, minWidth: 0 }}>
          <div className="classic-chart-title"><div><strong>Live Bandwidth</strong><small>WAN interface · last 60 seconds</small></div><div className="classic-chart-legend"><span><i style={{ background: "var(--classic-purple)" }} />Download</span><span><i style={{ background: "var(--classic-green)" }} />Upload</span></div></div>
          <div className="classic-chart-frame">
            <svg viewBox="0 0 1000 150" preserveAspectRatio="none" role="img" aria-label="Live bandwidth history"><line x1="0" y1="20" x2="1000" y2="20" /><line x1="0" y1="65" x2="1000" y2="65" /><line x1="0" y1="110" x2="1000" y2="110" />{rxPath && <path fill="none" stroke="var(--classic-purple)" strokeWidth="2" strokeLinejoin="round" d={rxPath} />}{txPath && <path fill="none" stroke="var(--classic-green)" strokeWidth="2" strokeLinejoin="round" d={txPath} />}</svg>
            {bandwidthHistory.length === 0 && <span className="classic-chart-empty">Collecting metrics…</span>}
          </div>
          <div className="classic-chart-axis">
            <span style={{ color: "var(--classic-purple)", fontWeight: 600 }}>{bandwidthHistory.length > 0 ? bandwidthHistory[bandwidthHistory.length - 1].rx.toFixed(1) + " Mbps ↓" : "↓"}</span>
            <span style={{ color: "var(--classic-green)", fontWeight: 600 }}>{bandwidthHistory.length > 0 ? bandwidthHistory[bandwidthHistory.length - 1].tx.toFixed(1) + " Mbps ↑" : "↑"}</span>
          </div>
        </div>
        
        <div className="classic-chart-block" style={{ flex: 1, minWidth: 0 }}>
          <div className="classic-chart-title"><div><strong>Gateway quality</strong><small>Live samples · last hour</small></div><div className="classic-chart-legend"><span><i className="is-latency" />Latency</span><span><i className="is-loss" />Packet loss</span></div></div>
          <div className="classic-chart-frame">
            <svg viewBox="0 0 1000 150" preserveAspectRatio="none" role="img" aria-label="Gateway latency and packet-loss history"><line x1="0" y1="20" x2="1000" y2="20" /><line x1="0" y1="65" x2="1000" y2="65" /><line x1="0" y1="110" x2="1000" y2="110" />{latencyPath && <path className="classic-chart-line is-latency" d={latencyPath} />}{lossPath && <path className="classic-chart-line is-loss" d={lossPath} />}</svg>
            {history.length === 0 && <span className="classic-chart-empty">Waiting for gateway history…</span>}
          </div>
          <div className="classic-chart-axis"><span>Older</span><span>Now</span></div>
        </div>
      </div>
    </article>
  </section>;
}
