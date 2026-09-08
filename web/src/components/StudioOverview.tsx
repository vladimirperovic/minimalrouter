import { useCallback, useRef, useState } from "react";
import type { ComponentProps, ReactNode } from "react";
import type { GatewayHistoryPoint, SystemStatus } from "../api-types";
import type ClassicOverview from "./ClassicOverviewBase";
import { apiFetch } from "../lib/api";
import { useVisiblePolling } from "../lib/useVisiblePolling";
import HealthBanner, { HealthCheckDetails } from "./HealthBanner";
import BootActivityPanel from "./BootActivityPanel";
import DeviceLeasesTable from "./DeviceLeasesTable";

function Icon({ name }: { name: 'check' | 'arrow' | 'network' | 'clock' | 'shield' | 'activity' }) {
  const paths: Record<typeof name, ReactNode> = {
    check: <path d="m5 12 4 4L19 6"/>, arrow: <path d="M4 12h16m-6-6 6 6-6 6"/>,
    network: <><rect x="3" y="3" width="18" height="12" rx="2"/><path d="M8 21h8m-4-6v6"/></>,
    clock: <><circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/></>,
    shield: <path d="m12 3 8 3v6c0 4-5 8-8 9-3-1-8-5-8-9V6z"/>,
    activity: <path d="M2 12h4l3-8 6 16 3-8h4"/>,
  };
  return <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">{paths[name]}</svg>;
}
const metric = (value: number | undefined, unit = '', digits = 1) => typeof value === 'number' && Number.isFinite(value) ? `${value.toFixed(digits)}${unit}` : 'Unavailable';
function bytes(value?: number) {
  if (value === undefined) return 'Unavailable';
  const unit = Math.min(4, Math.floor(Math.log(Math.max(1, value)) / Math.log(1024)));
  return `${(value / 1024 ** unit).toFixed(unit > 1 ? 1 : 0)} ${['B', 'KB', 'MB', 'GB', 'TB'][unit]}`;
}
function uptime(seconds?: number) {
  if (!seconds) return 'Unavailable';
  return seconds >= 86400 ? `${Math.floor(seconds / 86400)}d ${Math.floor(seconds % 86400 / 3600)}h` : `${Math.floor(seconds / 3600)}h ${Math.floor(seconds % 3600 / 60)}m`;
}
function plot(values: Array<number | null>, maximum: number) {
  let move = true;
  return values.map((value, i) => {
    if (value === null) { move = true; return ''; }
    const part = `${move ? 'M' : 'L'}${(i / Math.max(1, values.length - 1) * 800).toFixed(2)},${(166 - Math.min(value / maximum, 1) * 150).toFixed(2)}`;
    move = false;
    return part;
  }).join(' ');
}

