import React, { useState } from "react";
import type { FormEvent } from "react";
import { apiFetch } from "../lib/api";
import DNSFilterPanel from "./DNSFilterPanel";
import AuditLogPanel from "./AuditLogPanel";
import GatewayQualityPanel from "./GatewayQualityPanel";
import DeviceLeasesTable from "./DeviceLeasesTable";
import StaticLeasesEditor from "./StaticLeasesEditor";
import FirewallRulesEditor from "./FirewallRulesEditor";
import TrafficPanel from "./TrafficPanel";
import type { GatewaySettings, GatewaySummary, RouterConfig, Snapshot, SystemStatus, WireGuardPeer } from "../api-types";
import "./DNSFilterPanel.css";

export type SectionID = "overview" | "gateway" | "network" | "firewall" | "qos" | "wireguard" | "cloudflare" | "squid" | "dns-filter" | "wifi" | "recovery" | "security" | "logs" | "traffic";

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
  gatewaySummary: GatewaySummary | null;
  gatewaySettings: GatewaySettings;
  runtime: Runtime;
  leases: NonNullable<Runtime["dhcp_leases"]>;
  snapshots: Snapshot[];
  busy: boolean;
  load: () => Promise<void>;
  applyConfig: ApplyConfig;
  applyGatewayMonitoring: (settings: GatewaySettings) => void;
  submitNetwork: (event: FormEvent<HTMLFormElement>) => void;
  submitCloudflare: (event: FormEvent<HTMLFormElement>) => void;
  submitSquid: (event: FormEvent<HTMLFormElement>) => void;
  submitWiFi: (event: FormEvent<HTMLFormElement>) => void;
  submitQoS: (event: FormEvent<HTMLFormElement>) => void;
  submitWireGuardClient: (event: FormEvent<HTMLFormElement>) => void;
  runSpeedTest: () => Promise<void>;
  toggleQoS: (enabled: boolean) => void;
  toggleWAN: (enabled: boolean) => void;
  toggleDHCP: (enabled: boolean) => void;
  toggleCloudflare: (enabled: boolean) => void;
  toggleSquid: (enabled: boolean) => void;
  toggleWiFi: (enabled: boolean) => void;
  toggleWGClient: (enabled: boolean) => void;
  speedTest: SpeedTestResult | null;
  speedTesting: boolean;
  createSnapshot: () => Promise<void>;
  restoreSnapshot: (id: string) => Promise<void>;
  deleteSnapshot: (id: string) => Promise<void>;
  setError: (message: string) => void;
  onNavigate: (id: SectionID) => void;
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

function formatHandshake(epoch: number) {
  const diff = Math.max(0, Date.now() / 1000 - epoch);
  if (diff < 60) return "Just now";
  const minutes = Math.floor(diff / 60);
  if (minutes < 60) return `${minutes} min ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours} h ago`;
  return new Date(epoch * 1000).toLocaleString();
}

// The backend owns allocation (next free IP, DDNS endpoint). The UI only
// displays what the authoritative /provisioning-preview endpoint reports.
function endpointFor(config: { cloudflare: { domain: string }; wireguard: { listen_port: number } }) {
  const host = config.cloudflare.domain?.trim();
  return host ? `${host}:${config.wireguard.listen_port}` : "Configure Dynamic DNS first";
}

type WireGuardClientArtifact = {
  peerId?: string;
  name: string;
  config: string;
  qr?: string;
};

async function apiErrorMessage(response: Response, fallback: string) {
  const text = await response.text().catch(() => "");
  if (!text) return fallback;
  try {
    const body = JSON.parse(text) as { error?: string };
    return body.error || fallback;
  } catch {
    return text.trim() || fallback;
  }
}

function downloadWireGuardConfig(artifact: WireGuardClientArtifact) {
  const blob = new Blob([artifact.config], { type: "text/plain" });
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = `${artifact.name.replace(/[^a-zA-Z0-9]/g, "_")}.conf`;
  anchor.click();
  URL.revokeObjectURL(url);
}

type WGClientConfigShape = {
  enabled: boolean;
  endpoint: string;
  public_key: string;
  preshared_key?: string;
  address: string;
  allowed_ips: string[];
  persistent_keepalive: number;
  private_key?: string;
};

type WGClientRuntimeShape = {
  endpoint?: string;
  last_handshake_epoch?: number;
  rx_bytes?: number;
  tx_bytes?: number;
  online: boolean;
};

