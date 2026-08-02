import React, { useState } from "react";
import type { FormEvent } from "react";
import { apiFetch } from "../lib/api";
import DNSFilterPanel from "./DNSFilterPanel";
import AuditLogPanel from "./AuditLogPanel";
import GatewayQualityPanel, { GatewayOverviewCard } from "./GatewayQualityPanel";
import type { GatewaySettings, GatewaySummary, RouterConfig, Snapshot, SystemStatus, WireGuardPeer } from "../api-types";
import "./DNSFilterPanel.css";

export type SectionID = "overview" | "gateway" | "network" | "firewall" | "wireguard" | "cloudflare" | "squid" | "dns-filter" | "wifi" | "recovery" | "security" | "logs";

type Runtime = NonNullable<SystemStatus["runtime"]>;
type ApplyConfig = (mutate: (next: RouterConfig) => void, success: string) => Promise<void>;

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
  logout: () => Promise<void>;
  applyConfig: ApplyConfig;
  applyGatewayMonitoring: (settings: GatewaySettings) => void;
  submitNetwork: (event: FormEvent<HTMLFormElement>) => void;
  submitCloudflare: (event: FormEvent<HTMLFormElement>) => void;
  submitSquid: (event: FormEvent<HTMLFormElement>) => void;
  submitWiFi: (event: FormEvent<HTMLFormElement>) => void;
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

