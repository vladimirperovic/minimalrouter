import React, { useState } from "react";
import type { FormEvent } from "react";
import { apiFetch } from "../lib/api";
import DNSFilterPanel from "./DNSFilterPanel";
import AuditLogPanel from "./AuditLogPanel";
import GatewayQualityPanel, { GatewayOverviewCard } from "./GatewayQualityPanel";
import DeviceLeasesTable from "./DeviceLeasesTable";
import type { GatewaySettings, GatewaySummary, RouterConfig, Snapshot, SystemStatus, WireGuardPeer } from "../api-types";
import "./DNSFilterPanel.css";

export type SectionID = "overview" | "gateway" | "network" | "firewall" | "qos" | "wireguard" | "cloudflare" | "squid" | "dns-filter" | "wifi" | "recovery" | "security" | "logs";

type Runtime = NonNullable<SystemStatus["runtime"]>;
type ApplyConfig = (mutate: (next: RouterConfig) => void, success: string) => Promise<void>;

type SpeedTestResult = {
  download_mbps: number;
  upload_mbps: number;
  suggested_download_mbps: number;
  suggested_upload_mbps: number;
};

type Props = {
  active: SectionID;
  config: RouterConfig;
  system: SystemStatus;
  gatewaySummary: GatewaySummary | null;
  gatewaySettings: GatewaySettings;
  runtime: Runtime;
  memoryPercent: number;
  diskPercent: number;
  leases: NonNullable<Runtime["dhcp_leases"]>;
  snapshots: Snapshot[];
  busy: boolean;
  lastRefresh: Date | null;
  load: () => Promise<void>;
  applyConfig: ApplyConfig;
  applyGatewayMonitoring: (settings: GatewaySettings) => void;
  submitNetwork: (event: FormEvent<HTMLFormElement>) => void;
  submitCloudflare: (event: FormEvent<HTMLFormElement>) => void;
  submitSquid: (event: FormEvent<HTMLFormElement>) => void;
  submitWiFi: (event: FormEvent<HTMLFormElement>) => void;
  submitQoS: (event: FormEvent<HTMLFormElement>) => void;
  runSpeedTest: () => Promise<void>;
  toggleQoS: (enabled: boolean) => void;
  toggleWAN: (enabled: boolean) => void;
  toggleDHCP: (enabled: boolean) => void;
  toggleCloudflare: (enabled: boolean) => void;
  toggleSquid: (enabled: boolean) => void;
  toggleWiFi: (enabled: boolean) => void;
  speedTest: SpeedTestResult | null;
  speedTesting: boolean;
  createSnapshot: () => Promise<void>;
  restoreSnapshot: (id: string) => Promise<void>;
  changePassword: (event: FormEvent<HTMLFormElement>) => Promise<void>;
  setError: (message: string) => void;
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

function formatHandshake(epoch: number) {
  const diff = Math.max(0, Date.now() / 1000 - epoch);
  if (diff < 60) return "Just now";
  const minutes = Math.floor(diff / 60);
  if (minutes < 60) return `${minutes} min ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours} h ago`;
  return new Date(epoch * 1000).toLocaleString();
}

export default function DashboardSections({
  active, config, system, gatewaySummary, gatewaySettings, runtime, memoryPercent, diskPercent, leases, snapshots, busy,
  lastRefresh, load, applyConfig, applyGatewayMonitoring, submitNetwork, submitCloudflare, submitSquid,
  submitWiFi, submitQoS, runSpeedTest, toggleQoS, toggleWAN, toggleDHCP, toggleCloudflare, toggleSquid, toggleWiFi, speedTest, speedTesting, createSnapshot, restoreSnapshot, changePassword, setError,
}: Props) {
  const [ddnsTab, setDdnsTab] = useState(config.cloudflare.ddns_provider || "noip");
  const [wgConfig, setWgConfig] = useState<{name: string, config: string, qr?: string} | null>(null);
  const [addingPeer, setAddingPeer] = useState(false);
  const [confirmDeletePeer, setConfirmDeletePeer] = useState<{ id: string, name: string } | null>(null);

  const handleAddPeer = async (e: React.FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    setAddingPeer(true);
    try {
      const data = new FormData(e.currentTarget);
      const res = await apiFetch("/api/v1/wireguard/peers", {
        method: "POST",
        body: JSON.stringify({
          name: data.get("name"),
          client_ip_address: data.get("client_ip_address"),
          server_endpoint: data.get("server_endpoint")
        })
      });
      if (res.ok) {
        const body = await res.json();
        setWgConfig({name: body.peer.name, config: body.client_config, qr: body.qr_code_data});
        void load();
      } else {
        const err = await res.json().catch(()=>({}));
        setError(err.error || "Failed to add peer");
      }
    } catch (err: any) {
      setError(err.message || "Failed to add peer");
    } finally {
      setAddingPeer(false);
    }
  };

  return <>
{active === "overview" && <section className="dashboard-section" id="overview">
  <div className="dashboard-section-heading"><div><p className="eyebrow">Live status</p><h2>Router overview</h2>{lastRefresh && <small>Updated {lastRefresh.toLocaleTimeString()}</small>}</div><button className="button secondary" onClick={() => void load()} type="button">Refresh</button></div>
  <div className="metric-grid">
    <article><span>Uptime</span><strong>{formatUptime(runtime.uptime_seconds)}</strong><small>{runtime.os || "Runtime unavailable"}</small></article>
    <article><span>CPU</span><strong>{Math.round(runtime.cpu_load_percent || 0)}%</strong><small>{runtime.cpu_count || 0} logical cores</small></article>
    <article><span>Memory</span><strong>{memoryPercent}%</strong><small>{formatBytes(runtime.memory_used_bytes)} / {formatBytes(runtime.memory_total_bytes)}</small></article>
    <article><span>Disk</span><strong>{diskPercent}%</strong><small>{formatBytes(runtime.disk_used_bytes)} / {formatBytes(runtime.disk_total_bytes)}</small></article>
    <article><span>LAN</span><strong>{system.lan_ip || config.lan.ip_address}</strong><small>{config.lan.interface}</small></article>
    <article><span>Update trust</span><strong>{system.update_trust_configured ? "Pinned" : "Disabled"}</strong><small>{system.update_trust_configured ? "Ed25519 key installed" : "No signing key"}</small></article>
    <GatewayOverviewCard summary={gatewaySummary} />
  </div>
  <article className="card table-card">
    <div className="card-title-row"><div><h3>Connected DHCP devices</h3><p>Runtime lease view; names and addresses stay local.</p></div><span className="quiet-meta">{leases.length} leases</span></div>
    <div className="table-scroll"><table><thead><tr><th>Host</th><th>IP</th><th>MAC</th><th>Expires</th></tr></thead><tbody>{leases.length === 0 ? <tr><td className="empty-state" colSpan={4}>No active leases reported.</td></tr> : leases.map((lease) => <tr key={`${lease.mac}-${lease.ip_address}`}><td>{lease.hostname || "Unknown"}</td><td><code>{lease.ip_address}</code></td><td><code>{lease.mac}</code></td><td>{new Date(lease.expires_at * 1000).toLocaleString()}</td></tr>)}</tbody></table></div>
  </article>
</section>}

{active === "gateway" && <GatewayQualityPanel busy={busy} onApply={applyGatewayMonitoring} onError={setError} settings={gatewaySettings} summary={gatewaySummary} />}

{active === "network" && <section className="dashboard-section" id="network">
  <div className="dashboard-section-heading"><div><p className="eyebrow">Connectivity</p><h2>WAN, LAN and DHCP</h2></div></div>
  <form className="settings-form" key={`network-${config.revision}`} onSubmit={submitNetwork}>
    <fieldset><legend>WAN / PPPoE</legend><label className="checkbox-row"><input checked={config.wan.enabled} type="checkbox" onChange={(e) => toggleWAN(e.target.checked)} /><span>Enable PPPoE WAN</span></label><div className="form-grid two"><label className="field"><span>WAN interface</span><input defaultValue={config.wan.interface} name="wan_interface" required /></label><label className="field"><span>MTU</span><input defaultValue={config.wan.mtu} max="1500" min="1280" name="wan_mtu" type="number" /></label><label className="field"><span>PPPoE username</span><input defaultValue={config.wan.username} name="pppoe_username" /></label><label className="field"><span>New PPPoE password</span><input autoComplete="new-password" name="pppoe_password" placeholder="Leave blank to keep stored secret" type="password" /></label></div></fieldset>
    <fieldset><legend>LAN and DHCP</legend><div className="form-grid two"><label className="field"><span>LAN interface</span><input defaultValue={config.lan.interface} name="lan_interface" required /></label><label className="field"><span>Gateway IPv4</span><input defaultValue={config.lan.ip_address} name="lan_ip" required /></label><label className="field"><span>Prefix</span><select defaultValue={String(config.lan.cidr || "").split("/")[1] || "24"} name="lan_prefix"><option value="24">/24</option><option value="16">/16</option></select></label><label className="field"><span>Lease time</span><input defaultValue={config.dhcp.lease_time} name="lease_time" required /></label><label className="field"><span>DHCP start</span><input defaultValue={config.dhcp.range_start} name="dhcp_start" required /></label><label className="field"><span>DHCP end</span><input defaultValue={config.dhcp.range_end} name="dhcp_end" required /></label><label className="field form-span"><span>Upstream DNS, comma separated</span><input defaultValue={(config.dhcp.dns_servers || []).join(", ")} name="dns_servers" required /></label></div>    <label className="checkbox-row"><input checked={config.dhcp.enabled} type="checkbox" onChange={(e) => toggleDHCP(e.target.checked)} /><span>Enable DHCP server</span></label></fieldset>
    <div className="form-actions"><button className="button primary" disabled={busy} type="submit">Save settings</button></div>
  </form>
  <DeviceLeasesTable leases={leases} config={config} />
</section>}

{active === "firewall" && <section className="dashboard-section" id="firewall">
  <div className="dashboard-section-heading"><div><p className="eyebrow">Default deny</p><h2>Firewall policy</h2></div></div>
  <div className="status-list"><article><div><strong>WAN input</strong><span>Unsolicited WAN input remains denied.</span></div><b>DENY</b></article><article><div><strong>State tracking</strong><span>Established and related traffic is accepted after security schedules.</span></div><b>{config.firewall.stateful_firewall ? "ON" : "INVALID"}</b></article><article><div><strong>Remote entry</strong><span>WireGuard is the only supported WAN entry point.</span></div><b>{config.firewall.wan_ingress_mode || "wireguard_only"}</b></article>    <article><div><strong>WAN port forwards</strong><span>The secure profile rejects enabled port forwards.</span></div><b>{(config.firewall.port_forwards || []).filter((item) => item.enabled).length}</b></article></div>
</section>}

{active === "qos" && <section className="dashboard-section" id="qos">
  <div className="dashboard-section-heading"><div><p className="eyebrow">Bufferbloat control</p><h2>QoS / Smart Queue Management</h2><p className="dns-filter-intro">Shapes WAN bandwidth with CAKE or FQ-CoDel to keep latency low under load. Applied to {config.wan.enabled ? "ppp0" : config.wan.interface || "eth0"}.</p></div><span className={`classic-status-chip ${config.qos.enabled ? "" : "is-off"}`}>QoS {config.qos.enabled ? "Active" : "Off"}</span></div>
  <div className="metric-grid compact">
    <article><span>Algorithm</span><strong>{config.qos.algorithm}</strong><small>{config.qos.enabled ? "qdisc applied" : "inactive"}</small></article>
    <article><span>Download limit</span><strong>{config.qos.download_limit_mbps} Mbps</strong><small>ingress police</small></article>
    <article><span>Upload limit</span><strong>{config.qos.upload_limit_mbps} Mbps</strong><small>root qdisc rate</small></article>
  </div>
  <label className="checkbox-row"><input checked={config.qos.enabled} type="checkbox" onChange={(e) => toggleQoS(e.target.checked)} /><span>Enable QoS traffic shaping</span></label>
  <form className="settings-form" key={`qos-${config.revision}`} onSubmit={submitQoS}>
    <div className="form-grid two">
      <label className="field"><span>Algorithm</span><select defaultValue={config.qos.algorithm} name="algorithm"><option value="cake">CAKE (recommended)</option><option value="fq_codel">FQ-CoDel</option></select></label>
      <label className="field"><span>Download limit (Mbps)</span><input defaultValue={config.qos.download_limit_mbps} max="100000" min="1" name="download_limit_mbps" required type="number" /></label>
      <label className="field form-span"><span>Upload limit (Mbps)</span><input defaultValue={config.qos.upload_limit_mbps} max="100000" min="1" name="upload_limit_mbps" required type="number" /></label>
    </div>
    <p className="form-note">Set limits to ~90% of your measured WAN speed. Enabling QoS on a link with no congestion has little effect; measure latency before/after to confirm bufferbloat is reduced.</p>
    <div className="form-actions"><button className="button primary" disabled={busy} type="submit">Save settings</button></div>
  </form>

  {config.qos.enabled ? (
    <div className="speedtest-note" role="status">
      <strong>Speed test unavailable</strong>
      <span>Disable QoS first. An active shaper (currently {config.qos.download_limit_mbps}/{config.qos.upload_limit_mbps} Mbps) would report its own limit — not your real line speed — and suggested limits would be wrong.</span>
    </div>
  ) : (
    <div className="speedtest-block">
      <div className="speedtest-heading">
        <div>
          <strong>Measure your line speed</strong>
          <small>Run with QoS off, then apply the suggested 90% limits below.</small>
        </div>
        <button className="button secondary" disabled={busy || speedTesting} onClick={() => void runSpeedTest()} type="button">{speedTesting ? "Measuring…" : "Run speed test"}</button>
      </div>
      {speedTest && (
        <div className="metric-grid compact">
          <article><span>Measured download</span><strong>{speedTest.download_mbps.toFixed(1)} Mbps</strong><small>peak during test</small></article>
          <article><span>Measured upload</span><strong>{speedTest.upload_mbps.toFixed(1)} Mbps</strong><small>peak during test</small></article>
          <article><span>Suggested download</span><strong>{speedTest.suggested_download_mbps} Mbps</strong><small>90% of measured</small></article>
          <article><span>Suggested upload</span><strong>{speedTest.suggested_upload_mbps} Mbps</strong><small>90% of measured</small></article>
        </div>
      )}
    </div>
  )}
</section>}

{active === "wireguard" && <section className="dashboard-section" id="wireguard">
  <div className="dashboard-section-heading"><div><p className="eyebrow">Remote access</p><h2>WireGuard</h2></div><button className="button secondary" disabled={busy} onClick={() => void applyConfig((next) => { next.wireguard.enabled = !next.wireguard.enabled; }, `WireGuard ${config.wireguard.enabled ? "disabled" : "enabled"}.`)} type="button">{config.wireguard.enabled ? "Disable" : "Enable"}</button></div>
  <div className="metric-grid compact"><article><span>Interface</span><strong>{config.wireguard.interface}</strong></article><article><span>Listen port</span><strong>{config.wireguard.listen_port}</strong></article><article><span>Tunnel network</span><strong>{config.wireguard.address}</strong></article><article><span>Connected peers</span><strong>{runtime.wireguard_active_peers ?? 0} / {(config.wireguard.peers || []).filter((peer: WireGuardPeer) => peer.enabled).length}</strong></article></div>
  <div className="wg-status-card">
    <div className="wg-status-main">
      <span className={`wg-status-dot ${config.wireguard.enabled ? (runtime.wireguard_active_peers ? "is-connected" : "is-idle") : ""}`} aria-hidden="true" />
      <div>
        <h3>{config.wireguard.enabled ? (runtime.wireguard_active_peers ? "Interface up — clients reachable" : "Interface up — no clients connected") : "Interface disabled"}</h3>
        <p className="wg-status-host">wg0 · {config.wireguard.address} · UDP {config.wireguard.listen_port}</p>
      </div>
    </div>
    <dl className="wg-status-metrics">
      <div><dt>Peers online</dt><dd>{runtime.wireguard_active_peers ?? 0} of {(config.wireguard.peers || []).filter((peer: WireGuardPeer) => peer.enabled).length}</dd></div>
      <div><dt>Total received</dt><dd>{formatBytes(runtime.wireguard_peers?.reduce((sum, p) => sum + (p.rx_bytes || 0), 0) || 0)}</dd></div>
      <div><dt>Total sent</dt><dd>{formatBytes(runtime.wireguard_peers?.reduce((sum, p) => sum + (p.tx_bytes || 0), 0) || 0)}</dd></div>
    </dl>
  </div>
  <div className="elegant-table-container wg-table">
    <table className="elegant-device-table">
      <colgroup><col className="wg-col-status" /><col className="wg-col-peer" /><col className="wg-col-ip" /><col className="wg-col-endpoint" /><col className="wg-col-handshake" /><col className="wg-col-transfer" /><col className="wg-col-actions" /></colgroup>
      <thead>
        <tr>
          <th>Status</th>
          <th>Peer</th>
          <th>Allowed IPs</th>
          <th>Endpoint</th>
          <th>Last handshake</th>
          <th>Download / Upload</th>
          <th className="elegant-th-actions">Actions</th>
        </tr>
      </thead>
      <tbody>
        {(config.wireguard.peers || []).length === 0 ? (
          <tr><td className="elegant-empty" colSpan={7}><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round"><path d="M9 12l2 2 4-4" /><circle cx="12" cy="12" r="10" /></svg><span>No peers configured yet.</span></td></tr>
        ) : config.wireguard.peers.map((peer: WireGuardPeer) => {
          const live = runtime.wireguard_peers?.find((p) => p.public_key === peer.public_key);
          const online = live?.online || false;
          const handshake = live?.last_handshake_epoch;
          const endpoint = live?.endpoint || peer.endpoint;
          return (
            <tr key={peer.id}>
              <td className="wg-cell-status">
                <span className={`wg-status-icon ${online ? "is-online" : "is-offline"}`} role="img" aria-label={online ? "Connected" : "Disconnected"}>
                  <svg viewBox="0 0 24 24" aria-hidden="true">{online ? <><circle cx="12" cy="12" r="9" /><path d="M8 12.5l2.5 2.5L16 10" /></> : <><circle cx="12" cy="12" r="9" /><path d="M8.5 12h7" /></>}</svg>
                </span>
              </td>
              <td className="wg-cell-peer"><span className="wg-peer-name">{peer.name}</span><small className="wg-peer-key">{peer.public_key.slice(0, 18)}…</small></td>
              <td className="elegant-cell-ip">{live?.allowed_ips || (peer.allowed_ips || []).join(", ") || "—"}</td>
              <td className="wg-cell-endpoint">{endpoint || "—"}</td>
              <td className="wg-cell-handshake">{handshake ? formatHandshake(handshake) : "Never"}</td>
              <td className="wg-cell-transfer"><span className="wg-transfer-rx">↓ {formatBytes(live?.rx_bytes || 0)}</span><span className="wg-transfer-tx">↑ {formatBytes(live?.tx_bytes || 0)}</span></td>
              <td className="elegant-cell-actions">
                <button className="button secondary small" disabled={busy} onClick={() => void applyConfig((next) => {
                  const p = next.wireguard.peers?.find((x: WireGuardPeer) => x.id === peer.id);
                  if (p) p.enabled = !p.enabled;
                }, `Peer ${peer.name} ${peer.enabled ? "disabled" : "enabled"}.`)} type="button">
                  {peer.enabled ? "Disable" : "Enable"}
                </button>
                <button className="button danger small" disabled={busy} onClick={() => setConfirmDeletePeer({ id: peer.id, name: peer.name })} type="button" aria-label={`Delete peer ${peer.name}`} title={`Delete ${peer.name}`}>Delete</button>
              </td>
            </tr>
          );
        })}
      </tbody>
    </table>
  </div>

  {confirmDeletePeer && (
    <div className="modal-backdrop" onClick={() => setConfirmDeletePeer(null)}>
      <div className="modal destructive-modal" role="dialog" aria-modal="true" aria-labelledby="delete-peer-title" onClick={(e) => e.stopPropagation()}>
        <button className="modal-close" aria-label="Close" onClick={() => setConfirmDeletePeer(null)} type="button">✕</button>
        <h2 id="delete-peer-title">Delete {confirmDeletePeer.name}?</h2>
        <p className="modal-copy">This permanently removes the peer <strong>{confirmDeletePeer.name}</strong> from the WireGuard configuration. Its client config and QR code will no longer work, and this cannot be undone.</p>
        <div className="modal-actions">
          <button className="button secondary" onClick={() => setConfirmDeletePeer(null)} type="button">Cancel</button>
          <button className="button danger" disabled={busy} type="button" onClick={() => void applyConfig((next) => {
            next.wireguard.peers = (next.wireguard.peers || []).filter((x: WireGuardPeer) => x.id !== confirmDeletePeer.id);
          }, `Peer ${confirmDeletePeer.name} deleted.`).then(() => setConfirmDeletePeer(null))}>Delete</button>
        </div>
      </div>
    </div>
  )}

  {wgConfig ? (
    <div className="dashboard-callout wg-callout">
      {wgConfig.qr && (
        <div className="wg-qr">
          <img src={wgConfig.qr} alt="QR Code" />
        </div>
      )}
      <div>
        <strong className="wg-success-title">Success! Configuration generated for {wgConfig.name}.</strong>
        <p>WireGuard does not store your private key for security reasons. You MUST download this file or scan the QR code now, as it cannot be retrieved later.</p>
        <div className="wg-actions">
          <button className="button primary" onClick={() => {
            const blob = new Blob([wgConfig.config], { type: "text/plain" });
            const url = URL.createObjectURL(blob);
            const a = document.createElement("a");
            a.href = url;
            a.download = `${wgConfig.name.replace(/[^a-zA-Z0-9]/g, "_")}.conf`;
            a.click();
          }}>Download .conf file</button>
          <button className="button secondary" onClick={() => setWgConfig(null)}>Done</button>
        </div>
      </div>
    </div>
  ) : (
    <article className="card">
      <div className="card-title-row">
        <div>
          <h3>Add a new device</h3>
          <p>Generate a new WireGuard configuration to connect a laptop or phone.</p>
        </div>
      </div>
      <form className="settings-form" onSubmit={handleAddPeer}>
        <div className="form-grid two">
          <label className="field"><span>Device name</span><input name="name" placeholder="e.g. MacBook Air" required /></label>
          <label className="field"><span>Client IP Address</span><input name="client_ip_address" placeholder="10.6.0.4/32" required /></label>
          <label className="field form-span"><span>Server Endpoint (Domain:Port)</span><input name="server_endpoint" defaultValue={`${config.cloudflare.domain || runtime.public_ip || "1.2.3.4"}:${config.wireguard.listen_port}`} required /></label>
        </div>
        <div className="form-actions"><button className="button primary" disabled={addingPeer || busy} type="submit">{addingPeer ? "Generating..." : "Generate and Download"}</button></div>
      </form>
    </article>
  )}
</section>}

{active === "cloudflare" && <section className="dashboard-section" id="cloudflare">
  <div className="dashboard-section-heading ddns-heading">
    <div>
      <p className="eyebrow">Optional integration</p>
      <h2>Dynamic DNS</h2>
      <p className="ddns-lead">Keep a hostname pointing at your router, even when your public IP changes.</p>
    </div>
  </div>

  <div className="ddns-provider" role="tablist" aria-label="Dynamic DNS provider">
    <button type="button" className={ddnsTab === "noip" ? "is-active" : ""} role="tab" aria-selected={ddnsTab === "noip"} onClick={() => setDdnsTab("noip")}>No-IP</button>
    <button type="button" className={ddnsTab === "cloudflare" ? "is-active" : ""} role="tab" aria-selected={ddnsTab === "cloudflare"} onClick={() => setDdnsTab("cloudflare")}>Cloudflare</button>
  </div>

  {config.cloudflare.ddns_enabled ? (
    <article className="ddns-status">
      <div className="ddns-status-main">
        <span className={`ddns-status-dot ${runtime.ddns?.running ? "is-connected" : "is-starting"}`} aria-hidden="true" />
        <div>
          <h3>{runtime.ddns?.running ? "Connected" : "Starting…"}</h3>
          <p className="ddns-status-host">{runtime.ddns?.hostname || config.cloudflare.domain || "Hostname not configured"}</p>
        </div>
      </div>
      <dl className="ddns-status-metrics">
        <div><dt>Provider</dt><dd>{ddnsTab === "noip" ? "No-IP" : "Cloudflare"}</dd></div>
        <div><dt>Registered IP</dt><dd className="ddns-mono">{runtime.ddns?.last_ip || "—"}</dd></div>
        <div><dt>Last update</dt><dd>{runtime.ddns?.last_update_epoch ? new Date(runtime.ddns.last_update_epoch * 1000).toLocaleString() : "Never"}</dd></div>
        <div><dt>Refresh</dt><dd>Every 5 minutes</dd></div>
      </dl>
    </article>
  ) : (
    <article className="ddns-status ddns-status-off">
      <div className="ddns-status-main">
        <span className="ddns-status-dot" aria-hidden="true" />
        <div>
          <h3>Disabled</h3>
          <p className="ddns-status-host">Dynamic DNS is turned off for {ddnsTab === "noip" ? "No-IP" : "Cloudflare"}. Enable it below to keep your hostname in sync.</p>
        </div>
      </div>
    </article>
  )}

  <form className="settings-form ddns-form" key={`ddns-${config.revision}-${ddnsTab}`} onSubmit={submitCloudflare}>
    <input type="hidden" name="provider" value={ddnsTab} />
    <label className="checkbox-row"><input checked={config.cloudflare.ddns_enabled} type="checkbox" onChange={(e) => toggleCloudflare(e.target.checked)} /><span>Enable {ddnsTab === "noip" ? "No-IP" : "Cloudflare"} Dynamic DNS</span></label>

    {ddnsTab === "noip" ? (
      <div className="form-grid two">
        <label className="field"><span>Hostname / update target</span><input defaultValue={config.cloudflare.domain || "homelab.redirectme.net"} name="domain" placeholder="homelab.redirectme.net" /></label>
        <label className="field"><span>No-IP username / DDNS Key username</span><input autoComplete="username" defaultValue={config.cloudflare.ddns_username || "vladimir.perovic@gmail.com"} name="username" /></label>
        <label className="field form-span"><span>Provider credential</span><input autoComplete="new-password" name="credential" placeholder="Configured — leave blank to keep" type="password" /></label>
      </div>
    ) : (
      <div className="form-grid two">
        <label className="field"><span>Hostname / update target</span><input defaultValue={config.cloudflare.domain} name="domain" placeholder="router.example.com" /></label>
        <label className="field"><span>Cloudflare zone</span><input defaultValue={config.cloudflare.zone_name} name="zone" placeholder="example.com" /></label>
        <label className="field form-span"><span>API token</span><input autoComplete="new-password" name="credential" placeholder="Configured — leave blank to keep" type="password" /></label>
      </div>
    )}

    <p className="form-note">
      {ddnsTab === "noip"
        ? "No-IP free hostnames must receive an update at least every 30 days. Changes are applied immediately and kept in sync automatically."
        : "Cloudflare requires a Zone name and an API token with Edit DNS permission."}
    </p>
    <div className="form-actions"><button className="button primary" disabled={busy} type="submit">Save settings</button></div>
  </form>
</section>}

{active === "squid" && <section className="dashboard-section" id="squid"><div className="dashboard-section-heading"><div><p className="eyebrow">Optional</p><h2>Squid forward proxy</h2></div></div><label className="checkbox-row"><input checked={config.squid_proxy.enabled} type="checkbox" onChange={(e) => toggleSquid(e.target.checked)} /><span>Enable non-caching proxy</span></label><form className="settings-form" key={`squid-${config.revision}`} onSubmit={submitSquid}><div className="form-grid two"><label className="field"><span>Port</span><input defaultValue={config.squid_proxy.port} name="port" type="number" /></label><label className="field"><span>Username</span><input defaultValue={config.squid_proxy.username} name="username" /></label><label className="field form-span"><span>New password</span><input autoComplete="new-password" name="password" placeholder="Leave blank to keep stored secret" type="password" /></label></div><div className="form-actions"><button className="button primary" disabled={busy} type="submit">Save settings</button></div></form></section>}

{active === "dns-filter" && <DNSFilterPanel apiConnected onError={setError} />}

{active === "wifi" && <section className="dashboard-section" id="wifi"><div className="dashboard-section-heading"><div><p className="eyebrow">Optional hardware</p><h2>Wi-Fi access point</h2></div></div><label className="checkbox-row"><input checked={config.wifi.enabled} type="checkbox" onChange={(e) => toggleWiFi(e.target.checked)} /><span>Enable access point</span></label><form className="settings-form" key={`wifi-${config.revision}`} onSubmit={submitWiFi}><div className="form-grid two"><label className="field"><span>Radio interface</span><input defaultValue={config.wifi.interface} name="interface" /></label><label className="field"><span>SSID</span><input defaultValue={config.wifi.ssid} name="ssid" /></label><label className="field"><span>Band</span><select defaultValue={config.wifi.band} name="band"><option value="2.4ghz">2.4 GHz</option><option value="5ghz">5 GHz</option></select></label><label className="field"><span>Channel</span><input defaultValue={config.wifi.channel} name="channel" type="number" /></label><label className="field form-span"><span>New passphrase</span><input autoComplete="new-password" name="passphrase" placeholder="Leave blank to keep stored secret" type="password" /></label></div><label className="checkbox-row"><input defaultChecked={config.wifi.hide_ssid} name="hide_ssid" type="checkbox" /><span>Hide SSID</span></label><div className="form-actions"><button className="button primary" disabled={busy} type="submit">Save settings</button></div></form></section>}

{active === "recovery" && <section className="dashboard-section" id="recovery"><div className="dashboard-section-heading"><div><p className="eyebrow">Recoverability</p><h2>Snapshots and local console</h2></div><button className="button primary" disabled={busy} onClick={() => void createSnapshot()} type="button">Create snapshot</button></div><div className="dashboard-callout"><strong>Network recovery is intentionally unavailable.</strong><p>Password/TOTP reset, LAN repair, snapshot recovery, and factory reset use <code>router-recovery</code> on the local console.</p></div><article className="card table-card"><div className="table-scroll"><table><thead><tr><th>Created</th><th>Revision</th><th>Checksum</th><th>Action</th></tr></thead><tbody>{snapshots.length === 0 ? <tr><td className="empty-state" colSpan={4}>No snapshots yet.</td></tr> : snapshots.map((snapshot) => <tr key={snapshot.id}><td>{new Date(snapshot.created_at).toLocaleString()}</td><td>{snapshot.revision}</td><td><code>{snapshot.checksum.slice(0, 16)}…</code></td><td><button className="button secondary small" disabled={busy} onClick={() => void restoreSnapshot(snapshot.id)} type="button">Restore</button></td></tr>)}</tbody></table></div></article></section>}

{active === "security" && <section className="dashboard-section" id="security"><div className="dashboard-section-heading"><div><p className="eyebrow">Administrator</p><h2>Security settings</h2></div></div><form className="settings-form narrow" onSubmit={changePassword}><div className="form-grid"><label className="field"><span>Current password</span><input autoComplete="current-password" name="old_password" required type="password" /></label><label className="field"><span>New password</span><input autoComplete="new-password" minLength={15} name="new_password" required type="password" /></label><label className="field"><span>Confirm new password</span><input autoComplete="new-password" minLength={15} name="confirm_password" required type="password" /></label></div><p className="form-note">Changing the password revokes every session. TOTP enrollment remains available through the authenticated API; lost TOTP recovery is local-console only.</p><div className="form-actions"><button className="button primary" disabled={busy} type="submit">Change password</button></div></form></section>}
{active === "logs" && <AuditLogPanel />}
  </>;
}
