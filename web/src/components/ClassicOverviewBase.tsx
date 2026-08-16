import { useEffect, useMemo, useRef, useState } from "react";
import type { ReactNode } from "react";
import type { ApplianceHealth, GatewayHistoryPoint, GatewaySummary, RouterConfig, SystemStatus } from "../api-types";
import { apiFetch } from "../lib/api";
import HealthBanner, { HealthCheckDetails } from "./HealthBanner";

type Runtime = NonNullable<SystemStatus["runtime"]>;
type WANSpeedEstimate = { download_mbps: number; upload_mbps: number };

type Props = {
  config: RouterConfig;
  system: SystemStatus;
  runtime: Runtime;
  gatewaySummary: GatewaySummary | null;
  memoryPercent: number;
  diskPercent: number;
  gatewayTargetCount: number;
  lastRefresh: Date | null;
  health: ApplianceHealth | null;
  healthUnavailable: boolean;
};

type IconName = "check" | "gateway" | "key" | "shield" | "traffic" | "clock";

const WAN_ESTIMATE_STORAGE_KEY = "minimalrouter:wan-speed-estimate";
const WAN_ESTIMATE_ATTEMPT_KEY = "minimalrouter:wan-speed-estimate-attempt";
const WAN_ESTIMATE_RETRY_MS = 24 * 60 * 60 * 1000;

function storedWANEstimate(): WANSpeedEstimate | null {
  try {
    const raw = window.localStorage.getItem(WAN_ESTIMATE_STORAGE_KEY);
    if (!raw) return null;
    const parsed = JSON.parse(raw) as Partial<WANSpeedEstimate>;
    if (typeof parsed.download_mbps !== "number" || !Number.isFinite(parsed.download_mbps) || parsed.download_mbps <= 0) return null;
    if (typeof parsed.upload_mbps !== "number" || !Number.isFinite(parsed.upload_mbps) || parsed.upload_mbps <= 0) return null;
    return { download_mbps: parsed.download_mbps, upload_mbps: parsed.upload_mbps };
  } catch {
    return null;
  }
}

function OverviewIcon({ name }: { name: IconName }) {
  const paths: Record<IconName, ReactNode> = {
    check: <path d="m5 12 4 4L19 6" />,
    gateway: <path d="M3 17h3l2-5 3 8 3-14 3 11h4" />,
    key: <><circle cx="8" cy="15" r="4" /><path d="m11 12 8-8M16 7l2 2M14 9l2 2" /></>,
    shield: <path d="M12 3 5 6v5c0 4.5 2.9 7.2 7 9 4.1-1.8 7-4.5 7-9V6z" />,
    traffic: <path d="M6 19V9M12 19V5M18 19v-7" />,
    clock: <><circle cx="12" cy="12" r="9" /><path d="M12 7v5l3 2" /></>,
  };
  return <svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round">{paths[name]}</svg>;
}

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

function formatMetric(value: number | undefined, suffix: string, digits = 1) {
  return typeof value === "number" && Number.isFinite(value) ? `${value.toFixed(digits)}${suffix}` : "Unavailable";
}

type ChartPoint = { x: number; y: number };
type ChartDomain = { min: number; max: number };

function chartPoints(values: number[], width: number, height: number, domain?: ChartDomain) {
  if (values.length === 0) return [];
  const rawMin = domain?.min ?? Math.min(...values);
  const rawMax = domain?.max ?? Math.max(...values);
  const rawSpan = rawMax - rawMin;
  const min = domain ? rawMin : rawSpan > 0 ? rawMin - rawSpan * 0.16 : rawMin - 1;
  const max = domain ? rawMax : rawSpan > 0 ? rawMax + rawSpan * 0.16 : rawMax + 1;
  const span = Math.max(0.001, max - min);
  const verticalPadding = 8;
  const drawableHeight = height - verticalPadding * 2;
  return values.map((value, index) => ({
    x: values.length === 1 ? width / 2 : (index / (values.length - 1)) * width,
    y: verticalPadding + (1 - (value - min) / span) * drawableHeight,
  }));
}