// WGClientPanel configures the outbound tunnel (wg1) used to reach a remote
// site such as an office network. The remote peer can only reply: the router
// never accepts an initiation from this interface.
function WGClientPanel({
  cfg, runtime, busy, onSubmit, onToggle, onError,
}: {
  cfg: WGClientConfigShape;
  runtime: WGClientRuntimeShape | undefined;
  busy: boolean;
  onSubmit: (event: FormEvent<HTMLFormElement>) => void;
  onToggle: (enabled: boolean) => void;
  onError: (message: string) => void;
}) {
  const [keys, setKeys] = useState<{ private_key: string; public_key: string } | null>(null);
  const [publicKey, setPublicKey] = useState(cfg.public_key || "");
  const [privateKey, setPrivateKey] = useState("");
  const [generating, setGenerating] = useState(false);
  const [copied, setCopied] = useState(false);

  const generateKeys = async () => {
    setGenerating(true);
    try {
      const res = await apiFetch("/api/v1/wireguard/client/keys", { method: "POST" });
      const body = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(body.error || `Key generation failed (${res.status})`);
      setKeys(body);
      // The local public key is shown and copied for the remote side, but it
      // must never overwrite the Remote public key field, which holds the
      // remote peer's key.
      setPrivateKey(body.private_key);
      if (navigator.clipboard?.writeText) {
        navigator.clipboard.writeText(body.public_key).then(
          () => {
            setCopied(true);
            setTimeout(() => setCopied(false), 2000);
          },
          () => {},
        );
      }
    } catch (e) {
      onError(e instanceof Error ? e.message : "Key generation failed");
    } finally {
      setGenerating(false);
    }
  };

  const online = runtime?.online || false;
  const handshake = runtime?.last_handshake_epoch;

  return (
    <article className="card wg-client-card">
      <div className="wg-client-head">
        <div className="wg-client-title">
          <span className="wg-client-ico" aria-hidden="true">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"><path d="M12 3a4 4 0 0 1 2.83 6.83c-1.1.9-1.83 2.2-1.83 3.67V14" /><path d="M12 20a1.5 1.5 0 1 0 0-3 1.5 1.5 0 0 0 0 3z" /><path d="M19 4l2 2-2 2" /><path d="M21 6h-6" /><path d="M5 20l-2-2 2-2" /><path d="M3 18h6" /></svg>
          </span>
          <div>
            <h3>Outbound client tunnel</h3>
            <p>Reach a remote site — the office can only respond, never reach back into your home.</p>
          </div>
        </div>
        <label className="checkbox-row wg-client-toggle"><input checked={cfg.enabled} type="checkbox" onChange={(e) => onToggle(e.target.checked)} /><span>{cfg.enabled ? "Enabled" : "Disabled"}</span></label>
      </div>

      {cfg.enabled && (
        <div className={`wg-status-card wg-client-status ${online ? "is-online" : ""}`}>
          <div className="wg-status-main">
            <span className={`wg-status-dot ${online ? "is-connected" : "is-idle"}`} aria-hidden="true" />
            <div>
              <h3>{online ? "Tunnel connected — remote site reachable" : "Interface up — waiting for handshake"}</h3>
              <p className="wg-status-host">{runtime?.endpoint || cfg.endpoint || "endpoint not configured"} · {cfg.address || "wg1"}</p>
            </div>
          </div>
          <dl className="wg-status-metrics">
            <div><dt>Last handshake</dt><dd>{handshake ? formatHandshake(handshake) : "Never"}</dd></div>
            <div><dt>Total received</dt><dd>{formatBytes(runtime?.rx_bytes || 0)}</dd></div>
            <div><dt>Total sent</dt><dd>{formatBytes(runtime?.tx_bytes || 0)}</dd></div>
          </dl>
        </div>
      )}

      <form className="settings-form" key={`wg-client-${cfg.public_key}-${cfg.endpoint}`} onSubmit={onSubmit}>
        <div className="form-grid two">
          <label className="field form-span"><span>Remote endpoint</span><input defaultValue={cfg.endpoint} name="client_endpoint" placeholder="office.example.com:51820" required /></label>
          <label className="field form-span">
            <span>Remote public key</span>
            <input className={!publicKey || publicKey.length === 44 ? "" : "is-invalid"} name="client_public_key" onChange={(e) => setPublicKey(e.target.value)} placeholder="Paste the remote peer's public key" required value={publicKey} />
          </label>
          <label className="field"><span>Local tunnel address</span><input defaultValue={cfg.address} name="client_address" placeholder="10.7.0.2/32" required /></label>
          <label className="field"><span>Remote networks, comma separated</span><input defaultValue={(cfg.allowed_ips || []).join(", ")} name="client_allowed_ips" placeholder="10.7.0.0/24" required /></label>
          <label className="field"><span>Persistent keepalive (s)</span><input defaultValue={cfg.persistent_keepalive ?? 25} min="0" max="65535" name="client_keepalive" type="number" /></label>
          <label className="field"><span>Preshared key (optional)</span><input autoComplete="new-password" name="client_preshared_key" placeholder="Configured — leave blank to keep" type="password" /></label>
          <label className="field"><span>Local private key</span><input autoComplete="new-password" name="client_private_key" onChange={(e) => setPrivateKey(e.target.value)} placeholder={cfg.private_key ? "Configured — leave blank to keep" : "Generate a key pair below"} type="password" value={privateKey} /></label>
        </div>
        <div className="wg-client-keygen">
          <button className="button secondary" disabled={busy || generating} onClick={() => void generateKeys()} type="button">
            {generating ? "Generating…" : "Generate key pair"}
          </button>
          {keys && (
            <span className="wg-client-keygen-hint">
              {copied ? "Public key copied — paste it on the remote side." : "New key pair ready — add this public key on the remote peer:"}
            </span>
          )}
          {keys && <code className="wg-client-pubkey">{keys.public_key}</code>}
        </div>
        <p className="form-note">The remote site sees your traffic from the local tunnel address only, and it can never initiate a connection back into your home network — established replies are the only thing allowed through.</p>
        <div className="form-actions"><button className="button primary" disabled={busy} type="submit">Save client tunnel</button></div>
      </form>
    </article>
  );
}

const DNS_NAME_PATTERN = /^[a-zA-Z0-9]([a-zA-Z0-9.-]{0,251}[a-zA-Z0-9])?$/;
const DNS_IP_PATTERN = /^(\d{1,3}\.){3}\d{1,3}$/;

type DNSRecordRow = { name: string; ip: string };

// StaticDNSRecordsEditor renders the host-record table. Rows are plain inputs
// named dns_record_name_<i> / dns_record_ip_<i> so the parent form's
// submitNetwork picks them up; add/remove is local state until "Save".
// The key={config.revision} remount resets unsaved rows after every apply.
function StaticDNSRecordsEditor({ records, disabled }: { records: DNSRecordRow[]; disabled: boolean }) {
  const [rows, setRows] = useState<DNSRecordRow[]>(records.map((r) => ({ name: r.name || "", ip: r.ip || "" })));
  const update = (index: number, patch: Partial<DNSRecordRow>) =>
    setRows((rows) => rows.map((row, i) => (i === index ? { ...row, ...patch } : row)));
  return (
    <fieldset className="dns-records-fieldset" aria-labelledby="dns-records-title">
      <div className="fieldset-title" id="dns-records-title">Static DNS records</div>
      <p className="form-note">Names resolved by the router itself (host-record), useful for fixed devices and local services — e.g. <code>immich.home.arpa → 10.20.30.10</code>.</p>
      {rows.length === 0 ? (
        <div className="dns-records-empty">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" aria-hidden="true"><path d="M12 20a8 8 0 1 0 0-16 8 8 0 0 0 0 16z" /><path d="M12 8v5" /><path d="M12 16.5v.5" /></svg>
          <span>No static records yet — add one below.</span>
        </div>
      ) : (
        <div className="dns-records-list">
          {rows.map((row, i) => (
            <div className="dns-record-row" key={i}>
              <label className="field">
                <span>Name</span>
                <input
                  className={!row.name || DNS_NAME_PATTERN.test(row.name) ? "" : "is-invalid"}
                  name={`dns_record_name_${i}`}
                  onChange={(e) => update(i, { name: e.target.value })}
                  pattern="[a-zA-Z0-9]([a-zA-Z0-9.-]*[a-zA-Z0-9])?"
                  placeholder="immich.home.arpa"
                  title="Lowercase hostname, dots allowed"
                  value={row.name}
                />
              </label>
              <label className="field">
                <span>IPv4 address</span>
                <input
                  className={!row.ip || DNS_IP_PATTERN.test(row.ip) ? "" : "is-invalid"}
                  name={`dns_record_ip_${i}`}
                  onChange={(e) => update(i, { ip: e.target.value })}
                  pattern="(\d{1,3}\.){3}\d{1,3}"
                  placeholder="10.20.30.10"
                  title="IPv4 address, e.g. 10.20.30.10"
                  value={row.ip}
                />
              </label>
              <button
                className="dns-record-remove"
                disabled={disabled}
                onClick={() => setRows((rows) => rows.filter((_, idx) => idx !== i))}
                title={`Remove ${row.name || "record"}`}
                type="button"
                aria-label={`Remove ${row.name || "record"}`}
              >
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><path d="M3 6h18" /><path d="M8 6V4a1 1 0 0 1 1-1h6a1 1 0 0 1 1 1v2" /><path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6" /><path d="M10 11v6" /><path d="M14 11v6" /></svg>
              </button>
            </div>
          ))}
        </div>
      )}
      <button
        className="dns-record-add"
        disabled={disabled}
        onClick={() => setRows((rows) => [...rows, { name: "", ip: "" }])}
        type="button"
      >
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" aria-hidden="true"><path d="M12 5v14" /><path d="M5 12h14" /></svg>
        Add record
      </button>
    </fieldset>
  );
}

