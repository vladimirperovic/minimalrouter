import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { ReactNode } from "react";
import type { ApplianceHealth, GatewayHistoryPoint, GatewaySummary, RouterConfig, SystemStatus } from "../api-types";
import { apiFetch } from "../lib/api";
import HealthBanner, { HealthCheckDetails } from "./HealthBanner";
import BootActivityPanel from "./BootActivityPanel";
import OverviewAvailability from "./OverviewAvailability";
import OverviewSpeedTest from "./OverviewSpeedTest";
import OverviewTopTraffic from "./OverviewTopTraffic";
import { useVisiblePolling } from "../lib/useVisiblePolling";

type Runtime = NonNullable<SystemStatus["runtime"]>;

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
  onOpenUpdates?: () => void;
};

type IconName = "check" | "gateway" | "key" | "shield" | "traffic" | "clock";

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

function formatBytes(value?: number) {
  if (typeof value !== "number" || !Number.isFinite(value) || value < 0) return "Unavailable";
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
  onOpenUpdates,
}: Props) {
  const [history, setHistory] = useState<GatewayHistoryPoint[]>([]);
  const [historyUnavailable, setHistoryUnavailable] = useState(false);
  const [bandwidthHistory, setBandwidthHistory] = useState<Array<{ rx: number; tx: number }>>([]);
  const [bandwidthUnavailable, setBandwidthUnavailable] = useState(false);
  const [securityCount, setSecurityCount] = useState<number | null>(null);
  const [lastLogin, setLastLogin] = useState<{ timestamp: string; actor: string } | null>(null);
  const [healthDetailsOpen, setHealthDetailsOpen] = useState(false);
  const lastBytesRef = useRef<{ rx: number; tx: number; time: number } | null>(null);
  const toggleHealthDetails = () => {
    setHealthDetailsOpen((open) => !open);
  };

  const sampleBandwidth = useCallback(async (signal: AbortSignal) => {
    // A hidden tab must start a new baseline rather than average its idle time.
    signal.addEventListener("abort", () => { lastBytesRef.current = null; }, { once: true });
    try {
      const response = await apiFetch("/api/v1/system", { signal });
      if (!response.ok) throw new Error("System status unavailable");
      const data = (await response.json() as SystemStatus).runtime;
      if (signal.aborted) return;
      const rx = data?.rx_bytes;
      const tx = data?.tx_bytes;
      if (!data?.available || typeof rx !== "number" || typeof tx !== "number"
        || !Number.isFinite(rx) || !Number.isFinite(tx) || rx < 0 || tx < 0) throw new Error("Missing counters");
      const now = performance.now();
      const last = lastBytesRef.current;
      if (last && rx >= last.rx && tx >= last.tx) {
        const elapsed = Math.max(0.1, (now - last.time) / 1000);
        const rxRate = (rx - last.rx) * 8 / (1_000_000 * elapsed);
        const txRate = (tx - last.tx) * 8 / (1_000_000 * elapsed);
        setBandwidthHistory((previous) => [...previous, { rx: rxRate, tx: txRate }].slice(-13));
      } else {
        setBandwidthHistory([]);
      }
      lastBytesRef.current = { rx, tx, time: now };
      setBandwidthUnavailable(false);
    } catch {
      if (signal.aborted) return;
      lastBytesRef.current = null;
      setBandwidthHistory([]);
      setBandwidthUnavailable(true);
    }
  }, []);
  useVisiblePolling(sampleBandwidth, 5000);

  const loadHistory = useCallback(async (signal: AbortSignal) => {
    try {
      const response = await apiFetch("/api/v1/gateway/history?window=1h", { signal });
      if (!response.ok) throw new Error("History unavailable");
      const body = await response.json() as { points?: GatewayHistoryPoint[] };
      if (!Array.isArray(body.points)) throw new Error("History unavailable");
      if (signal.aborted) return;
      setHistory(body.points);
      setHistoryUnavailable(false);
    } catch {
      if (!signal.aborted) { setHistory([]); setHistoryUnavailable(true); }
    }
  }, []);
  useVisiblePolling(loadHistory, 30_000);

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
  const connectionKnown = Boolean(gatewaySummary?.available) || (runtime.available === true && typeof runtime.wan_connected === "boolean");
  const wanConnected = gatewaySummary?.available ? gatewaySummary.link.connected : runtime.available === true && runtime.wan_connected === true;
  const recoveryRequired = system.recovery_required || (!healthUnavailable && health?.state === "recovery_required");
  const verified = wanConnected && config.firewall.stateful_firewall && !recoveryRequired
    && !healthUnavailable && health?.state === "healthy" && gatewaySummary?.available === true && gatewayState === "healthy";
  const statusUnknown = healthUnavailable || !health || health.state === "unknown" || !gatewaySummary?.available || gatewayState === "unknown";
  const headline = recoveryRequired
    ? "Recovery required"
    : !connectionKnown
      ? "Checking connection"
      : verified
        ? "Online and verified"
        : wanConnected ? statusUnknown ? "Online · checks unavailable" : "Online with warnings" : "WAN disconnected";
  const heroState = verified ? "is-good" : recoveryRequired || (connectionKnown && !wanConnected) ? "is-bad" : "is-warning";
  const loadAverage = runtime.load_average?.length ? runtime.load_average.map((value) => value.toFixed(2)).join(" / ") : "Unavailable";
  const cpuIdle = typeof runtime.cpu_load_percent === "number" ? `${Math.max(0, 100 - runtime.cpu_load_percent).toFixed(0)}% idle` : "Unavailable";
  const ddnsProvider = config.cloudflare.ddns_provider === "noip" ? "No-IP" : "Cloudflare";
  const gatewayLabel = gatewayState === "unknown" ? "Gateway checking" : `Gateway ${gatewayState}`;
  const lastLoginDate = lastLogin ? new Date(lastLogin.timestamp) : null;
  const lastLoginTime = lastLoginDate ? lastLoginDate.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }) : "Checking";
  const lastLoginDetail = lastLogin && lastLoginDate ? `${lastLogin.actor} · ${lastLoginDate.toLocaleDateString()}` : "Audit event pending";
  const loss = gatewaySummary?.available && Number.isFinite(gatewaySummary.packet_loss_percent)
    ? Math.min(100, Math.max(0, gatewaySummary.packet_loss_percent)) : undefined;
  const today = `${new Date().toLocaleDateString("en-GB").replaceAll("/", ".")}.`;

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

  const cpuKnown = runtime.available === true && Number.isFinite(runtime.cpu_load_percent) && runtime.cpu_load_percent! >= 0;
  const memoryKnown = runtime.available === true && Number.isFinite(runtime.memory_used_bytes) && runtime.memory_used_bytes! >= 0
    && Number.isFinite(runtime.memory_total_bytes) && runtime.memory_total_bytes! > 0;
  const diskKnown = runtime.available === true && Number.isFinite(runtime.disk_used_bytes) && runtime.disk_used_bytes! >= 0
    && Number.isFinite(runtime.disk_total_bytes) && runtime.disk_total_bytes! > 0;
  const worstResource = Math.max(memoryKnown ? memoryPercent : 0, diskKnown ? diskPercent : 0, cpuKnown ? Math.min(100, runtime.cpu_load_percent!) : 0);
  const resourceNote = !cpuKnown && !memoryKnown && !diskKnown
    ? { className: "is-unknown", label: "Resource telemetry unavailable" }
    : worstResource >= 90
      ? { className: "is-bad", label: `Resource pressure: highest usage is ${worstResource.toFixed(0)}%` }
      : worstResource >= 80
        ? { className: "is-warning", label: `Approaching limits: highest usage is ${worstResource.toFixed(0)}%` }
        : !cpuKnown || !memoryKnown || !diskKnown
          ? { className: "is-unknown", label: "Some resource telemetry unavailable" }
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
        space is freed. Routing and the active firewall are unaffected. <a href="#recovery">Open Recovery →</a>
      </div>
    )}
    {storage && storage.level === "warning" && (
      <div className="dashboard-alert is-warning" role="status">
        <strong>Storage at {storage.usage_percent.toFixed(0)}%.</strong> Durable configuration changes stop working at 90%. <a href="#recovery">Open Recovery →</a>
      </div>
    )}

    <article className={`overview-status-hero ${heroState}`}>
      {/* The masthead answers the question the page exists for, then states
          the four readings an operator checks next. Everything else on the
          page is detail behind that answer. */}
      <div className="overview-summary-header">
      <header className="overview-masthead">
        <span className="overview-hero-kicker"><i aria-hidden="true" className={heroState} />System status · {today}</span>
        <h1>{headline}</h1>
        <p>{verified ? "WAN, security policy and local services are operating normally." : "Review WAN connectivity and the active appliance alerts."}</p>
      </header>

      <dl className="overview-headline-readings" aria-label="Headline readings">
        <div><dt>Uptime</dt><dd>{formatUptime(gatewaySummary?.pppoe_uptime_seconds || 0)}</dd><small>{gatewaySummary?.reconnects_24h ?? 0} reconnects / 24h</small></div>
        <div><dt>Latency</dt><dd>{formatMetric(gatewaySummary?.latency_ms, " ms")}</dd><small>jitter {formatMetric(gatewaySummary?.jitter_ms, " ms")}</small></div>
        <OverviewSpeedTest connected={wanConnected} />
      </dl>
      </div>

      {/* Three groups, equal weight, each a ruled list. A reading belongs to
          exactly one of them, so nothing has to be hunted for twice. */}
      <div className="overview-groups">
        <section className="overview-group" aria-label="Connection">
          <h2>Connection</h2>
          <dl>
            <div><dt>State</dt><dd className={!connectionKnown ? "is-info" : wanConnected ? "is-good" : "is-bad"}>{connectionKnown ? wanConnected ? "Connected" : "Disconnected" : "Checking"}</dd></div>
            <div><dt>Session</dt><dd className="is-info">PPPoE</dd></div>
            <div><dt>Public IP</dt><dd className="is-identifier">{runtime.public_ip || "Unavailable"}</dd></div>
            <div><dt>MTU</dt><dd>{config.wan.mtu || 1492}</dd></div>
            <div><dt>Probe targets</dt><dd>{gatewaySummary?.targets?.length || gatewayTargetCount}</dd></div>
            <div><dt>LAN clients</dt><dd>{runtime.dhcp_leases?.length ?? 0}</dd></div>
          </dl>
        </section>

        <section className="overview-group" aria-label="Load">
          <h2>Load</h2>
          <dl>
            <div><dt>CPU state</dt><dd>{cpuIdle}</dd></div>
            <div><dt>Load average</dt><dd>{loadAverage}</dd></div>
            <div><dt>Conntrack</dt><dd>{runtime.conntrack_count ?? 0} / {runtime.conntrack_max ?? 0}</dd></div>
            <div className="overview-request-state"><dt>Rejected requests</dt><dd className={securityCount === null ? "is-info" : undefined}>{securityCount ?? "Checking"}</dd></div>
            <OverviewAvailability />
          </dl>
        </section>

        <section className="overview-group" aria-label="Trust and access">
          <h2>Trust &amp; access</h2>
          <dl>
            <div><dt>Update trust</dt><dd>{system.update_trust_configured ? "Signed updates enabled" : "Updates unavailable"}</dd><small>{system.update_trust_configured ? "Package verification enforced" : "Signing key unavailable"}</small></div>
            <div><dt>Last admin access</dt><dd className="is-nowrap">{lastLoginTime}</dd><small>{lastLoginDetail}</small></div>
            <div><dt>Time synchronization</dt><dd className={runtime.time_synchronized ? "is-good" : "is-warning"}>{runtime.time_synchronized ? "Synchronized" : "Not synchronized"}</dd></div>
          </dl>
        </section>
      </div>

      {/* Identifiers are reference material: the smallest type on the card,
          at its foot, in the monospace face they are read in. */}
      <footer className="overview-technical-facts" aria-label="Router identity">
        <div><span>WAN MAC{config.wan.interface ? ` · ${config.wan.interface}` : ""}</span><strong>{runtime.wan_mac || "Unknown"}</strong></div>
        <div><span>LAN MAC{config.lan.interface ? ` · ${config.lan.interface}` : ""}</span><strong>{runtime.lan_mac || "Unknown"}</strong></div>
        <div><span>Guide</span><strong><a href="/help.html" target="_blank" rel="noreferrer" className="overview-help-link">Help &amp; operator guide ↗</a></strong></div>
      </footer>
    </article>

    <div className="overview-health-drawer">
      <HealthBanner health={health} unavailable={healthUnavailable} onShowDetails={toggleHealthDetails} detailsOpen={healthDetailsOpen} onOpenUpdates={onOpenUpdates} />
      {healthDetailsOpen && <HealthCheckDetails health={health} unavailable={healthUnavailable} onOpenUpdates={onOpenUpdates} />}
    </div>

    <section className="overview-diagnostic-strip" aria-label="Appliance diagnostics">
      <div><OverviewIcon name="traffic" /><span><small>Conntrack</small><strong>{runtime.conntrack_count ?? 0} / {runtime.conntrack_max ?? 0}<em>{typeof runtime.conntrack_usage_percent === "number" ? `${runtime.conntrack_usage_percent.toFixed(2)}% utilized` : ""}</em></strong></span></div>
      <div><OverviewIcon name="clock" /><span><small>Time synchronization</small><strong className={runtime.time_synchronized ? "is-positive" : "is-warning"}>{runtime.time_synchronized ? "Synchronized" : "Not synchronized"}<em>{new Date().toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}</em></strong></span></div>
    </section>

    <div className="overview-content-grid">
      <section className="overview-panel overview-bandwidth-panel" aria-labelledby="bandwidth-title">
        <header className="overview-panel-header"><div><h2 id="bandwidth-title">Live bandwidth</h2><p>WAN throughput · last 60 seconds</p></div><div className="overview-legend"><span><i className="is-download" />Download</span><span><i className="is-upload" />Upload</span></div></header>
        <div className="overview-bandwidth-now"><p><strong>{latestBandwidth ? `${latestBandwidth.rx.toFixed(1)} Mbps ↓` : bandwidthUnavailable ? "Unavailable" : "Collecting"}</strong><small>Download</small></p><p><strong>{latestBandwidth ? `${latestBandwidth.tx.toFixed(1)} Mbps ↑` : bandwidthUnavailable ? "Unavailable" : "Collecting"}</strong><small>Upload</small></p></div>
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
          {bandwidthHistory.length < 2 && <span className="overview-chart-empty">{bandwidthUnavailable ? "Bandwidth telemetry unavailable" : "Collecting live samples"}</span>}
        </div>
        <div className="overview-chart-axis"><span>60 sec ago</span><span>45 sec</span><span>30 sec</span><span>15 sec</span><span>Now</span></div>
      </section>

      <section className="overview-panel overview-resources-panel" aria-labelledby="resources-title">
        <header className="overview-panel-header"><div><h2 id="resources-title">Appliance resources</h2><p>Live system utilization</p></div></header>
        <div className="overview-resource-list">
          <article><div><span>CPU</span><small>{runtime.cpu_count ?? "Unknown"} logical cores</small></div><strong>{cpuKnown ? formatMetric(runtime.cpu_load_percent, "%", 2) : "Unavailable"}</strong>{cpuKnown && <progress aria-label="CPU usage" max="100" value={Math.min(100, runtime.cpu_load_percent!)} />}</article>
          <article><div><span>Memory</span><small title="Used includes reclaimable kernel file cache">{memoryKnown ? `${formatBytes(runtime.memory_used_bytes)} of ${formatBytes(runtime.memory_total_bytes)}` : "Unavailable"}{memoryKnown && runtime.app_memory_bytes ? ` · apps ${formatBytes(runtime.app_memory_bytes)}` : ""}</small></div><strong>{memoryKnown ? formatBytes(runtime.memory_used_bytes) : "Unavailable"}</strong>{memoryKnown && <progress aria-label="Memory usage" max="100" value={Math.min(100, memoryPercent)} />}</article>
          <article><div><span>Disk</span><small>{diskKnown ? `${formatBytes(runtime.disk_used_bytes)} of ${formatBytes(runtime.disk_total_bytes)}` : "Unavailable"}</small></div><strong>{diskKnown ? formatBytes(runtime.disk_used_bytes) : "Unavailable"}</strong>{diskKnown && <progress aria-label="Disk usage" max="100" value={Math.min(100, diskPercent)} />}</article>
        </div>
        <div className={`overview-resource-note ${resourceNote.className}`}><OverviewIcon name="check" /><span>{resourceNote.label}</span></div>
        <OverviewTopTraffic enabled={config.accounting?.enabled === true} />
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
            {history.length === 0 && <span className="overview-chart-empty">{historyUnavailable ? "Gateway history unavailable" : "Waiting for gateway history"}</span>}
            {history.length > 0 && latencySegments.length === 0 && <span className="overview-chart-empty">No successful probes in this window</span>}
          </div>
          <div className="overview-chart-axis"><span>60 min</span><span>45 min</span><span>30 min</span><span>15 min</span><span>Now</span></div>
          {history.length > measuredLatencyCount && <p className="overview-chart-note">{history.length - measuredLatencyCount} of {history.length} samples had no successful probe; those periods are left blank rather than drawn as 0&nbsp;ms.</p>}
        </div>
        <div className="overview-loss-band"><div><span><i />Packet loss</span><strong>{loss === undefined ? "Unavailable" : `${loss.toFixed(1)}% latest sample`}</strong></div>{loss !== undefined && <progress aria-label="Packet loss" max="100" value={loss} />}</div>
        <p className="overview-quality-note">Read-only WAN quality monitor</p>
      </section>

      <BootActivityPanel pppoeEnabled={config.wan.enabled} wireGuardEnabled={config.wireguard.enabled} />
    </div>
  </section>;
}
