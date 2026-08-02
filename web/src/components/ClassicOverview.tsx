import { useEffect, useMemo, useState } from "react";
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
  leases: NonNullable<Runtime["dhcp_leases"]>;
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
  leases,
  lastRefresh,
}: Props) {
  const [history, setHistory] = useState<GatewayHistoryPoint[]>([]);

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
  const connected = Boolean(runtime.wan_connected);
  const healthyGateway = gatewaySummary?.state === "healthy";
  const headline = connected && healthyGateway ? "Online and verified" : connected ? "Online with warnings" : "WAN offline";
  const provider = config.cloudflare.ddns_provider || "cloudflare";
  const storageLevel = runtime.storage?.level || "unknown";
  const conntrackPercent = runtime.conntrack_usage_percent ?? (runtime.conntrack_max ? ((runtime.conntrack_count || 0) / runtime.conntrack_max) * 100 : 0);

  return <section className="classic-dashboard-overview" aria-label="Router overview">
    <article className="classic-hero-card">
      <div className="classic-hero-heading">
        <div>
          <div className="classic-kicker"><span className={connected ? "classic-dot is-good" : "classic-dot is-bad"} />Internet</div>
          <h1>{headline}</h1>
        </div>
        <span className={`classic-state-pill ${connected ? "is-good" : "is-bad"}`}><span className="classic-dot" />{connected ? "PPPoE connected" : "PPPoE disconnected"}</span>
      </div>

      <div className="classic-meta-row">
        <span>Public IP <b>{runtime.public_ip || "Unavailable"}</b></span>
        <span>Uptime <b>{formatUptime(runtime.uptime_seconds)}</b></span>
        <span>MTU <b>{config.wan.mtu || 1492}</b></span>
        <span>Revision <b>{config.revision}</b></span>
        <span className={system.update_trust_configured ? "is-positive" : "is-warning"}>{system.update_trust_configured ? "Signed updates enabled" : "Signed updates disabled"}</span>
      </div>

      <div className="classic-live-grid" style={{ marginBottom: "20px" }}>
        <article className="classic-live-card"><span>Gateway latency</span><strong>{metric(gatewaySummary?.latency_ms, " ms")}</strong><small>Read-only WAN quality monitor</small></article>
        <article className="classic-live-card"><span>Packet loss</span><strong>{metric(gatewaySummary?.packet_loss_percent, "%")}</strong><small>Across configured probe targets</small></article>
        <article className="classic-live-card"><span>PPPoE uptime</span><strong>{formatUptime(gatewaySummary?.pppoe_uptime_seconds || 0)}</strong><small>{gatewaySummary?.reconnects_24h ?? 0} reconnects / 24h</small></article>
      </div>

      <div className="classic-resource-grid" style={{ marginBottom: "20px" }}>
        <article><span>CPU</span><strong>{Math.round(runtime.cpu_load_percent || 0)}%</strong><small>{runtime.cpu_count || 0} logical cores</small><progress max="100" value={Math.min(100, runtime.cpu_load_percent || 0)} /></article>
        <article><span>Memory</span><strong>{formatBytes(runtime.memory_used_bytes)}</strong><small>{formatBytes(runtime.memory_used_bytes)} of {formatBytes(runtime.memory_total_bytes)}</small><progress max="100" value={Math.min(100, memoryPercent)} /></article>
        <article><span>Disk</span><strong>{formatBytes(runtime.disk_used_bytes)}</strong><small>{formatBytes(runtime.disk_used_bytes)} of {formatBytes(runtime.disk_total_bytes)}</small><progress max="100" value={Math.min(100, diskPercent)} /></article>
      </div>

      <div className="classic-chart-block">
        <div className="classic-chart-title"><div><strong>Gateway quality</strong><small>Live samples · last hour</small></div><div className="classic-chart-legend"><span><i className="is-latency" />Latency</span><span><i className="is-loss" />Packet loss</span></div></div>
        <div className="classic-chart-frame">
          <svg viewBox="0 0 1000 150" preserveAspectRatio="none" role="img" aria-label="Gateway latency and packet-loss history"><line x1="0" y1="20" x2="1000" y2="20" /><line x1="0" y1="65" x2="1000" y2="65" /><line x1="0" y1="110" x2="1000" y2="110" />{latencyPath && <path className="classic-chart-line is-latency" d={latencyPath} />}{lossPath && <path className="classic-chart-line is-loss" d={lossPath} />}</svg>
          {history.length === 0 && <span className="classic-chart-empty">Waiting for gateway history…</span>}
        </div>
        <div className="classic-chart-axis"><span>Older</span><span>Now</span></div>
      </div>
    </article>



    <section className="classic-operations-section">
      <div className="classic-section-heading"><div><span>OPERATIONS</span><h2>Everything important, at a glance.</h2></div><small>{lastRefresh ? `Updated ${lastRefresh.toLocaleTimeString()}` : "Live state"}</small></div>
      <div className="classic-ops-grid">
        <article><span>Storage pressure</span><strong>{storageLevel}</strong><small>{runtime.storage ? `${runtime.storage.usage_percent.toFixed(1)}% used` : "Telemetry unavailable"}</small></article>
        <article><span>Conntrack</span><strong>{Math.round(conntrackPercent)}%</strong><small>{runtime.conntrack_count ?? 0} / {runtime.conntrack_max ?? 0}</small></article>
        <article><span>Time sync</span><strong>{runtime.time_synchronized ? "Synchronized" : "Not verified"}</strong><small>Kernel clock health</small></article>
        <article><span>Dynamic DNS</span><strong>{config.cloudflare.ddns_enabled ? provider === "noip" ? "No-IP" : "Cloudflare" : "Off"}</strong><small>{config.cloudflare.domain || "No hostname configured"}</small></article>
        <article><span>WireGuard</span><strong>{config.wireguard.enabled ? "On" : "Off"}</strong><small>{(config.wireguard.peers || []).filter((peer) => peer.enabled).length} enabled peers</small></article>
        <article><span>DHCP</span><strong>{config.dhcp.enabled ? "On" : "Off"}</strong><small>{leases.length} active leases</small></article>
      </div>
    </section>

    <section className="classic-device-section">
      <div className="classic-section-heading"><div><span>LAN</span><h2>Connected devices.</h2></div><small>{leases.length} active leases</small></div>
      <div className="classic-device-table-wrap"><table className="classic-device-table"><thead><tr><th>Host</th><th>IP address</th><th>MAC</th><th>Expires</th></tr></thead><tbody>{leases.length === 0 ? <tr><td colSpan={4}>No active DHCP leases reported.</td></tr> : leases.map((lease) => <tr key={`${lease.mac}-${lease.ip_address}`}><td>{lease.hostname || "Unknown"}</td><td><code>{lease.ip_address}</code></td><td><code>{lease.mac}</code></td><td>{new Date(lease.expires_at * 1000).toLocaleString()}</td></tr>)}</tbody></table></div>
    </section>
  </section>;
}
