import { useCallback, useEffect, useMemo, useState, useRef } from "react";
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
  const [bandwidthHistory, setBandwidthHistory] = useState<{rx: number, tx: number}[]>([]);
  const lastBytesRef = useRef<{rx: number, tx: number, time: number} | null>(null);
  const [searchQuery, setSearchQuery] = useState("");

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

  const staticMacs = useMemo(() => new Set((config.dhcp.static_leases || []).map(sl => sl.mac.toLowerCase())), [config.dhcp.static_leases]);
  
  const filteredLeases = useMemo(() => {
    if (!searchQuery) return leases;
    const lower = searchQuery.toLowerCase();
    return leases.filter(l => 
      (l.hostname && l.hostname.toLowerCase().includes(lower)) || 
      l.ip_address.includes(lower) || 
      l.mac.includes(lower)
    );
  }, [leases, searchQuery]);

  const wakeOnLan = useCallback(async (mac: string) => {
    try {
      await apiFetch("/api/v1/network/wol", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ mac }),
      });
    } catch (e) {
      console.error("WOL failed", e);
    }
  }, []);

  const headline = useMemo(() => {
    if (runtime.os === "linux") return `${runtime.architecture} minimalrouter`;
    return "Unsupported minimalrouter";
  }, [runtime]);

  return <section className="classic-dashboard-overview" aria-label="System Overview">
    <article className="classic-hero-card">
      <div className="classic-hero-heading">
        <div>
          <div className="classic-kicker">Local appliance</div>
          <h1>{headline}</h1>
        </div>
        {runtime.os === "linux" ? <span className="classic-state-pill is-primary">{runtime.architecture}</span> : <span className="classic-state-pill is-primary">Unsupported platform</span>}
      </div>

      <div className="classic-meta-row">
        <span>Public IP <b>{runtime.public_ip || "Unavailable"}</b></span>
        <span>Uptime <b>{formatUptime(runtime.uptime_seconds)}</b></span>
        <span>MTU <b>{config.wan.mtu || 1492}</b></span>
        <span>Revision <b>{config.revision}</b></span>
        <span className={system.update_trust_configured ? "is-positive" : "is-warning"}>{system.update_trust_configured ? "Signed updates enabled" : "Signed updates disabled"}</span>
      </div>
      <div className="classic-chips-row">
        {typeof runtime.temperature_c === "number" && (
           <span className="classic-chip" title="CPU Temperature">🌡️ {runtime.temperature_c.toFixed(1)}°C</span>
        )}
        <span className="classic-chip" title="Active LAN leases">🟢 Trenutno zakačeno {leases.length} LAN uređaja</span>
        <span className="classic-chip" title="Active WireGuard peers">🔵 {(config.wireguard.peers || []).filter((peer) => peer.enabled).length} WireGuard uređaja</span>
        <span className="classic-chip" title="Storage">💾 Storage {runtime.storage ? `${runtime.storage.usage_percent.toFixed(1)}% used` : "Unknown"}</span>
        <span className="classic-chip" title="Conntrack">🔌 Conntrack {runtime.conntrack_count ?? 0} / {runtime.conntrack_max ?? 0}</span>
        <span className="classic-chip" title="Time sync">🕒 Time {runtime.time_synchronized ? "Synchronized" : "Not verified"}</span>
      </div>

      <div className="classic-live-grid" style={{ marginBottom: "20px" }}>
        <article className="classic-live-card"><span>Gateway latency</span><strong>{metric(gatewaySummary?.latency_ms, " ms")}</strong><small>Read-only WAN quality monitor</small></article>
        <article className="classic-live-card"><span>Packet loss</span><strong>{metric(gatewaySummary?.packet_loss_percent, "%")}</strong><small>Across configured probe targets</small></article>
        <article className="classic-live-card"><span>PPPoE uptime</span><strong>{formatUptime(gatewaySummary?.pppoe_uptime_seconds || 0)}</strong><small>{gatewaySummary?.reconnects_24h ?? 0} reconnects / 24h</small></article>
      </div>

      <div className="classic-resource-grid" style={{ marginBottom: "20px" }}>
        <article>
          <span>CPU</span>
          <strong>{(runtime.cpu_load_percent || 0).toFixed(2)}%</strong>
          <small>{runtime.cpu_count || 0} logical cores {typeof runtime.temperature_c === "number" ? `· ${runtime.temperature_c.toFixed(1)}°C` : ""}</small>
          <progress max="100" value={Math.min(100, runtime.cpu_load_percent || 0)} />
        </article>
        <article><span>Memory</span><strong>{formatBytes(runtime.memory_used_bytes)}</strong><small>{formatBytes(runtime.memory_used_bytes)} of {formatBytes(runtime.memory_total_bytes)}</small><progress max="100" value={Math.min(100, memoryPercent)} /></article>
        <article><span>Disk</span><strong>{formatBytes(runtime.disk_used_bytes)}</strong><small>{formatBytes(runtime.disk_used_bytes)} of {formatBytes(runtime.disk_total_bytes)}</small><progress max="100" value={Math.min(100, diskPercent)} /></article>
      </div>

      <div className="classic-charts-row" style={{ display: "flex", gap: "20px" }}>
        <div className="classic-chart-block" style={{ flex: 1, minWidth: 0 }}>
          <div className="classic-chart-title"><div><strong>Gateway quality</strong><small>Live samples · last hour</small></div><div className="classic-chart-legend"><span><i className="is-latency" />Latency</span><span><i className="is-loss" />Packet loss</span></div></div>
          <div className="classic-chart-frame">
            <svg viewBox="0 0 1000 150" preserveAspectRatio="none" role="img" aria-label="Gateway latency and packet-loss history"><line x1="0" y1="20" x2="1000" y2="20" /><line x1="0" y1="65" x2="1000" y2="65" /><line x1="0" y1="110" x2="1000" y2="110" />{latencyPath && <path className="classic-chart-line is-latency" d={latencyPath} />}{lossPath && <path className="classic-chart-line is-loss" d={lossPath} />}</svg>
            {history.length === 0 && <span className="classic-chart-empty">Waiting for gateway history…</span>}
          </div>
          <div className="classic-chart-axis"><span>Older</span><span>Now</span></div>
        </div>
        
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
      </div>
    </article>

    <section className="classic-device-section">
      <div className="classic-section-heading">
        <div>
          <span>LAN</span>
          <h2>Connected devices.</h2>
        </div>
        <div style={{ display: "flex", gap: "10px", alignItems: "center" }}>
          <input 
            type="text" 
            placeholder="Search devices..." 
            value={searchQuery}
            onChange={e => setSearchQuery(e.target.value)}
            style={{ padding: "6px 12px", borderRadius: "20px", border: "1px solid var(--classic-border)", fontSize: "12px", background: "var(--classic-panel)", color: "var(--classic-text)" }}
          />
          <small>{filteredLeases.length} active leases</small>
        </div>
      </div>
      <div className="classic-device-table-wrap">
        <table className="classic-device-table">
          <thead>
            <tr>
              <th>Host</th>
              <th>IP address</th>
              <th>MAC</th>
              <th>Expires</th>
              <th>Actions</th>
            </tr>
          </thead>
          <tbody>
            {filteredLeases.length === 0 ? <tr><td colSpan={5}>No active DHCP leases found.</td></tr> : filteredLeases.map((lease) => {
              const isStatic = staticMacs.has(lease.mac.toLowerCase());
              return (
                <tr key={`${lease.mac}-${lease.ip_address}`} style={isStatic ? { background: "rgba(0,0,0,0.03)" } : {}}>
                  <td style={{ fontWeight: isStatic ? 600 : "normal" }}>
                    {lease.hostname || "Unknown"}
                    {isStatic && <span style={{ marginLeft: "8px", fontSize: "10px", background: "var(--classic-purple)", color: "white", padding: "2px 6px", borderRadius: "10px" }}>Static</span>}
                  </td>
                  <td><code>{lease.ip_address}</code></td>
                  <td><code>{lease.mac}</code></td>
                  <td>{new Date(lease.expires_at * 1000).toLocaleString()}</td>
                  <td>
                    <button 
                      type="button" 
                      onClick={() => void wakeOnLan(lease.mac)}
                      className="quiet-button"
                      style={{ fontSize: "11px", padding: "4px 8px", background: "var(--classic-border)", borderRadius: "4px", fontWeight: "bold" }}
                      title="Wake-on-LAN"
                    >
                      WOL
                    </button>
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </section>
  </section>;
}