function smoothPath(points: ChartPoint[]) {
  if (points.length === 0) return "";
  if (points.length === 1) return `M${points[0].x.toFixed(1)},${points[0].y.toFixed(1)}`;
  let path = `M${points[0].x.toFixed(1)},${points[0].y.toFixed(1)}`;
  for (let index = 0; index < points.length - 1; index += 1) {
    const previous = points[index - 1] || points[index];
    const current = points[index];
    const next = points[index + 1];
    const afterNext = points[index + 2] || next;
    const controlOne = { x: current.x + (next.x - previous.x) / 6, y: current.y + (next.y - previous.y) / 6 };
    const controlTwo = { x: next.x - (afterNext.x - current.x) / 6, y: next.y - (afterNext.y - current.y) / 6 };
    path += ` C${controlOne.x.toFixed(1)},${controlOne.y.toFixed(1)} ${controlTwo.x.toFixed(1)},${controlTwo.y.toFixed(1)} ${next.x.toFixed(1)},${next.y.toFixed(1)}`;
  }
  return path;
}

function smoothPathFor(values: number[], width: number, height: number, domain?: ChartDomain) {
  return smoothPath(chartPoints(values, width, height, domain));
}

function segmentedPaths(values: Array<number | null>, width: number, height: number, fixedDomain?: ChartDomain): string[] {
  const measured = values.filter((value): value is number => value !== null);
  if (measured.length === 0) return [];
  const rawMin = Math.min(...measured);
  const rawMax = Math.max(...measured);
  const span = rawMax - rawMin;
  const domain: ChartDomain = fixedDomain ?? (span > 0
    ? { min: rawMin - span * 0.16, max: rawMax + span * 0.16 }
    : { min: rawMin - 1, max: rawMax + 1 });
  const height_ = Math.max(0.001, domain.max - domain.min);
  const verticalPadding = 8;
  const drawableHeight = height - verticalPadding * 2;

  const paths: string[] = [];
  let run: ChartPoint[] = [];
  values.forEach((value, index) => {
    if (value === null) {
      if (run.length > 0) {
        paths.push(smoothPath(run));
        run = [];
      }
      return;
    }
    run.push({
      x: values.length === 1 ? width / 2 : (index / (values.length - 1)) * width,
      y: verticalPadding + (1 - Math.min(1, Math.max(0, (value - domain.min) / height_))) * drawableHeight,
    });
  });
  if (run.length > 0) paths.push(smoothPath(run));
  return paths.filter((path) => path !== "");
}

function stateClass(enabled: boolean, fallback = "is-off") {
  return enabled ? "is-good" : fallback;
}