export default function DashboardSections({
  active, config, gatewaySummary, gatewaySettings, runtime, leases, snapshots, busy,
  load, applyConfig, applyGatewayMonitoring, submitNetwork, submitCloudflare, submitSquid,
  submitWiFi, submitQoS, submitWireGuardClient, runSpeedTest, toggleQoS, toggleWAN, toggleDHCP, toggleCloudflare, toggleSquid, toggleWiFi, toggleWGClient, speedTest, speedTesting, createSnapshot, restoreSnapshot, deleteSnapshot, setError, onNavigate }: Props) {
  const [staticPrefill, setStaticPrefill] = useState<{ mac?: string; ip?: string; hostname?: string } | null>(null);
  const [ddnsTab, setDdnsTab] = useState(config.cloudflare.ddns_provider || "noip");
  // The status card reports the provider the router is actually running, which
  // is not necessarily the tab the operator is looking at.
  const configuredProvider = config.cloudflare.ddns_provider || "noip";
  const configuredProviderLabel = configuredProvider === "noip" ? "No-IP" : "Cloudflare";
  const viewingOtherProvider = ddnsTab !== configuredProvider;
  const [wgConfig, setWgConfig] = useState<WireGuardClientArtifact | null>(null);
  const [addingPeer, setAddingPeer] = useState(false);
  const [confirmDeletePeer, setConfirmDeletePeer] = useState<{ id: string, name: string } | null>(null);
  const [peerActionID, setPeerActionID] = useState<string | null>(null);
  const [peerActionError, setPeerActionError] = useState("");
  const [renamingPeer, setRenamingPeer] = useState<{ id: string; name: string } | null>(null);
  const [wgPreview, setWgPreview] = useState<{ client_ip: string, server_endpoint: string } | null>(null);

  const submitPeerRename = () => {
    if (!renamingPeer) return;
    const name = renamingPeer.name.trim();
    applyConfig((next) => {
      const selected = next.wireguard.peers?.find((item: WireGuardPeer) => item.id === renamingPeer.id);
      if (selected && name) selected.name = name;
    }, `Peer renamed to ${name}.`);
    setRenamingPeer(null);
  };

  // Authoritative allocation preview from the backend (MR-AUD-005): the UI
  // never re-implements next-free-IP or endpoint resolution.
  React.useEffect(() => {
    let cancelled = false;
    const loadPreview = () => {
      apiFetch("/api/v1/wireguard/provisioning-preview")
        .then((res) => (res.ok ? res.json() : null))
        .then((body: { client_ip: string, server_endpoint: string } | null) => {
          if (!cancelled) setWgPreview(body);
        })
        .catch(() => { if (!cancelled) setWgPreview(null); });
    };
    loadPreview();
    const timer = window.setInterval(loadPreview, 15000);
    return () => { cancelled = true; window.clearInterval(timer); };
  }, [config.wireguard.peers?.length, config.cloudflare.domain, config.wireguard.address]);

  const handleAddPeer = async (e: React.FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    if (!config.cloudflare.domain?.trim()) {
      setError("Configure and save a Dynamic DNS hostname before adding a WireGuard device.");
      return;
    }
    setAddingPeer(true);
    try {
      const data = new FormData(e.currentTarget);
      const res = await apiFetch("/api/v1/wireguard/peers", {
        method: "POST",
        body: JSON.stringify({
          name: data.get("name")
        })
      });
      if (res.ok) {
        const body = await res.json();
        const artifact = {peerId: body.peer.id, name: body.peer.name, config: body.client_config, qr: body.qr_code_data};
        setWgConfig(artifact);
        downloadWireGuardConfig(artifact);
        if (body.tx?.state === "AwaitingConfirmation") void load();
      } else {
        setError(await apiErrorMessage(res, "Failed to add peer"));
      }
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : "Failed to add peer");
    } finally {
      setAddingPeer(false);
    }
  };

  const showWireGuardArtifact = (artifact: WireGuardClientArtifact) => {
    setWgConfig(artifact);
    window.requestAnimationFrame(() => document.getElementById("wg-client-artifact")?.scrollIntoView({ behavior: "smooth", block: "center" }));
  };

  const handlePeerConfiguration = async (peer: WireGuardPeer, download: boolean) => {
    if (wgConfig?.peerId === peer.id) {
      if (download) downloadWireGuardConfig(wgConfig);
      else showWireGuardArtifact(wgConfig);
      return;
    }
    if (!window.confirm(`Generate a new configuration for ${peer.name}? The previous configuration for this peer will stop working.`)) return;
    setPeerActionID(peer.id);
    setError("");
    try {
      const response = await apiFetch(`/api/v1/wireguard/peers/${encodeURIComponent(peer.id)}/configuration`, {
        method: "POST",
        body: JSON.stringify({ server_endpoint: wgPreview?.server_endpoint || "" }),
      });
      if (!response.ok) throw new Error(await apiErrorMessage(response, "Failed to generate peer configuration"));
      const body = await response.json();
      const artifact = {peerId: body.peer.id, name: body.peer.name, config: body.client_config, qr: body.qr_code_data};
      showWireGuardArtifact(artifact);
      if (download) downloadWireGuardConfig(artifact);
      if (body.tx?.state === "AwaitingConfirmation") void load();
    } catch (error: unknown) {
      setError(error instanceof Error ? error.message : "Failed to generate peer configuration");
    } finally {
      setPeerActionID(null);
    }
  };

  const handleDeletePeer = async () => {
    if (!confirmDeletePeer) return;
    setPeerActionID(confirmDeletePeer.id);
    setPeerActionError("");
    setError("");
    try {
      const response = await apiFetch(`/api/v1/wireguard/peers/${encodeURIComponent(confirmDeletePeer.id)}`, { method: "DELETE" });
      if (!response.ok) throw new Error(await apiErrorMessage(response, "Failed to delete peer"));
      setConfirmDeletePeer(null);
      await load();
    } catch (error: unknown) {
      const message = error instanceof Error ? error.message : "Failed to delete peer";
      setPeerActionError(message);
      setError(message);
    } finally {
      setPeerActionID(null);
    }
  };

  const changeWAN = (enabled: boolean) => {
    if (!enabled && !window.confirm("Disable PPPoE WAN? This will disconnect Internet access until WAN is enabled again.")) return;
    toggleWAN(enabled);
  };

  const changeDHCP = (enabled: boolean) => {
    if (!enabled && !window.confirm("Disable the DHCP server? Devices may lose network access when their current leases expire.")) return;
    toggleDHCP(enabled);
  };

  return <>
{active === "overview" && <section className="dashboard-section overview-devices" id="overview-devices"><DeviceLeasesTable leases={leases} config={config} onAddStatic={(lease) => { setStaticPrefill({ mac: lease.mac, ip: lease.ip_address, hostname: lease.hostname }); onNavigate("network"); }} /></section>}