export default function StudioOverview({ config, runtime, gatewaySummary, health, healthUnavailable, memoryPercent, diskPercent }: ComponentProps<typeof ClassicOverview>) {
  const [checks, setChecks] = useState(false);
  const [chart, setChart] = useState<'bandwidth' | 'gateway'>('bandwidth');
  const [samples, setSamples] = useState<Array<{ rx: number; tx: number }>>([]);
  const [history, setHistory] = useState<GatewayHistoryPoint[]>([]);
  const [sampleError, setSampleError] = useState(false);
  const [historyError, setHistoryError] = useState(false);
  const previous = useRef<{ rx: number; tx: number; time: number } | null>(null);
  const sample = useCallback(async (signal: AbortSignal) => {
    try {
      const response = await apiFetch('/api/v1/system', { signal });
      if (!response.ok) throw new Error('Unavailable');
      const data = (await response.json() as SystemStatus).runtime;
      if (signal.aborted) return;
      if (!data?.available || !Number.isFinite(data.rx_bytes) || !Number.isFinite(data.tx_bytes)) throw new Error('Unavailable');
      const current = { rx: data.rx_bytes!, tx: data.tx_bytes!, time: performance.now() };
      const last = previous.current;
      if (last && current.rx >= last.rx && current.tx >= last.tx) {
        const elapsed = Math.max(.1, (current.time - last.time) / 1000);
        setSamples(values => [...values, { rx: (current.rx - last.rx) * 8 / 1e6 / elapsed, tx: (current.tx - last.tx) * 8 / 1e6 / elapsed }].slice(-13));
      } else setSamples([]);
      previous.current = current;
      setSampleError(false);
    } catch {
      if (signal.aborted) return;
      previous.current = null; setSamples([]); setSampleError(true);
    }
  }, []);
  const loadHistory = useCallback(async (signal: AbortSignal) => {
    try {
      const response = await apiFetch('/api/v1/gateway/history?window=1h', { signal });
      if (!response.ok) throw new Error('Unavailable');
      const body = await response.json() as { points?: GatewayHistoryPoint[] };
      if (signal.aborted) return;
      setHistory(Array.isArray(body.points) ? body.points : []); setHistoryError(false);
    } catch { if (!signal.aborted) { setHistory([]); setHistoryError(true); } }
  }, []);
  useVisiblePolling(sample, 5000);
  useVisiblePolling(loadHistory, 30000);
  const known = runtime.available === true;
  const connected = known && runtime.wan_connected === true;
  const healthy = connected && !healthUnavailable && health?.state === 'healthy' && gatewaySummary?.available === true && gatewaySummary.link.connected && gatewaySummary.state === 'healthy';
  const unknown = !known || !health || healthUnavailable || health.state === 'unknown';
  const title = healthy ? 'All clear.' : unknown ? 'Checking in.' : 'Let’s take a look.';
  const leaseList = known ? runtime.dhcp_leases ?? [] : [];
  const latest = samples.at(-1);
  const values = chart === 'bandwidth' ? samples.map(p => p.rx) : history.map(p => typeof p.latency_ms === 'number' && Number.isFinite(p.latency_ms) ? p.latency_ms : null);
  const secondary = chart === 'bandwidth' ? samples.map(p => p.tx) : [];
  const max = Math.max(1, ...values.filter((n): n is number => n !== null), ...secondary) * 1.15;
  const chartError = chart === 'bandwidth' ? sampleError : historyError;
  const hasPlot = !chartError && values.filter(v => v !== null).length >= 2;
  const resourceRows: Array<[string, string, number | undefined]> = [
    ['CPU load', known ? metric(runtime.cpu_load_percent, '%') : 'Unavailable', known ? runtime.cpu_load_percent : undefined],
    ['Memory', known ? `${bytes(runtime.memory_used_bytes)} / ${bytes(runtime.memory_total_bytes)}` : 'Unavailable', known && runtime.memory_total_bytes ? memoryPercent : undefined],
    ['Storage', known ? `${bytes(runtime.disk_used_bytes)} / ${bytes(runtime.disk_total_bytes)}` : 'Unavailable', known && runtime.disk_total_bytes ? diskPercent : undefined],
  ];
  return <section id="overview" className="studio-overview" aria-label="System overview">
    <header className="studio-page-heading"><div><p className="studio-eyebrow">YOUR NETWORK · AT A GLANCE</p><h1>{healthy ? 'Your network, in good shape.' : 'Your network, at a glance.'}</h1><p>A clear view of the things that keep you connected.</p></div><a href="#logs" className="studio-link">View activity <Icon name="arrow"/></a></header>
    {runtime.storage?.level === 'critical' && <div className="dashboard-alert is-error" role="alert"><strong>Storage critical ({runtime.storage.usage_percent.toFixed(0)}% used):</strong> configuration changes are rejected until space is freed. Routing and the active firewall are unaffected.</div>}
    {runtime.storage?.level === 'warning' && <div className="dashboard-alert is-warning" role="status"><strong>Storage at {runtime.storage.usage_percent.toFixed(0)}%.</strong> Durable configuration changes stop working at 90%.</div>}
    <div className="studio-hero-grid">
      <article className={`studio-connection ${healthy ? 'is-healthy' : 'needs-attention'}`}>
        <p className="studio-eyebrow">CONNECTION STATUS <i/></p><div className="studio-connection-body"><span className="studio-orbit"><Icon name={healthy ? 'check' : 'activity'}/></span><div><h2>{title}</h2><p>{healthy ? 'Your network is running just the way it should.' : unknown ? 'Measured status is not yet available. Review the checks below.' : 'A connection or appliance check needs your attention.'}</p></div></div>
        <footer><Icon name="clock"/><span>{connected ? `WAN connected · ${uptime(gatewaySummary?.pppoe_uptime_seconds)}` : known ? 'WAN disconnected' : 'WAN status unavailable'}</span></footer>
      </article>
      <article className="studio-card studio-chart-card"><header className="studio-card-header"><div><p className="studio-eyebrow">THE BIG PICTURE</p><h2>{chart === 'bandwidth' ? 'Network activity' : 'Gateway quality'}</h2></div><div className="studio-segment" aria-label="Chart metric">{(['bandwidth', 'gateway'] as const).map(id => <button type="button" key={id} aria-pressed={chart === id} onClick={() => setChart(id)}>{id === 'bandwidth' ? 'Bandwidth' : 'Latency'}</button>)}</div></header>
        <div className="studio-chart-metrics">{chart === 'bandwidth' ? <><div><span>↓ Download</span><strong>{latest ? latest.rx.toFixed(1) : '—'}<small>Mbps</small></strong></div><div><span>↑ Upload</span><strong>{latest ? latest.tx.toFixed(1) : '—'}<small>Mbps</small></strong></div></> : <><div><span>Latency</span><strong>{metric(gatewaySummary?.latency_ms)}<small>ms</small></strong></div><div><span>Packet loss</span><strong>{metric(gatewaySummary?.packet_loss_percent)}<small>%</small></strong></div></>}</div>
        <div className="studio-chart"><svg viewBox="0 0 800 180" preserveAspectRatio="none" role="img" aria-label={chart === 'bandwidth' ? 'Live bandwidth history' : 'Gateway latency history'}><defs><linearGradient id="studio-chart-fill" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stopColor="var(--studio-chart-green)" stopOpacity=".22"/><stop offset="1" stopColor="var(--studio-chart-green)" stopOpacity="0"/></linearGradient></defs>{[16,66,116,166].map(y => <line key={y} x1="0" x2="800" y1={y} y2={y} className="studio-grid-line"/>)}{hasPlot && <>{chart === 'bandwidth' && <path d={`${plot(values,max)} L800,180 L0,180 Z`} fill="url(#studio-chart-fill)"/>}<path d={plot(values,max)} className="studio-chart-line"/>{secondary.length > 1 && <path d={plot(secondary,max)} className="studio-chart-line is-upload"/>}</>}</svg>{!hasPlot && <p className="studio-chart-empty">{chartError ? 'Live data unavailable' : 'Collecting measured samples…'}</p>}</div>
        <div className="studio-chart-axis"><span>{chart === 'bandwidth' ? 'Recent samples · every 5 seconds' : 'Last hour · gaps indicate missing probes'}</span><span>Now</span></div>
        <footer className="studio-chart-footer"><span><i/>{chart === 'bandwidth' ? 'Download' : 'Latency'}</span>{chart === 'bandwidth' && <span><i className="is-upload"/>Upload</span>}<a href={chart === 'bandwidth' ? '#traffic' : '#gateway'}>Explore details <Icon name="arrow"/></a></footer>
      </article>
    </div>
    <div className="studio-metrics">{([
      ['network', 'DHCP leases', known ? String(leaseList.length) : '—', 'Reserved until expiry'],
      ['activity', 'Gateway latency', metric(gatewaySummary?.latency_ms, ' ms'), gatewaySummary?.state || 'Unavailable'],
      ['shield', 'Firewall policy', config.firewall.stateful_firewall ? 'Enabled' : 'Disabled', 'Configured stateful protection'],
      ['clock', 'Appliance uptime', known ? uptime(runtime.uptime_seconds) : '—', 'Since the last restart'],
    ] as const).map(([icon,label,value,note]) => <article key={label}><Icon name={icon}/><div><span>{label}</span><strong>{value}</strong><small>{note}</small></div></article>)}</div>
    <div className="studio-detail-grid"><DeviceLeasesTable view="active" compact leases={leaseList} config={config} />
      <section className="studio-card studio-resource-card"><header className="studio-card-header"><div><p className="studio-eyebrow">APPLIANCE HEALTH</p><h2>{known && Math.max(runtime.cpu_load_percent ?? 100, memoryPercent, diskPercent) < 80 ? "Room to breathe." : "Appliance resources."}</h2></div><Icon name="activity"/></header><div className="studio-resources">{resourceRows.map(([label,value,percent]) => <div key={label}><div><span>{label}</span><strong>{value}</strong></div>{percent !== undefined && <progress value={Math.min(100,Math.max(0,percent))} max="100" aria-label={label}/>}</div>)}</div><div className="studio-resource-footer"><span>Connection tracking</span><strong>{known ? `${runtime.conntrack_count ?? '—'} / ${runtime.conntrack_max ?? '—'}` : 'Unavailable'}</strong></div></section></div>
    <div className="studio-health-drawer"><HealthBanner health={health} unavailable={healthUnavailable} detailsOpen={checks} onShowDetails={() => setChecks(!checks)}/>{checks && <HealthCheckDetails health={health} unavailable={healthUnavailable}/>}</div>
    <div className="studio-bottom-grid"><section className="studio-card studio-identifiers"><header className="studio-card-header"><div><p className="studio-eyebrow">THE CONNECTION DETAILS</p><h2>Everything in its place.</h2></div><Icon name="network"/></header><dl><div><dt>Public IP</dt><dd>{runtime.public_ip || 'Unavailable'}</dd></div><div><dt>WAN MAC · {config.wan.interface}</dt><dd>{runtime.wan_mac || 'Unavailable'}</dd></div><div><dt>LAN MAC · {config.lan.interface}</dt><dd>{runtime.lan_mac || 'Unavailable'}</dd></div><div><dt>Time synchronization</dt><dd>{known ? runtime.time_synchronized ? 'Synchronized' : 'Not synchronized' : 'Unavailable'}</dd></div></dl></section><BootActivityPanel pppoeEnabled={config.wan.enabled} wireGuardEnabled={config.wireguard.enabled}/></div>
    <footer className="studio-page-footer"><span>Minimal Router OS</span><a href="/help.html" target="_blank" rel="noreferrer">Help & operator guide ↗</a></footer>
  </section>;
}