export default function ClassicOverview({
  config,
  system,
  runtime,
  gatewaySummary,
  memoryPercent,
  diskPercent,
  gatewayTargetCount,
  lastRefresh,
  health,
  healthUnavailable,
}: Props) {
  const [history, setHistory] = useState<GatewayHistoryPoint[]>([]);
  const [bandwidthHistory, setBandwidthHistory] = useState<Array<{ rx: number; tx: number }>>([]);
  const [securityCount, setSecurityCount] = useState<number | null>(null);
  const [lastLogin, setLastLogin] = useState<{ timestamp: string; actor: string } | null>(null);
  const [healthDetailsOpen, setHealthDetailsOpen] = useState(false);
  const [wanEstimate, setWanEstimate] = useState<WANSpeedEstimate | null>(storedWANEstimate);
  const lastBytesRef = useRef<{ rx: number; tx: number; time: number } | null>(null);
  const healthDetailsRef = useRef<HTMLElement>(null);

  const showHealthDetails = () => {
    setHealthDetailsOpen(true);
    window.setTimeout(() => healthDetailsRef.current?.scrollIntoView({ behavior: "smooth", block: "start" }), 0);
  };

  useEffect(() => {
    let active = true;
    let timer = 0;

    const sample = async () => {
      if (!active || document.hidden) return;
      try {
        const response = await apiFetch("/api/v1/system");
        if (!response.ok) throw new Error("System status unavailable");
        const data = await response.json() as SystemStatus;
        if (!active || !data.runtime) return;
        const now = Date.now();
        const rx = data.runtime.rx_bytes || 0;
        const tx = data.runtime.tx_bytes || 0;
        if (lastBytesRef.current) {
          const elapsed = Math.max(0.1, (now - lastBytesRef.current.time) / 1000);
          const rxRate = Math.max(0, (rx - lastBytesRef.current.rx) * 8 / (1024 * 1024 * elapsed));
          const txRate = Math.max(0, (tx - lastBytesRef.current.tx) * 8 / (1024 * 1024 * elapsed));
          setBandwidthHistory((previous) => [...previous, { rx: rxRate, tx: txRate }].slice(-12));
        }
        lastBytesRef.current = { rx, tx, time: now };
      } catch {
        // Live bandwidth is advisory; a failed sample must not disturb routing UI.
      }
    };

    const schedule = () => {
      window.clearTimeout(timer);
      if (!active || document.hidden) return;
      timer = window.setTimeout(() => {
        void sample().finally(schedule);
      }, 5000);
    };

    const resume = () => {
      window.clearTimeout(timer);
      if (!active || document.hidden) return;
      lastBytesRef.current = null;
      void sample().finally(schedule);
    };

    const onVisibilityChange = () => {
      if (document.hidden) {
        window.clearTimeout(timer);
        return;
      }
      resume();
    };

    document.addEventListener("visibilitychange", onVisibilityChange);
    resume();
    return () => {
      active = false;
      window.clearTimeout(timer);
      document.removeEventListener("visibilitychange", onVisibilityChange);
    };
  }, []);

  useEffect(() => {
    const refreshEstimate = () => setWanEstimate(storedWANEstimate());
    window.addEventListener("minimalrouter:wan-speed-estimate", refreshEstimate);
    return () => window.removeEventListener("minimalrouter:wan-speed-estimate", refreshEstimate);
  }, []);

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

  useEffect(() => {
    let active = true;
    void apiFetch("/api/v1/audit/events?limit=50")
      .then((response) => response.ok ? response.json() : Promise.reject(new Error("Audit events unavailable")))
      .then((body: { events?: Array<{ event_type: string; timestamp: string; actor: string }> }) => {
        if (!active || !Array.isArray(body.events)) return;
        const login = body.events.find((event) => event.event_type === "auth.login_succeeded");
        setLastLogin(login ? { timestamp: login.timestamp, actor: login.actor } : null);
        setSecurityCount(body.events.filter((event) => /auth\.(csrf|origin|cross_site)_rejected/.test(event.event_type)).length);
      })
      .catch(() => undefined);
    return () => { active = false; };
  }, [lastRefresh]);

  const bandwidthDomain = useMemo<ChartDomain>(() => ({
    min: 0,
    max: Math.max(1, ...bandwidthHistory.flatMap((point) => [point.rx, point.tx])) * 1.12,
  }), [bandwidthHistory]);
  const rxPath = useMemo(() => smoothPathFor(bandwidthHistory.map((point) => point.rx), 1000, 130, bandwidthDomain), [bandwidthDomain, bandwidthHistory]);
  const txPath = useMemo(() => smoothPathFor(bandwidthHistory.map((point) => point.tx), 1000, 130, bandwidthDomain), [bandwidthDomain, bandwidthHistory]);
  const gatewayLatencyDomain = useMemo<ChartDomain>(() => {
    const highestLatency = Math.max(0, ...history.map((point) => typeof point.latency_ms === "number" ? point.latency_ms : 0));
    return { min: 0, max: Math.max(50, Math.ceil(highestLatency / 25) * 25) };
  }, [history]);
  const latencySegments = useMemo(
    () => segmentedPaths(history.map((point) => (typeof point.latency_ms === "number" ? point.latency_ms : null)), 1000, 130, gatewayLatencyDomain),
    [gatewayLatencyDomain, history],
  );
  const measuredLatencyCount = useMemo(
    () => history.filter((point) => typeof point.latency_ms === "number").length,
    [history],
  );
  const latestBandwidth = bandwidthHistory.at(-1);
  const gatewayState = gatewaySummary?.state || "unknown";
  const connectionKnown = Boolean(gatewaySummary?.available) || typeof runtime.wan_connected === "boolean";
  const wanConnected = gatewaySummary?.link?.connected ?? runtime.wan_connected ?? false;
  const verified = wanConnected && config.firewall.stateful_firewall && !system.recovery_required;
  const headline = system.recovery_required
    ? "Recovery required"
    : !connectionKnown
      ? "Checking connection"
      : verified
        ? gatewayState === "degraded" || gatewayState === "flapping" ? "Online with warnings" : "Online and verified"
        : "WAN disconnected";
  const heroState = verified ? "is-good" : system.recovery_required ? "is-bad" : "is-warning";
  const loadAverage = runtime.load_average?.length ? runtime.load_average.map((value) => value.toFixed(2)).join(" / ") : "Unavailable";
  const cpuIdle = typeof runtime.cpu_load_percent === "number" ? `${Math.max(0, 100 - runtime.cpu_load_percent).toFixed(0)}% idle` : "Unavailable";
  const ddnsProvider = config.cloudflare.ddns_provider === "noip" ? "No-IP" : "Cloudflare";
  const gatewayLabel = gatewayState === "unknown" ? "Gateway checking" : `Gateway ${gatewayState}`;
  const lastLoginDate = lastLogin ? new Date(lastLogin.timestamp) : null;
  const lastLoginTime = lastLoginDate ? lastLoginDate.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }) : "Checking";
  const lastLoginDetail = lastLogin && lastLoginDate ? `${lastLogin.actor} · ${lastLoginDate.toLocaleDateString()}` : "Audit event pending";
  const loss = Math.min(100, Math.max(0, gatewaySummary?.packet_loss_percent || 0));

  useEffect(() => {
    if (wanEstimate || !wanConnected || document.hidden) return;
    try {
      const lastAttempt = Number(window.localStorage.getItem(WAN_ESTIMATE_ATTEMPT_KEY) || 0);
      if (Date.now() - lastAttempt < WAN_ESTIMATE_RETRY_MS) return;
      window.localStorage.setItem(WAN_ESTIMATE_ATTEMPT_KEY, String(Date.now()));
    } catch {
      return;
    }

    const controller = new AbortController();
    void apiFetch("/api/v1/qos/speedtest?mode=estimate", { method: "POST", signal: controller.signal })
      .catch(() => undefined);
    return () => controller.abort();
  }, [wanConnected, wanEstimate]);

  const dnsCheck = health?.checks?.find((check) => check.id === "dns_dhcp" || check.id.startsWith("dns"));
  const dnsChipClass = healthUnavailable || !dnsCheck
    ? "is-off"
    : dnsCheck.state === "healthy"
      ? "is-good"
      : dnsCheck.state === "unknown"
        ? "is-off"
        : dnsCheck.state === "recovery_required" || dnsCheck.state === "degraded"
          ? "is-bad"
          : "is-warning";
  const dnsChipLabel = healthUnavailable || !dnsCheck ? "unknown" : dnsCheck.state === "healthy" ? "ok" : dnsCheck.state.replace("_", " ");

  const worstResource = Math.max(memoryPercent, diskPercent, Math.min(100, runtime.cpu_load_percent ?? 0));
  const resourceNote = typeof runtime.memory_total_bytes !== "number"
    ? { className: "is-unknown", label: "Resource telemetry unavailable" }
    : worstResource >= 90
      ? { className: "is-bad", label: `Resource pressure: highest usage is ${worstResource.toFixed(0)}%` }
      : worstResource >= 80
        ? { className: "is-warning", label: `Approaching limits: highest usage is ${worstResource.toFixed(0)}%` }
        : { className: "is-good", label: "Within normal operating range" };

  const storage = runtime.storage;
  const storageChip = !storage || !storage.available
    ? { className: "is-off", label: "Storage unknown" }
    : storage.level === "critical"
      ? { className: "is-bad", label: `Storage ${storage.usage_percent.toFixed(0)}%` }
      : storage.level === "warning"
        ? { className: "is-warning", label: `Storage ${storage.usage_percent.toFixed(0)}%` }
        : { className: "is-good", label: `Storage ${storage.usage_percent.toFixed(0)}%` };

  return <section className="classic-dashboard-overview" id="overview" aria-label="System overview">
    <div className="overview-service-ribbon" aria-label="Router service status">
      <span className={`overview-service-chip ${stateClass(config.firewall.stateful_firewall)}`}>Firewall {config.firewall.stateful_firewall ? "on" : "off"}</span>
      <span className={`overview-service-chip ${stateClass(config.wireguard.enabled)}`}>WireGuard{config.wireguard.enabled && <b>{system.runtime?.wireguard_active_peers || 0} / {(config.wireguard.peers || []).filter((peer) => peer.enabled).length}</b>}</span>
      <span className={`overview-service-chip ${stateClass(config.dhcp.enabled)}`}>DHCP{config.dhcp.enabled && <b>{runtime.dhcp_leases?.length || 0}</b>}</span>
      <span className={`overview-service-chip ${dnsChipClass}`}>DNS {dnsChipLabel}</span>
      <span className={`overview-service-chip ${config.cloudflare.ddns_enabled ? runtime.ddns?.running ? "is-good" : "is-info" : "is-off"}`}>{config.cloudflare.ddns_enabled ? `DDNS: ${ddnsProvider}` : "DDNS off"}</span>
      <span className={`overview-service-chip ${stateClass(config.squid_proxy.enabled)}`}>Squid Proxy {config.squid_proxy.enabled ? "on" : "off"}</span>
      <span className={`overview-service-chip ${config.qos.enabled ? "is-info" : "is-off"}`}>QoS {config.qos.enabled ? config.qos.algorithm : "off"}</span>
      <span className={`overview-service-chip ${stateClass(config.cloudflare.tunnel_enabled)}`}>Cloudflare Tunnel {config.cloudflare.tunnel_enabled ? "on" : "off"}</span>
      <span className={`overview-service-chip ${gatewayState === "healthy" ? "is-good" : gatewayState === "unknown" ? "is-off" : "is-warning"}`}>{gatewayLabel}</span>
      <span className={`overview-service-chip ${storageChip.className}`}>{storageChip.label}</span>
    </div>

    {storage && storage.level === "critical" && (
      <div className="dashboard-alert is-error" role="alert">
        <strong>Storage critical ({storage.usage_percent.toFixed(0)}% used):</strong> configuration changes are rejected until
        space is freed. Routing and the active firewall are unaffected.
      </div>
    )}
    {storage && storage.level === "warning" && (
      <div className="dashboard-alert is-warning" role="status">
        <strong>Storage at {storage.usage_percent.toFixed(0)}%.</strong> Durable configuration changes stop working at 90%.
      </div>
    )}

    <article className={`overview-status-hero ${heroState}`}>
      <div className="overview-hero-command">
        <div className="overview-hero-summary">
          <span className="overview-hero-kicker"><i aria-hidden="true" />System status</span>
          <h1>{headline}</h1>
          <p>{verified ? "WAN, security policy and local services are operating normally." : "Review WAN connectivity and the active appliance alerts."}</p>
          <div className="overview-summary-meta">
            <span><small>CPU state</small><strong>{cpuIdle}</strong></span>
            <span><small>Load average</small><strong>{loadAverage}</strong></span>
            <span className="overview-request-state"><small>Rejected requests</small><strong>{securityCount ?? "Checking"}</strong></span>
          </div>
          <div className="overview-summary-meta"><span><small>Guide</small><a href="/help.html" target="_blank" rel="noreferrer" className="overview-help-link"><strong>Help &amp; operator guide ↗</strong></a></span></div>
        </div>

        <section className="overview-wan-card" aria-label="WAN session and quality">
          <header><span>WAN connection</span><b className={wanConnected ? "is-good" : "is-bad"}><i aria-hidden="true" />{connectionKnown ? wanConnected ? "Connected" : "Disconnected" : "Checking"}</b></header>
          <div className="overview-wan-main"><div><small>Session</small><strong>PPPoE</strong></div><p><strong>{formatUptime(gatewaySummary?.pppoe_uptime_seconds || 0)}</strong><small>{gatewaySummary?.reconnects_24h ?? 0} reconnects / 24h</small></p></div>
          <div className="overview-wan-quality">
            <span><small>Latency</small><strong>{formatMetric(gatewaySummary?.latency_ms, " ms")}</strong></span>
            <span><small>Jitter</small><strong>{formatMetric(gatewaySummary?.jitter_ms, " ms")}</strong></span>
            <span><small>Line estimate</small><strong>{wanEstimate ? `~${wanEstimate.download_mbps.toFixed(0)} ↓ / ${wanEstimate.upload_mbps.toFixed(0)} ↑ Mbps` : wanConnected ? "Measuring…" : "Not measured"}</strong></span>
            <span><small>Probe targets</small><strong>{gatewaySummary?.targets?.length || gatewayTargetCount}</strong></span>
          </div>
        </section>

        <div className="overview-assurance">
          <div><span><OverviewIcon name="shield" /></span><p><small>Update trust</small><strong>{system.update_trust_configured ? "Signed updates enabled" : "Signed updates disabled"}</strong><em>{system.update_trust_configured ? "Package verification enforced" : "Signing key unavailable"}</em></p></div>
          <div><span><OverviewIcon name="key" /></span><p><small>Last admin access</small><strong>{lastLoginTime}</strong><em>{lastLoginDetail}</em></p></div>
        </div>
      </div>

      <div className="overview-technical-facts" aria-label="Router identity and WAN facts">
        <div><span>Public IP</span><strong>{runtime.public_ip || "Unavailable"}</strong></div>
        <div><span>WAN MAC</span><strong>{runtime.wan_mac || "Unknown"}</strong></div>
        <div><span>LAN MAC</span><strong>{runtime.lan_mac || "Unknown"}</strong></div>
        <div><span>Uptime</span><strong>{formatUptime(runtime.uptime_seconds)}</strong></div>
        <div><span>MTU</span><strong>{config.wan.mtu || 1492}</strong></div>
      </div>
    </article>

    <HealthBanner health={health} unavailable={healthUnavailable} onShowDetails={showHealthDetails} />

    <section className="overview-diagnostic-strip" aria-label="Appliance diagnostics">
      <div><OverviewIcon name="traffic" /><span><small>Conntrack</small><strong>{runtime.conntrack_count ?? 0} / {runtime.conntrack_max ?? 0}<em>{typeof runtime.conntrack_usage_percent === "number" ? `${runtime.conntrack_usage_percent.toFixed(2)}% utilized` : ""}</em></strong></span></div>
      <div><OverviewIcon name="clock" /><span><small>Time synchronization</small><strong className={runtime.time_synchronized ? "is-positive" : "is-warning"}>{runtime.time_synchronized ? "Synchronized" : "Not synchronized"}<em>{new Date().toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}</em></strong></span></div>
    </section>

    <div className="overview-content-grid">
      <section className="overview-panel overview-bandwidth-panel" aria-labelledby="bandwidth-title">
        <header className="overview-panel-header"><div><h2 id="bandwidth-title">Live bandwidth</h2><p>WAN throughput · last 60 seconds</p></div><div className="overview-legend"><span><i className="is-download" />Download</span><span><i className="is-upload" />Upload</span></div></header>
        <div className="overview-bandwidth-now"><p><strong>{latestBandwidth ? `${latestBandwidth.rx.toFixed(1)} Mbps ↓` : "Collecting"}</strong><small>Download</small></p><p><strong>{latestBandwidth ? `${latestBandwidth.tx.toFixed(1)} Mbps ↑` : "Collecting"}</strong><small>Upload</small></p></div>
        <div className="overview-chart-wrap">
          <svg viewBox="0 0 1000 150" preserveAspectRatio="none" role="img" aria-label="Live bandwidth history">
            <defs>
              <linearGradient id="bandwidth-download-fill" x1="0" x2="0" y1="0" y2="1">
                <stop offset="0%" stopColor="#1177e8" stopOpacity="0.24" />
                <stop offset="70%" stopColor="#1177e8" stopOpacity="0.07" />
                <stop offset="100%" stopColor="#1177e8" stopOpacity="0" />
              </linearGradient>
            </defs>
            <g className="overview-chart-grid"><line x1="0" y1="20" x2="1000" y2="20" /><line x1="0" y1="65" x2="1000" y2="65" /><line x1="0" y1="110" x2="1000" y2="110" /><line x1="0" y1="145" x2="1000" y2="145" /></g>
            {bandwidthHistory.length > 1 && rxPath && <><path className="overview-chart-area" d={`${rxPath} L1000,145 L0,145 Z`} /><path className="overview-chart-line is-download" d={rxPath} /></>}
            {bandwidthHistory.length > 1 && txPath && <path className="overview-chart-line is-upload" d={txPath} />}
          </svg>
          {bandwidthHistory.length < 2 && <span className="overview-chart-empty">Collecting live samples</span>}
        </div>
        <div className="overview-chart-axis"><span>60 sec ago</span><span>45 sec</span><span>30 sec</span><span>15 sec</span><span>Now</span></div>
      </section>

      <section className="overview-panel overview-resources-panel" aria-labelledby="resources-title">
        <header className="overview-panel-header"><div><h2 id="resources-title">Appliance resources</h2><p>Live system utilization</p></div></header>
        <div className="overview-resource-list">
          <article><div><span>CPU</span><small>{runtime.cpu_count || 0} logical cores</small></div><strong>{(runtime.cpu_load_percent || 0).toFixed(2)}%</strong><progress max="100" value={Math.min(100, runtime.cpu_load_percent || 0)} /></article>
          <article><div><span>Memory</span><small>{formatBytes(runtime.memory_used_bytes)} of {formatBytes(runtime.memory_total_bytes)}</small></div><strong>{formatBytes(runtime.memory_used_bytes)}</strong><progress max="100" value={Math.min(100, memoryPercent)} /></article>
          <article><div><span>Disk</span><small>{formatBytes(runtime.disk_used_bytes)} of {formatBytes(runtime.disk_total_bytes)}</small></div><strong>{formatBytes(runtime.disk_used_bytes)}</strong><progress max="100" value={Math.min(100, diskPercent)} /></article>
        </div>
        <div className={`overview-resource-note ${resourceNote.className}`}><OverviewIcon name="check" /><span>{resourceNote.label}</span></div>
      </section>

      <section className="overview-panel overview-quality-panel" aria-labelledby="quality-title">
        <header className="overview-panel-header"><div><h2 id="quality-title">Gateway quality</h2><p>Live samples · rolling one-hour window</p></div><span className={`overview-live-state ${gatewayState === "healthy" ? "is-good" : "is-warning"}`}><i aria-hidden="true" />{gatewayState === "unknown" ? "Checking" : gatewayState}</span></header>
        <div className="overview-quality-plot">
          <div className="overview-quality-heading"><span><i />Latency trend</span><strong>Lower is better</strong></div>
          <div className="overview-chart-wrap is-quality">
            <svg viewBox="0 0 1000 150" preserveAspectRatio="none" role="img" aria-label="Gateway latency history">
              <defs>
                <linearGradient id="gateway-quality-fill" x1="0" x2="0" y1="0" y2="1">
                  <stop offset="0%" stopColor="#1177e8" stopOpacity="0.2" />
                  <stop offset="72%" stopColor="#1177e8" stopOpacity="0.06" />
                  <stop offset="100%" stopColor="#1177e8" stopOpacity="0" />
                </linearGradient>
              </defs>
              <g className="overview-chart-grid"><line x1="0" y1="20" x2="1000" y2="20" /><line x1="0" y1="65" x2="1000" y2="65" /><line x1="0" y1="110" x2="1000" y2="110" /><line x1="0" y1="145" x2="1000" y2="145" /></g>
              {latencySegments.map((path, index) => <path className="overview-chart-line is-latency" d={path} key={index} />)}
            </svg>
            {history.length === 0 && <span className="overview-chart-empty">Waiting for gateway history</span>}
            {history.length > 0 && latencySegments.length === 0 && <span className="overview-chart-empty">No successful probes in this window</span>}
          </div>
          <div className="overview-chart-axis"><span>60 min</span><span>45 min</span><span>30 min</span><span>15 min</span><span>Now</span></div>
          {history.length > measuredLatencyCount && <p className="overview-chart-note">{history.length - measuredLatencyCount} of {history.length} samples had no successful probe; those periods are left blank rather than drawn as 0&nbsp;ms.</p>}
        </div>
        <div className="overview-loss-band"><div><span><i />Packet loss</span><strong>{loss.toFixed(1)}% throughout</strong></div><progress max="100" value={loss} /></div>
        <p className="overview-quality-note">Read-only WAN quality monitor</p>
      </section>
    </div>
    {healthDetailsOpen && <HealthCheckDetails health={health} unavailable={healthUnavailable} sectionRef={healthDetailsRef} />}
  </section>;
}