{active === "gateway" && <GatewayQualityPanel busy={busy} onApply={applyGatewayMonitoring} onError={setError} settings={gatewaySettings} summary={gatewaySummary} />}

{active === "network" && <section className="dashboard-section" id="network">
  <div className="dashboard-section-heading has-facts"><div className="subpage-hero-head"><div><p className="eyebrow">Connectivity</p><h2>WAN, LAN and DHCP</h2><p className="section-copy">Configure the uplink, local gateway, address allocation and local DNS records from one controlled network workspace.</p></div><span className={`classic-status-chip ${config.wan.enabled ? "" : "is-off"}`}>WAN {config.wan.enabled ? "Connected" : "Disabled"}</span></div><dl className="subpage-hero-facts"><div><dt>Uplink</dt><dd>{config.wan.enabled ? "PPPoE" : "Off"}</dd><small>{config.wan.interface || "No interface"}</small></div><div><dt>LAN gateway</dt><dd>{config.lan.ip_address}</dd><small>{config.lan.interface}</small></div><div><dt>DHCP</dt><dd>{config.dhcp.enabled ? "Active" : "Disabled"}</dd><small>{config.dhcp.range_start} – {config.dhcp.range_end}</small></div><div><dt>DNS resolvers</dt><dd>{config.dhcp.dns_servers?.length || 0}</dd><small>{config.dhcp.dns_enabled ? "local DNS enabled" : "upstream only"}</small></div></dl></div>
  <form className="settings-form" key={`network-${config.revision}`} onSubmit={submitNetwork}>
    <fieldset aria-labelledby="network-wan-title"><div className="fieldset-title" id="network-wan-title">WAN / PPPoE</div><label className="checkbox-row"><input checked={config.wan.enabled} type="checkbox" onChange={(e) => changeWAN(e.target.checked)} /><span>Enable PPPoE WAN</span></label><div className="form-grid two"><label className="field"><span>WAN interface</span><input defaultValue={config.wan.interface} name="wan_interface" required /></label><label className="field"><span>MTU</span><input defaultValue={config.wan.mtu} max="1500" min="1280" name="wan_mtu" type="number" /></label><label className="field"><span>PPPoE username</span><input defaultValue={config.wan.username} name="pppoe_username" /></label><label className="field"><span>New PPPoE password</span><input autoComplete="new-password" name="pppoe_password" placeholder="Leave blank to keep stored secret" type="password" /></label></div></fieldset>
    <fieldset aria-labelledby="network-lan-title"><div className="fieldset-title" id="network-lan-title">LAN interface</div><div className="form-grid three"><label className="field"><span>Interface</span><input defaultValue={config.lan.interface} name="lan_interface" required /></label><label className="field"><span>Gateway IPv4</span><input defaultValue={config.lan.ip_address} name="lan_ip" required /></label><label className="field"><span>Prefix</span><input defaultValue={`/${String(config.lan.cidr || "").split("/")[1] || "24"}`} disabled name="lan_prefix_display" title="Changing the LAN subnet requires the local recovery console" /></label></div></fieldset>
    <fieldset aria-labelledby="network-dhcp-title"><div className="fieldset-title" id="network-dhcp-title">DHCP server</div><label className="checkbox-row"><input checked={config.dhcp.enabled} type="checkbox" onChange={(e) => changeDHCP(e.target.checked)} /><span>Enable DHCP server</span></label><div className="form-grid three"><label className="field"><span>Range start</span><input defaultValue={config.dhcp.range_start} name="dhcp_start" required /></label><label className="field"><span>Range end</span><input defaultValue={config.dhcp.range_end} name="dhcp_end" required /></label><label className="field"><span>Lease time</span><input defaultValue={config.dhcp.lease_time} name="lease_time" required /></label></div></fieldset>
    <fieldset aria-labelledby="network-dns-title"><div className="fieldset-title" id="network-dns-title">DNS</div><div className="form-grid"><label className="field"><span>Upstream resolvers, comma separated</span><input defaultValue={(config.dhcp.dns_servers || []).join(", ")} name="dns_servers" required /></label></div></fieldset>
    <StaticDNSRecordsEditor disabled={busy} key={config.revision} records={config.dns?.records || []} />
    <div className="form-actions"><button className="button primary" disabled={busy} type="submit">Save settings</button></div>
  </form>
  <DeviceLeasesTable leases={leases} config={config} onAddStatic={(lease) => { setStaticPrefill({ mac: lease.mac, ip: lease.ip_address, hostname: lease.hostname }); onNavigate("network"); }} />
  <StaticLeasesEditor applyConfig={applyConfig} busy={busy} config={config} liveLeases={leases} prefill={staticPrefill} onPrefillConsumed={() => setStaticPrefill(null)} />
</section>}