export default function DashboardSections({
  active, config, system, gatewaySummary, gatewaySettings, runtime, memoryPercent, diskPercent, leases, snapshots, busy,
  lastRefresh, load, logout, applyConfig, applyGatewayMonitoring, submitNetwork, submitCloudflare, submitSquid,
  submitWiFi, createSnapshot, restoreSnapshot, changePassword, setError,
}: Props) {
  const [ddnsTab, setDdnsTab] = useState(config.cloudflare.ddns_provider || "noip");
  const [wgConfig, setWgConfig] = useState<{name: string, config: string, qr?: string} | null>(null);
  const [addingPeer, setAddingPeer] = useState(false);

  const handleAddPeer = async (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setAddingPeer(true);
    try {
      const data = new FormData(event.currentTarget);
      const response = await apiFetch("/api/v1/wireguard/peers", {
        method: "POST",
        body: JSON.stringify({
          name: data.get("name"),
          client_ip_address: data.get("client_ip_address"),
          server_endpoint: data.get("server_endpoint"),
        }),
      });
      if (response.ok) {
        const body = await response.json();
        setWgConfig({ name: body.peer.name, config: body.client_config, qr: body.qr_code_data });
        void load();
      } else {
        const errorBody = await response.json().catch(() => ({}));
        setError(errorBody.error || "Failed to add peer");
      }
    } catch (peerError) {
      setError(peerError instanceof Error ? peerError.message : "Failed to add peer");
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
    <fieldset><legend>WAN / PPPoE</legend><label className="checkbox-row"><input defaultChecked={config.wan.enabled} name="wan_enabled" type="checkbox" /><span>Enable PPPoE WAN</span></label><div className="form-grid two"><label className="field"><span>WAN interface</span><input defaultValue={config.wan.interface} name="wan_interface" required /></label><label className="field"><span>MTU</span><input defaultValue={config.wan.mtu} max="1500" min="1280" name="wan_mtu" type="number" /></label><label className="field"><span>PPPoE username</span><input defaultValue={config.wan.username} name="pppoe_username" /></label><label className="field"><span>New PPPoE password</span><input autoComplete="new-password" name="pppoe_password" placeholder="Leave blank to keep stored secret" type="password" /></label></div></fieldset>
    <fieldset><legend>LAN and DHCP</legend><div className="form-grid two"><label className="field"><span>LAN interface</span><input defaultValue={config.lan.interface} name="lan_interface" required /></label><label className="field"><span>Gateway IPv4</span><input defaultValue={config.lan.ip_address} name="lan_ip" required /></label><label className="field"><span>Prefix</span><select defaultValue={String(config.lan.cidr || "").split("/")[1] || "24"} name="lan_prefix"><option value="24">/24</option><option value="16">/16</option></select></label><label className="field"><span>Lease time</span><input defaultValue={config.dhcp.lease_time} name="lease_time" required /></label><label className="field"><span>DHCP start</span><input defaultValue={config.dhcp.range_start} name="dhcp_start" required /></label><label className="field"><span>DHCP end</span><input defaultValue={config.dhcp.range_end} name="dhcp_end" required /></label><label className="field form-span"><span>Upstream DNS, comma separated</span><input defaultValue={(config.dhcp.dns_servers || []).join(", ")} name="dns_servers" required /></label></div><label className="checkbox-row"><input defaultChecked={config.dhcp.enabled} name="dhcp_enabled" type="checkbox" /><span>Enable DHCP server</span></label></fieldset>
    <div className="form-actions"><button className="button primary" disabled={busy} type="submit">Apply network configuration</button></div>
  </form>
</section>}

{active === "firewall" && <section className="dashboard-section" id="firewall">
  <div className="dashboard-section-heading"><div><p className="eyebrow">Default deny</p><h2>Firewall policy</h2></div></div>
  <div className="status-list"><article><div><strong>WAN input</strong><span>Unsolicited WAN input remains denied.</span></div><b>DENY</b></article><article><div><strong>State tracking</strong><span>Established and related traffic is accepted after security schedules.</span></div><b>{config.firewall.stateful_firewall ? "ON" : "INVALID"}</b></article><article><div><strong>Remote entry</strong><span>WireGuard is the only supported WAN entry point.</span></div><b>{config.firewall.wan_ingress_mode || "wireguard_only"}</b></article><article><div><strong>WAN port forwards</strong><span>The secure profile rejects enabled port forwards.</span></div><b>{(config.firewall.port_forwards || []).filter((item) => item.enabled).length}</b></article></div>
</section>}

{active === "wireguard" && <section className="dashboard-section" id="wireguard">
  <div className="dashboard-section-heading"><div><p className="eyebrow">Remote access</p><h2>WireGuard</h2></div><button className="button secondary" disabled={busy} onClick={() => void applyConfig((next) => { next.wireguard.enabled = !next.wireguard.enabled; }, `WireGuard ${config.wireguard.enabled ? "disabled" : "enabled"}.`)} type="button">{config.wireguard.enabled ? "Disable" : "Enable"}</button></div>
  <div className="metric-grid compact"><article><span>Interface</span><strong>{config.wireguard.interface}</strong></article><article><span>Listen port</span><strong>{config.wireguard.listen_port}</strong></article><article><span>Tunnel network</span><strong>{config.wireguard.address}</strong></article><article><span>Enabled peers</span><strong>{(config.wireguard.peers || []).filter((peer: WireGuardPeer) => peer.enabled).length}</strong></article></div>
  <div className="wireguard-peer-cards">
    {(config.wireguard.peers || []).length === 0 ? (
      <article className="card"><div className="empty-state wireguard-empty">No peers configured.</div></article>
    ) : (
      config.wireguard.peers.map((peer: WireGuardPeer) => (
        <article className="card wireguard-peer-card" key={peer.id}>
          <div>
            <h3 className="wireguard-peer-title">
              <span className={peer.enabled ? "wireguard-peer-dot is-enabled" : "wireguard-peer-dot"} />
              {peer.name}
            </h3>
            <p className="wireguard-peer-meta">
              IP: <code>{(peer.allowed_ips || []).join(", ")}</code> • Endpoint: {peer.endpoint || "Dynamic"}
            </p>
          </div>
          <div className="wireguard-peer-actions">
            <p className="wireguard-key-preview">{peer.public_key.slice(0, 16)}…</p>
            <button className="button secondary small" disabled={busy} onClick={() => void applyConfig((next) => {
              const targetPeer = next.wireguard.peers?.find((item: WireGuardPeer) => item.id === peer.id);
              if (targetPeer) targetPeer.enabled = !targetPeer.enabled;
            }, `Peer ${peer.name} ${peer.enabled ? "disabled" : "enabled"}.`)} type="button">
              {peer.enabled ? "Disable" : "Enable"}
            </button>
          </div>
        </article>
      ))
    )}
  </div>

  {wgConfig ? (
    <div className="dashboard-callout wireguard-config-callout">
      {wgConfig.qr && (
        <div className="wireguard-qr-wrap">
          <img src={wgConfig.qr} alt="WireGuard configuration QR code" className="wireguard-qr" />
        </div>
      )}
      <div>
        <strong className="wireguard-config-title">Success! Configuration generated for {wgConfig.name}.</strong>
        <p>WireGuard does not store your private key for security reasons. Download this file or scan the QR code now, as it cannot be retrieved later.</p>
        <div className="wireguard-config-actions">
          <button className="button primary" onClick={() => {
            const blob = new Blob([wgConfig.config], { type: "text/plain" });
            const url = URL.createObjectURL(blob);
            const link = document.createElement("a");
            link.href = url;
            link.download = `${wgConfig.name.replace(/[^a-zA-Z0-9]/g, "_")}.conf`;
            link.click();
            URL.revokeObjectURL(url);
          }} type="button">Download .conf file</button>
          <button className="button secondary" onClick={() => setWgConfig(null)} type="button">Done</button>
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
      <form className="settings-form wireguard-add-form" onSubmit={handleAddPeer}>
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
  <div className="dashboard-section-heading"><div><p className="eyebrow">Optional</p><h2>Dynamic DNS</h2></div></div>
  <div className="ddns-tabs">
    <button className={`button ${ddnsTab === "noip" ? "primary" : "secondary"}`} type="button" onClick={() => setDdnsTab("noip")}>No-IP</button>
    <button className={`button ${ddnsTab === "cloudflare" ? "primary" : "secondary"}`} type="button" onClick={() => setDdnsTab("cloudflare")}>Cloudflare</button>
  </div>
  <form className="settings-form" key={`ddns-${config.revision}-${ddnsTab}`} onSubmit={submitCloudflare}>
    <input type="hidden" name="provider" value={ddnsTab} />
    <label className="checkbox-row"><input defaultChecked={config.cloudflare.ddns_enabled} name="enabled" type="checkbox" /><span>Enable Dynamic DNS ({ddnsTab === "noip" ? "No-IP" : "Cloudflare"})</span></label>
    {ddnsTab === "noip" ? (
      <div className="form-grid two">
        <label className="field"><span>Hostname / update target</span><input defaultValue={config.cloudflare.domain || "homelab.redirectme.net"} name="domain" placeholder="homelab.redirectme.net" /></label>
        <label className="field"><span>No-IP username / DDNS Key username</span><input autoComplete="username" defaultValue={config.cloudflare.ddns_username || ""} name="username" placeholder="No-IP username or DDNS key username" /></label>
        <label className="field form-span"><span>New provider credential (Password)</span><input autoComplete="new-password" name="credential" placeholder="Leave blank to keep stored secret" type="password" /></label>
      </div>
    ) : (
      <div className="form-grid two">
        <label className="field"><span>Hostname / update target</span><input defaultValue={config.cloudflare.domain} name="domain" placeholder="router.example.com" /></label>
        <label className="field"><span>Cloudflare zone</span><input defaultValue={config.cloudflare.zone_name} name="zone" placeholder="example.com" /></label>
        <label className="field form-span"><span>New API token</span><input autoComplete="new-password" name="credential" placeholder="Leave blank to keep stored secret" type="password" /></label>
      </div>
    )}
    <p className="form-note">{ddnsTab === "noip" ? "With a No-IP DDNS Key, use the generated key username/password." : "Cloudflare requires a Zone name and API token with Edit DNS permissions."}</p>
    <div className="form-actions"><button className="button primary" disabled={busy} type="submit">Apply Dynamic DNS</button></div>
  </form>
</section>}

{active === "squid" && <section className="dashboard-section" id="squid"><div className="dashboard-section-heading"><div><p className="eyebrow">Optional</p><h2>Squid forward proxy</h2></div></div><form className="settings-form" key={`squid-${config.revision}`} onSubmit={submitSquid}><label className="checkbox-row"><input defaultChecked={config.squid_proxy.enabled} name="enabled" type="checkbox" /><span>Enable non-caching proxy</span></label><div className="form-grid two"><label className="field"><span>Port</span><input defaultValue={config.squid_proxy.port} name="port" type="number" /></label><label className="field"><span>Username</span><input defaultValue={config.squid_proxy.username} name="username" /></label><label className="field form-span"><span>New password</span><input autoComplete="new-password" name="password" placeholder="Leave blank to keep stored secret" type="password" /></label></div><div className="form-actions"><button className="button primary" disabled={busy} type="submit">Apply proxy configuration</button></div></form></section>}

{active === "dns-filter" && <DNSFilterPanel apiConnected onError={setError} />}

{active === "wifi" && <section className="dashboard-section" id="wifi"><div className="dashboard-section-heading"><div><p className="eyebrow">Optional hardware</p><h2>Wi-Fi access point</h2></div></div><form className="settings-form" key={`wifi-${config.revision}`} onSubmit={submitWiFi}><label className="checkbox-row"><input defaultChecked={config.wifi.enabled} name="enabled" type="checkbox" /><span>Enable access point</span></label><div className="form-grid two"><label className="field"><span>Radio interface</span><input defaultValue={config.wifi.interface} name="interface" /></label><label className="field"><span>SSID</span><input defaultValue={config.wifi.ssid} name="ssid" /></label><label className="field"><span>Band</span><select defaultValue={config.wifi.band} name="band"><option value="2.4ghz">2.4 GHz</option><option value="5ghz">5 GHz</option></select></label><label className="field"><span>Channel</span><input defaultValue={config.wifi.channel} name="channel" type="number" /></label><label className="field form-span"><span>New passphrase</span><input autoComplete="new-password" name="passphrase" placeholder="Leave blank to keep stored secret" type="password" /></label></div><label className="checkbox-row"><input defaultChecked={config.wifi.hide_ssid} name="hide_ssid" type="checkbox" /><span>Hide SSID</span></label><div className="form-actions"><button className="button primary" disabled={busy} type="submit">Apply Wi-Fi configuration</button></div></form></section>}

{active === "recovery" && <section className="dashboard-section" id="recovery"><div className="dashboard-section-heading"><div><p className="eyebrow">Recoverability</p><h2>Snapshots and local console</h2></div><button className="button primary" disabled={busy} onClick={() => void createSnapshot()} type="button">Create snapshot</button></div><div className="dashboard-callout"><strong>Network recovery is intentionally unavailable.</strong><p>Password/TOTP reset, LAN repair, snapshot recovery, and factory reset use <code>router-recovery</code> on the local console.</p></div><article className="card table-card"><div className="table-scroll"><table><thead><tr><th>Created</th><th>Revision</th><th>Checksum</th><th>Action</th></tr></thead><tbody>{snapshots.length === 0 ? <tr><td className="empty-state" colSpan={4}>No snapshots yet.</td></tr> : snapshots.map((snapshot) => <tr key={snapshot.id}><td>{new Date(snapshot.created_at).toLocaleString()}</td><td>{snapshot.revision}</td><td><code>{snapshot.checksum.slice(0, 16)}…</code></td><td><button className="button secondary small" disabled={busy} onClick={() => void restoreSnapshot(snapshot.id)} type="button">Restore</button></td></tr>)}</tbody></table></div></article></section>}

{active === "security" && <section className="dashboard-section" id="security"><div className="dashboard-section-heading"><div><p className="eyebrow">Administrator</p><h2>Security settings</h2></div></div><form className="settings-form narrow" onSubmit={changePassword}><div className="form-grid"><label className="field"><span>Current password</span><input autoComplete="current-password" name="old_password" required type="password" /></label><label className="field"><span>New password</span><input autoComplete="new-password" minLength={15} name="new_password" required type="password" /></label><label className="field"><span>Confirm new password</span><input autoComplete="new-password" minLength={15} name="confirm_password" required type="password" /></label></div><p className="form-note">Changing the password revokes every session. TOTP enrollment remains available through the authenticated API; lost TOTP recovery is local-console only.</p><div className="form-actions"><button className="button primary" disabled={busy} type="submit">Change password</button><button className="button secondary" onClick={() => void logout()} type="button">Sign out</button></div></form></section>}
{active === "logs" && <AuditLogPanel />}
  </>;
}