{active === "firewall" && <section className="dashboard-section" id="firewall">
  <div className="dashboard-section-heading has-facts"><div className="subpage-hero-head"><div><p className="eyebrow">Default deny</p><h2>Firewall policy</h2><p className="section-copy">Review the enforced WAN posture, state tracking and the only paths permitted into the local network.</p></div><span className={`classic-status-chip ${config.firewall.stateful_firewall ? "" : "is-warning"}`}>{config.firewall.stateful_firewall ? "Policy enforced" : "Attention required"}</span></div><dl className="subpage-hero-facts"><div><dt>WAN input</dt><dd>Deny</dd><small>unsolicited traffic blocked</small></div><div><dt>State tracking</dt><dd>{config.firewall.stateful_firewall ? "On" : "Invalid"}</dd><small>established traffic tracked</small></div><div><dt>Remote entry</dt><dd>WireGuard</dd><small>{config.firewall.wan_ingress_mode || "wireguard_only"}</small></div><div><dt>Tunnel forwards</dt><dd>{(config.firewall.port_forwards || []).filter((item) => item.enabled).length}</dd><small>WAN remains closed</small></div></dl></div>
  <FirewallRulesEditor applyConfig={applyConfig} busy={busy} config={config} />
</section>}

{active === "traffic" && <TrafficPanel applyConfig={applyConfig} busy={busy} config={config} />}

{active === "qos" && <section className="dashboard-section" id="qos">
  <div className="dashboard-section-heading has-facts"><div className="subpage-hero-head"><div><p className="eyebrow">Bufferbloat control</p><h2>QoS / Smart Queue Management</h2><p className="dns-filter-intro">Shapes WAN bandwidth with CAKE or FQ-CoDel to keep latency low under load. Applied to {config.wan.enabled ? "ppp0" : config.wan.interface || "eth0"}.</p></div><span className={`classic-status-chip ${config.qos.enabled ? "" : "is-off"}`}>QoS {config.qos.enabled ? "Active" : "Off"}</span></div><dl className="subpage-hero-facts"><div><dt>Algorithm</dt><dd>{config.qos.algorithm}</dd><small>{config.qos.enabled ? "qdisc applied" : "inactive"}</small></div><div><dt>Download</dt><dd>{config.qos.download_limit_mbps} Mbps</dd><small>ingress limit</small></div><div><dt>Upload</dt><dd>{config.qos.upload_limit_mbps} Mbps</dd><small>egress limit</small></div><div><dt>Interface</dt><dd>{config.wan.enabled ? "ppp0" : config.wan.interface || "eth0"}</dd><small>shaping target</small></div></dl></div>
  <div className="qos-speedtest">
    <div className="qos-speedtest-head">
      <div><h4>Speed test</h4><p>Measures your real WAN speed and suggests QoS limits (90% of the result — the standard CAKE/SQM recommendation).</p></div>
      <button className="button secondary" disabled={busy || speedTesting} onClick={() => void runSpeedTest()} type="button">{speedTesting ? "Testing…" : "Test speed"}</button>
    </div>
    {speedTest && (
      <div className="qos-speedtest-result">
        <div className="qos-speedtest-measured"><small>Measured</small><b>{speedTest.download_mbps.toFixed(1)} Mbps ↓</b><span>{speedTest.upload_mbps.toFixed(1)} Mbps ↑</span></div>
        <div className="qos-speedtest-suggested"><small>Suggested for QoS</small><b>{speedTest.suggested_download_mbps.toFixed(1)} Mbps ↓</b><span>{speedTest.suggested_upload_mbps.toFixed(1)} Mbps ↑</span></div>
        <button
          className="button primary small"
          onClick={() => {
            const root = document.getElementById("qos");
            const down = root?.querySelector<HTMLInputElement>('input[name="download_limit_mbps"]');
            const up = root?.querySelector<HTMLInputElement>('input[name="upload_limit_mbps"]');
            const form = root?.querySelector<HTMLFormElement>("form.settings-form");
            if (down && up && form && speedTest) {
              down.value = String(speedTest.suggested_download_mbps);
              up.value = String(speedTest.suggested_upload_mbps);
              form.requestSubmit();
            }
          }}
          type="button"
        >Apply suggested</button>
      </div>
    )}
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

{active === "wireguard" && <section className="dashboard-section wireguard-workspace" id="wireguard">
  <section className="wg-page-hero" aria-labelledby="wg-page-title">
    <div className="wg-hero-heading">
      <div className="wg-hero-copy">
        <span className="wg-hero-icon" aria-hidden="true"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round"><path d="M10 13a5 5 0 0 0 7.54.54l2.5-2.5a5 5 0 0 0-7.07-7.07l-1.45 1.45" /><path d="M14 11a5 5 0 0 0-7.54-.54l-2.5 2.5a5 5 0 0 0 7.07 7.07l1.45-1.45" /></svg></span>
        <div><p className="eyebrow">Encrypted remote access</p><h2 id="wg-page-title">Private network access</h2><p>Manage devices that can securely reach this router and its local network.</p></div>
      </div>
      <div className="wg-hero-actions">
        <span className={`wg-interface-state ${config.wireguard.enabled ? "is-active" : ""}`}><i aria-hidden="true" />{config.wireguard.enabled ? "Interface active" : "Interface disabled"}</span>
        <button className="wg-interface-toggle" disabled={busy} onClick={() => void applyConfig((next) => { next.wireguard.enabled = !next.wireguard.enabled; }, `WireGuard ${config.wireguard.enabled ? "disabled" : "enabled"}.`)} type="button">{config.wireguard.enabled ? "Disable interface" : "Enable interface"}</button>
      </div>
    </div>
    <dl className="wg-hero-metrics">
      <div><dt>Interface</dt><dd>{config.wireguard.interface}</dd><small>{config.wireguard.address}</small></div>
      <div><dt>Listen port</dt><dd>{config.wireguard.listen_port}</dd><small>UDP</small></div>
      <div><dt>Peers online</dt><dd>{runtime.wireguard_active_peers ?? 0}<span> / {(config.wireguard.peers || []).filter((peer: WireGuardPeer) => peer.enabled).length}</span></dd><small>enabled devices</small></div>
      <div><dt>Traffic</dt><dd>{formatBytes(runtime.wireguard_peers?.reduce((sum, peer) => sum + (peer.rx_bytes || 0) + (peer.tx_bytes || 0), 0) || 0)}</dd><small>received and sent</small></div>
    </dl>
  </section>

  <section className="wg-peer-panel" aria-labelledby="wg-peers-title">
    <header className="wg-peer-panel-head">
      <div><h3 id="wg-peers-title">Remote devices</h3><p>Each peer has a dedicated tunnel address and its own access key.</p></div>
      <span>{(config.wireguard.peers || []).length} configured</span>
    </header>
    <div className="wg-peer-list">
      {(config.wireguard.peers || []).length === 0 ? (
        <div className="wg-peer-empty"><span aria-hidden="true"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round"><path d="M8 12h8M12 8v8" /><circle cx="12" cy="12" r="9" /></svg></span><div><strong>No remote devices yet</strong><p>Generate a configuration below to add the first peer.</p></div></div>
      ) : config.wireguard.peers.map((peer: WireGuardPeer) => {
        const live = runtime.wireguard_peers?.find((item) => item.public_key === peer.public_key);
        const online = Boolean(peer.enabled && live?.online);
        const handshake = live?.last_handshake_epoch;
        const endpoint = live?.endpoint || peer.endpoint;
        const peerState = !peer.enabled ? "is-disabled" : online ? "is-connected" : "is-waiting";
        return (
          <article className={`wg-peer-row ${peerState}`} key={peer.id}>
            <div className="wg-peer-identity">
              <span className="wg-peer-device" aria-hidden="true"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round"><rect x="4" y="3" width="16" height="18" rx="3" /><path d="M9 17h6M9 7h6" /></svg></span>
              <div>{renamingPeer?.id === peer.id ? <form onSubmit={(event) => { event.preventDefault(); submitPeerRename(); }} className="wg-peer-rename-form"><input aria-label="Peer name" autoFocus onChange={(event) => setRenamingPeer({ id: peer.id, name: event.target.value })} value={renamingPeer.name} /></form> : <strong>{peer.name}</strong>}<code>{peer.public_key.slice(0, 18)}…</code></div>
            </div>
            <div className="wg-peer-state"><span><i aria-hidden="true" />{!peer.enabled ? "Disabled" : online ? "Connected" : "Awaiting handshake"}</span><small>{handshake ? formatHandshake(handshake) : "Not connected yet"}</small></div>
            <dl className="wg-peer-details">
              <div><dt>Tunnel IP</dt><dd>{live?.allowed_ips || (peer.allowed_ips || []).join(", ") || "Not assigned"}</dd></div>
              <div><dt>Endpoint</dt><dd title={endpoint || "Endpoint not learned"}>{endpoint || "Not learned"}</dd></div>
              <div><dt>Transfer</dt><dd><span className="is-rx">↓ {formatBytes(live?.rx_bytes || 0)}</span><span>↑ {formatBytes(live?.tx_bytes || 0)}</span></dd></div>
            </dl>
            <div className="wg-peer-actions">
              {renamingPeer?.id === peer.id ? (
                <>
                  <button className="wg-peer-config" disabled={busy || peerActionID !== null || !renamingPeer.name.trim()} onClick={() => void submitPeerRename()} type="button">Save</button>
                  <button className="wg-peer-config" disabled={busy || peerActionID !== null} onClick={() => setRenamingPeer(null)} type="button">Cancel</button>
                </>
              ) : (
                <button className="wg-peer-config" disabled={busy || peerActionID !== null} onClick={() => setRenamingPeer({ id: peer.id, name: peer.name })} type="button">Rename</button>
              )}
              <button className="wg-peer-config" disabled={busy || peerActionID !== null} onClick={() => void handlePeerConfiguration(peer, false)} type="button">QR code</button>
              <button className="wg-peer-config" disabled={busy || peerActionID !== null} onClick={() => void handlePeerConfiguration(peer, true)} type="button">Download settings</button>
              <button className="wg-peer-toggle" disabled={busy || peerActionID !== null} onClick={() => void applyConfig((next) => {
                const selected = next.wireguard.peers?.find((item: WireGuardPeer) => item.id === peer.id);
                if (selected) selected.enabled = !selected.enabled;
              }, `Peer ${peer.name} ${peer.enabled ? "disabled" : "enabled"}.`)} type="button">{peer.enabled ? "Disable" : "Enable"}</button>
              <button className="wg-peer-delete" disabled={busy || peerActionID !== null} onClick={() => { setPeerActionError(""); setConfirmDeletePeer({ id: peer.id, name: peer.name }); }} type="button" aria-label={`Delete peer ${peer.name}`} title={`Delete ${peer.name}`}><svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round"><path d="M4 7h16M9 7V4h6v3M6.5 7l.7 13h9.6l.7-13M10 11v5M14 11v5" /></svg></button>
            </div>
          </article>
        );
      })}
    </div>
  </section>

  {confirmDeletePeer && (
    <div className="modal-backdrop" onClick={() => setConfirmDeletePeer(null)}>
      <div className="modal destructive-modal" role="dialog" aria-modal="true" aria-labelledby="delete-peer-title" onClick={(e) => e.stopPropagation()}>
        <button className="modal-close" aria-label="Close" onClick={() => setConfirmDeletePeer(null)} type="button">✕</button>
        <h2 id="delete-peer-title">Delete {confirmDeletePeer.name}?</h2>
        <p className="modal-copy">This permanently removes the peer <strong>{confirmDeletePeer.name}</strong> from the WireGuard configuration. Its client config and QR code will no longer work, and this cannot be undone.</p>
        {peerActionError && <p className="form-note is-error" role="alert">{peerActionError}</p>}
        <div className="modal-actions">
          <button className="button secondary" disabled={peerActionID !== null} onClick={() => setConfirmDeletePeer(null)} type="button">Cancel</button>
          <button className="button danger" disabled={busy || peerActionID !== null} type="button" onClick={() => void handleDeletePeer()}>{peerActionID === confirmDeletePeer.id ? "Deleting…" : "Delete"}</button>
        </div>
      </div>
    </div>
  )}

  {wgConfig ? (
    <div className="dashboard-callout wg-callout" id="wg-client-artifact">
      {wgConfig.qr && (
        <div className="wg-qr">
          <img src={wgConfig.qr} alt="QR Code" />
        </div>
      )}
      <div>
        <strong className="wg-success-title">Success! Configuration generated for {wgConfig.name}.</strong>
        <p>WireGuard does not store your private key for security reasons. You MUST download this file or scan the QR code now, as it cannot be retrieved later.</p>
        <div className="wg-actions">
          <button className="button primary" onClick={() => downloadWireGuardConfig(wgConfig)}>Download .conf file</button>
          <button className="button secondary" onClick={() => { setWgConfig(null); void load(); }}>Done</button>
        </div>
      </div>
    </div>
  ) : (
    <article className="card wg-provision-card" id="wg-add-peer">
      <div className="card-title-row">
        <div className="wg-provision-title"><span aria-hidden="true"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round"><path d="M12 5v14M5 12h14" /></svg></span><div><h3>Add a remote device</h3><p>Minimal Router assigns the next free tunnel IP and creates a ready-to-import configuration.</p></div></div>
      </div>
      <form className="settings-form" onSubmit={handleAddPeer}>
        <div className="wg-add-form">
          <div className="wg-add-input">
            <label className="field"><span>Device name</span><input name="name" placeholder="e.g. MacBook Air" required autoFocus /></label>
          </div>
          <div className="wg-add-summary">
            <div className="wg-add-assign">
              <span className="wg-assign-copy">Minimal Router will assign</span>
              <dl>
                <div><dt>Client IP</dt><dd><code>{wgPreview?.client_ip || "…"}</code></dd></div>
                <div><dt>Server endpoint</dt><dd><code>{wgPreview?.server_endpoint || endpointFor(config)}</code></dd></div>
              </dl>
            </div>
          </div>
        </div>
        {!config.cloudflare.domain?.trim() && <p className="form-note">Save a Dynamic DNS hostname first. The backend will not generate a remote client with a guessed or stale public IP.</p>}
        <div className="form-actions"><button className="button primary" disabled={addingPeer || busy || !config.cloudflare.domain?.trim()} type="submit">{addingPeer ? "Generating..." : "Generate and Download"}</button></div>
      </form>
    </article>
  )}

  <WGClientPanel
    busy={busy}
    cfg={config.wg_client}
    onError={setError}
    onSubmit={submitWireGuardClient}
    onToggle={toggleWGClient}
    runtime={runtime.wireguard_client}
  />
</section>}

{active === "cloudflare" && <section className="dashboard-section" id="cloudflare">
  <div className="dashboard-section-heading ddns-heading has-facts">
    <div className="subpage-hero-head"><div><p className="eyebrow">Optional integration</p><h2>Dynamic DNS</h2><p className="ddns-lead">Keep a hostname pointing at your router, even when your public IP changes.</p></div><span className={`classic-status-chip ${runtime.ddns?.running ? "" : "is-off"}`}>{runtime.ddns?.running ? "In sync" : config.cloudflare.ddns_enabled ? "Starting" : "Disabled"}</span></div>
    <dl className="subpage-hero-facts"><div><dt>Provider</dt><dd>{configuredProviderLabel}</dd><small>{config.cloudflare.ddns_enabled ? "updates enabled" : "updates disabled"}</small></div><div><dt>Hostname</dt><dd>{runtime.ddns?.hostname || config.cloudflare.domain || "Not set"}</dd><small>public update target</small></div><div><dt>Registered IP</dt><dd>{runtime.ddns?.last_ip || "—"}</dd><small>last observed address</small></div><div><dt>Last update</dt><dd>{runtime.ddns?.last_update_epoch ? new Date(runtime.ddns.last_update_epoch * 1000).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }) : "Never"}</dd><small>refresh every 5 minutes</small></div></dl>
  </div>

  <div className="ddns-provider" role="tablist" aria-label="Dynamic DNS provider">
    <button type="button" className={ddnsTab === "noip" ? "is-active" : ""} role="tab" aria-selected={ddnsTab === "noip"} onClick={() => setDdnsTab("noip")}>No-IP</button>
    <button type="button" className={ddnsTab === "cloudflare" ? "is-active" : ""} role="tab" aria-selected={ddnsTab === "cloudflare"} onClick={() => setDdnsTab("cloudflare")}>Cloudflare</button>
  </div>

  <form className="settings-form ddns-form" key={`ddns-${config.revision}-${ddnsTab}`} onSubmit={submitCloudflare}>
    <input type="hidden" name="provider" value={ddnsTab} />
    <label className="checkbox-row">
      <input
        checked={config.cloudflare.ddns_enabled}
        disabled={viewingOtherProvider && !config.cloudflare.ddns_enabled}
        onChange={(e) => toggleCloudflare(e.target.checked)}
        type="checkbox"
      />
      <span>Enable {configuredProviderLabel} Dynamic DNS</span>
    </label>
    {viewingOtherProvider && (
      <p className="form-note is-warning">
        {configuredProviderLabel} is the configured provider. Save this form to switch to {ddnsTab === "noip" ? "No-IP" : "Cloudflare"};
        the toggle above always applies to the provider that is currently configured.
      </p>
    )}

    {ddnsTab === "noip" ? (
      <div className="form-grid two">
        <label className="field"><span>Hostname / update target</span><input defaultValue={config.cloudflare.domain} name="domain" placeholder="router.example.net" /></label>
        <label className="field"><span>No-IP username / DDNS Key username</span><input autoComplete="username" defaultValue={config.cloudflare.ddns_username || ""} name="username" /></label>
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

{active === "squid" && <section className="dashboard-section" id="squid">
  <div className="dashboard-section-heading has-facts"><div className="subpage-hero-head"><div><p className="eyebrow">Optional service</p><h2>Squid forward proxy</h2><p className="section-copy">Provide authenticated, non-caching HTTP proxy access to trusted local clients without exposing the service to WAN.</p></div><span className={`classic-status-chip ${config.squid_proxy.enabled ? "" : "is-off"}`}>Proxy {config.squid_proxy.enabled ? "Active" : "Off"}</span></div><dl className="subpage-hero-facts"><div><dt>Service</dt><dd>{config.squid_proxy.enabled ? "Running" : "Disabled"}</dd><small>non-caching mode</small></div><div><dt>Listener</dt><dd>{config.squid_proxy.port}</dd><small>LAN only</small></div><div><dt>Identity</dt><dd>{config.squid_proxy.username}</dd><small>password protected</small></div><div><dt>Restricted clients</dt><dd>{config.squid_proxy.restricted_ips?.filter((item) => item.enabled).length || 0}</dd><small>trusted local devices</small></div></dl></div>
  <article className="service-config-card">
    <header className="service-config-header">
      <div className="service-config-title"><span className="service-config-icon" aria-hidden="true"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round"><path d="M4 7h16M6 7v10a3 3 0 0 0 3 3h6a3 3 0 0 0 3-3V7M9 4h6M9 12h6" /></svg></span><div><p>Service controls</p><h3>Authenticated proxy listener</h3><span>One compact workspace for access, identity and the local listening port.</span></div></div>
      <label className="service-toggle"><input checked={config.squid_proxy.enabled} type="checkbox" onChange={(e) => toggleSquid(e.target.checked)} /><span><b>{config.squid_proxy.enabled ? "Enabled" : "Disabled"}</b><small>Non-caching proxy</small></span></label>
    </header>
    <form className="settings-form" key={`squid-${config.revision}`} onSubmit={submitSquid}><div className="form-grid two"><label className="field"><span>Port</span><input defaultValue={config.squid_proxy.port} name="port" type="number" /></label><label className="field"><span>Username</span><input defaultValue={config.squid_proxy.username} name="username" /></label><label className="field form-span"><span>New password</span><input autoComplete="new-password" name="password" placeholder="Leave blank to keep stored secret" type="password" /></label></div><div className="form-actions"><button className="button primary" disabled={busy} type="submit">Save settings</button></div></form>
  </article>
</section>}

{active === "dns-filter" && <DNSFilterPanel apiConnected onError={setError} />}

{active === "wifi" && <section className="dashboard-section" id="wifi">
  <div className="dashboard-section-heading has-facts"><div className="subpage-hero-head"><div><p className="eyebrow">Optional hardware</p><h2>Wi-Fi access point</h2><p className="section-copy">Control the local radio, network identity and channel settings when compatible wireless hardware is installed.</p></div><span className={`classic-status-chip ${config.wifi.enabled ? "" : "is-off"}`}>Wi-Fi {config.wifi.enabled ? "Active" : "Off"}</span></div><dl className="subpage-hero-facts"><div><dt>Radio</dt><dd>{config.wifi.enabled ? "Active" : "Off"}</dd><small>{config.wifi.interface || "no interface"}</small></div><div><dt>Network</dt><dd>{config.wifi.ssid || "Not set"}</dd><small>{config.wifi.hide_ssid ? "hidden SSID" : "visible SSID"}</small></div><div><dt>Band</dt><dd>{config.wifi.band}</dd><small>wireless spectrum</small></div><div><dt>Channel</dt><dd>{config.wifi.channel}</dd><small>manual selection</small></div></dl></div>
  <article className="service-config-card">
    <header className="service-config-header">
      <div className="service-config-title"><span className="service-config-icon" aria-hidden="true"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round"><path d="M5 12.5a11 11 0 0 1 14 0M8.5 16a6 6 0 0 1 7 0M12 20h.01M2 9a16 16 0 0 1 20 0" /></svg></span><div><p>Radio controls</p><h3>Local wireless network</h3><span>Identity, spectrum and access policy in a single card.</span></div></div>
      <label className="service-toggle"><input checked={config.wifi.enabled} type="checkbox" onChange={(e) => toggleWiFi(e.target.checked)} /><span><b>{config.wifi.enabled ? "Enabled" : "Disabled"}</b><small>Access point</small></span></label>
    </header>
    <form className="settings-form" key={`wifi-${config.revision}`} onSubmit={submitWiFi}><div className="form-grid two"><label className="field"><span>Radio interface</span><input defaultValue={config.wifi.interface} name="interface" /></label><label className="field"><span>SSID</span><input defaultValue={config.wifi.ssid} name="ssid" /></label><label className="field"><span>Band</span><select defaultValue={config.wifi.band} name="band"><option value="2.4ghz">2.4 GHz</option><option value="5ghz">5 GHz</option></select></label><label className="field"><span>Channel</span><input defaultValue={config.wifi.channel} name="channel" type="number" /></label><label className="field form-span"><span>New passphrase</span><input autoComplete="new-password" name="passphrase" placeholder="Leave blank to keep stored secret" type="password" /></label></div><label className="checkbox-row"><input defaultChecked={config.wifi.hide_ssid} name="hide_ssid" type="checkbox" /><span>Hide SSID</span></label><div className="form-actions"><button className="button primary" disabled={busy} type="submit">Save settings</button></div></form>
  </article>
</section>}

{active === "recovery" && <section className="dashboard-section" id="recovery">
  <div className="dashboard-section-heading has-facts"><div className="subpage-hero-head"><div><p className="eyebrow">Recoverability</p><h2>Snapshots and local console</h2><p className="section-copy">Create verified configuration restore points and keep destructive recovery operations on the physical console.</p></div><button className="button primary" disabled={busy} onClick={() => void createSnapshot()} type="button">Create snapshot</button></div><dl className="subpage-hero-facts"><div><dt>Snapshots</dt><dd>{snapshots.length}</dd><small>verified restore points</small></div><div><dt>Current revision</dt><dd>{config.revision}</dd><small>active configuration</small></div><div><dt>Network recovery</dt><dd>Console only</dd><small>no remote endpoint</small></div><div><dt>Rollback</dt><dd>Automatic</dd><small>critical changes protected</small></div></dl></div>
  <div className="dashboard-callout"><strong>Network recovery is intentionally unavailable.</strong><p>Password/TOTP reset, LAN repair, snapshot recovery, and factory reset use <code>router-recovery</code> on the local console.</p></div>
  <article className="card table-card"><div className="card-title-row"><div><h3>Configuration snapshots</h3><p>Signed local restore points retained by the appliance.</p></div><span className="quiet-meta">{snapshots.length} available</span></div><div className="elegant-table-container"><table className="elegant-device-table"><colgroup><col className="elegant-col-expires" /><col className="elegant-col-w100" /><col /><col className="elegant-col-actions" /></colgroup><thead><tr><th>Created</th><th>Revision</th><th>Checksum</th><th className="elegant-th-actions">Action</th></tr></thead><tbody>{snapshots.length === 0 ? <tr><td className="empty-state" colSpan={4}>No snapshots yet.</td></tr> : snapshots.map((snapshot) => <tr key={snapshot.id}><td className="elegant-cell-data">{new Date(snapshot.created_at).toLocaleString()}</td><td>{snapshot.revision}</td><td className="elegant-cell-ip"><code>{snapshot.checksum.slice(0, 16)}…</code></td><td className="elegant-cell-actions"><div className="device-row-actions"><button className="button secondary small" disabled={busy} onClick={() => void restoreSnapshot(snapshot.id)} type="button">Restore</button><button className="button secondary small danger" disabled={busy} onClick={() => void deleteSnapshot(snapshot.id)} type="button">Delete</button></div></td></tr>)}</tbody></table></div></article>
</section>}

{active === "logs" && <AuditLogPanel />}
  </>;
}
