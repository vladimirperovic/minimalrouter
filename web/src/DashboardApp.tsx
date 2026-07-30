import { FormEvent, useCallback, useEffect, useMemo, useState } from "react";
import AuthGate from "./components/AuthGate";
import DNSFilterPanel from "./components/DNSFilterPanel";
import AuditLogPanel from "./components/AuditLogPanel";
import { apiFetch } from "./lib/api";
import "./components/DNSFilterPanel.css";
import "./DashboardApp.css";

type RouterConfig = Record<string, any>;
type Snapshot = { id: string; revision: number; created_at: string; checksum: string };
type SystemStatus = {
  status?: string;
  version?: string;
  wan_iface?: string;
  lan_ip?: string;
  revision?: number;
  update_trust_configured?: boolean;
  runtime?: {
    available?: boolean;
    os?: string;
    architecture?: string;
    wan_connected?: boolean;
    public_ip?: string;
    uptime_seconds?: number;
    cpu_count?: number;
    cpu_load_percent?: number;
    memory_used_bytes?: number;
    memory_total_bytes?: number;
    disk_used_bytes?: number;
    disk_total_bytes?: number;
    temperature_c?: number;
    dhcp_leases?: Array<{ expires_at: number; mac: string; ip_address: string; hostname?: string }>;
  };
};

type SectionID = "overview" | "network" | "firewall" | "wireguard" | "cloudflare" | "squid" | "dns-filter" | "wifi" | "recovery" | "security" | "logs";

const navigation: Array<[SectionID, string]> = [
  ["overview", "Overview"],
  ["network", "WAN, LAN & DHCP"],
  ["firewall", "Firewall"],
  ["wireguard", "WireGuard"],
  ["cloudflare", "Cloudflare DDNS"],
  ["squid", "Squid Proxy"],
  ["dns-filter", "DNS Filter"],
  ["wifi", "Wi-Fi AP"],
  ["recovery", "Recovery"],
  ["security", "Security"],
  ["logs", "Logs"],
];

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

function field(form: FormData, name: string) {
  return String(form.get(name) ?? "").trim();
}

function Dashboard() {
  const [active, setActive] = useState<SectionID>("overview");
  const [config, setConfig] = useState<RouterConfig | null>(null);
  const [system, setSystem] = useState<SystemStatus>({});
  const [snapshots, setSnapshots] = useState<Snapshot[]>([]);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [busy, setBusy] = useState(false);
  const [menuOpen, setMenuOpen] = useState(false);
  const [dark, setDark] = useState(false);
  const [pendingID, setPendingID] = useState("");

  const load = useCallback(async () => {
    try {
      const [configResponse, systemResponse, snapshotsResponse, pendingResponse] = await Promise.all([
        apiFetch("/api/v1/config"),
        apiFetch("/api/v1/system"),
        apiFetch("/api/v1/snapshots"),
        apiFetch("/api/v1/transactions/pending"),
      ]);
      if (!configResponse.ok) throw new Error(`Configuration unavailable (${configResponse.status})`);
      if (!systemResponse.ok) throw new Error(`System status unavailable (${systemResponse.status})`);
      setConfig(await configResponse.json());
      setSystem(await systemResponse.json());
      if (snapshotsResponse.ok) {
        const body = await snapshotsResponse.json();
        setSnapshots(Array.isArray(body) ? body : Array.isArray(body.snapshots) ? body.snapshots : []);
      }
      if (pendingResponse.ok) {
        const body = await pendingResponse.json();
        setPendingID(body?.id || "");
      }
      setError("");
    } catch (loadError) {
      setError(loadError instanceof Error ? loadError.message : "Router API unavailable");
    }
  }, []);

  useEffect(() => {
    void load();
    const timer = window.setInterval(() => void load(), 15000);
    return () => window.clearInterval(timer);
  }, [load]);

  useEffect(() => {
    document.documentElement.dataset.theme = dark ? "dark" : "light";
  }, [dark]);

  const applyConfig = async (mutate: (next: RouterConfig) => void, success: string) => {
    setBusy(true);
    setNotice("");
    setError("");
    try {
      const response = await apiFetch("/api/v1/config");
      if (!response.ok) throw new Error(`Configuration reload failed (${response.status})`);
      const next = await response.json();
      mutate(next);
      const applyResponse = await apiFetch("/api/v1/config", {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(next),
      });
      const body = await applyResponse.json().catch(() => ({}));
      if (!applyResponse.ok) throw new Error(body.error || `Apply failed (${applyResponse.status})`);
      if (body.state === "AwaitingConfirmation" && body.id) setPendingID(body.id);
      setNotice(body.state === "AwaitingConfirmation" ? "Promjena je privremeno aktivna i čeka potvrdu pristupa." : success);
      await load();
    } catch (applyError) {
      setError(applyError instanceof Error ? applyError.message : "Configuration apply failed");
    } finally {
      setBusy(false);
    }
  };

  const submitNetwork = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    void applyConfig((next) => {
      const wanEnabled = form.get("wan_enabled") === "on";
      next.wan = {
        ...next.wan,
        interface: field(form, "wan_interface"),
        enabled: wanEnabled,
        username: field(form, "pppoe_username"),
        password: field(form, "pppoe_password") || next.wan.password,
        mtu: Number(field(form, "wan_mtu")) || 1492,
      };
      next.lan = {
        ...next.lan,
        interface: field(form, "lan_interface"),
        ip_address: field(form, "lan_ip"),
        cidr: `${field(form, "lan_ip")}/${field(form, "lan_prefix") || "24"}`,
        netmask: field(form, "lan_prefix") === "16" ? "255.255.0.0" : "255.255.255.0",
      };
      next.dhcp = {
        ...next.dhcp,
        enabled: form.get("dhcp_enabled") === "on",
        range_start: field(form, "dhcp_start"),
        range_end: field(form, "dhcp_end"),
        lease_time: field(form, "lease_time"),
        dns_servers: field(form, "dns_servers").split(",").map((item) => item.trim()).filter(Boolean),
      };
    }, "Network configuration applied.");
  };

  const submitCloudflare = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    void applyConfig((next) => {
      next.cloudflare = {
        ...next.cloudflare,
        ddns_enabled: form.get("enabled") === "on",
        domain: field(form, "domain"),
        zone_name: field(form, "zone"),
        api_token: field(form, "token") || next.cloudflare.api_token,
        tunnel_enabled: false,
      };
    }, "Cloudflare DDNS configuration applied.");
  };

  const submitSquid = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    void applyConfig((next) => {
      next.squid_proxy = {
        ...next.squid_proxy,
        enabled: form.get("enabled") === "on",
        port: Number(field(form, "port")) || 3128,
        username: field(form, "username"),
        password: field(form, "password") || next.squid_proxy.password,
      };
    }, "Squid proxy configuration applied.");
  };

  const submitWiFi = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    void applyConfig((next) => {
      next.wifi = {
        ...next.wifi,
        enabled: form.get("enabled") === "on",
        interface: field(form, "interface"),
        ssid: field(form, "ssid"),
        passphrase: field(form, "passphrase") || next.wifi.passphrase,
        band: field(form, "band"),
        channel: Number(field(form, "channel")),
        hide_ssid: form.get("hide_ssid") === "on",
      };
    }, "Wi-Fi configuration applied.");
  };

  const confirmPending = async () => {
    if (!pendingID) return;
    setBusy(true);
    try {
      const response = await apiFetch(`/api/v1/transactions/${encodeURIComponent(pendingID)}/confirm`, { method: "POST" });
      if (!response.ok) throw new Error(`Confirmation failed (${response.status})`);
      setPendingID("");
      setNotice("Connectivity confirmed; configuration committed.");
      await load();
    } catch (confirmationError) {
      setError(confirmationError instanceof Error ? confirmationError.message : "Confirmation failed");
    } finally {
      setBusy(false);
    }
  };

  const createSnapshot = async () => {
    setBusy(true);
    try {
      const response = await apiFetch("/api/v1/snapshots", { method: "POST" });
      if (!response.ok) throw new Error(`Snapshot failed (${response.status})`);
      setNotice("Configuration snapshot created.");
      await load();
    } catch (snapshotError) {
      setError(snapshotError instanceof Error ? snapshotError.message : "Snapshot failed");
    } finally {
      setBusy(false);
    }
  };

  const restoreSnapshot = async (id: string) => {
    if (!window.confirm("Restore this snapshot? A current undo snapshot will be retained.")) return;
    setBusy(true);
    try {
      const response = await apiFetch(`/api/v1/snapshots/${encodeURIComponent(id)}/restore`, { method: "POST" });
      const body = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(body.error || `Restore failed (${response.status})`);
      if (body.state === "AwaitingConfirmation" && body.id) setPendingID(body.id);
      setNotice("Snapshot restore applied.");
      await load();
    } catch (restoreError) {
      setError(restoreError instanceof Error ? restoreError.message : "Restore failed");
    } finally {
      setBusy(false);
    }
  };

  const changePassword = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    const oldPassword = field(form, "old_password");
    const newPassword = field(form, "new_password");
    const confirm = field(form, "confirm_password");
    if (newPassword.length < 15 || newPassword !== confirm) {
      setError("Nova lozinka mora imati najmanje 15 karaktera i potvrda mora biti ista.");
      return;
    }
    setBusy(true);
    try {
      const response = await apiFetch("/api/v1/auth/change-password", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ old_password: oldPassword, new_password: newPassword }),
      });
      if (!response.ok) throw new Error(`Password change failed (${response.status})`);
      window.dispatchEvent(new Event("minimalrouter:unauthorized"));
    } catch (passwordError) {
      setError(passwordError instanceof Error ? passwordError.message : "Password change failed");
    } finally {
      setBusy(false);
    }
  };

  const logout = async () => {
    await apiFetch("/api/v1/auth/logout", { method: "POST" }).catch(() => undefined);
    window.dispatchEvent(new Event("minimalrouter:unauthorized"));
  };

  const runtime = system.runtime || {};
  const memoryPercent = runtime.memory_total_bytes ? Math.round(((runtime.memory_used_bytes || 0) / runtime.memory_total_bytes) * 100) : 0;
  const diskPercent = runtime.disk_total_bytes ? Math.round(((runtime.disk_used_bytes || 0) / runtime.disk_total_bytes) * 100) : 0;
  const leases = useMemo(() => runtime.dhcp_leases || [], [runtime.dhcp_leases]);

  if (!config) {
    return <main className="dashboard-loading"><p>{error || "Loading secure router state…"}</p><button className="button secondary" onClick={() => void load()} type="button">Retry</button></main>;
  }

  return (
    <div className="dashboard-app">
      <aside className={menuOpen ? "dashboard-sidebar is-open" : "dashboard-sidebar"}>
        <div className="dashboard-brand"><span aria-hidden="true">M</span><div><strong>Minimal Router</strong><small>{system.version || "Early alpha"}</small></div></div>
        <nav aria-label="Router sections">
          {navigation.map(([id, label], index) => (
            <a className={active === id ? "is-active" : ""} href={`#${id}`} key={id} onClick={() => { setActive(id); setMenuOpen(false); }}><span>{String(index + 1).padStart(2, "0")}</span>{label}</a>
          ))}
        </nav>
        <div className="dashboard-sidebar-actions">
          <button className="quiet-button" onClick={() => setDark((value) => !value)} type="button">{dark ? "Light mode" : "Dark mode"}</button>
          <button className="quiet-button" onClick={() => void logout()} type="button">Sign out</button>
        </div>
      </aside>

      <main className="dashboard-main">
        <header className="dashboard-topbar">
          <button aria-label="Open navigation" className="dashboard-menu" onClick={() => setMenuOpen((value) => !value)} type="button">☰</button>
          <div><p className="eyebrow">Minimal Router OS</p><h1>{navigation.find(([id]) => id === active)?.[1]}</h1></div>
          <div className="dashboard-health"><span className={runtime.wan_connected ? "health-dot is-online" : "health-dot"} />{runtime.wan_connected ? "WAN online" : "WAN offline"}</div>
        </header>

        {error && <div className="dashboard-alert is-error" role="alert">{error}<button aria-label="Dismiss error" onClick={() => setError("")} type="button">✕</button></div>}
        {notice && <div className="dashboard-alert is-success" role="status">{notice}<button aria-label="Dismiss notice" onClick={() => setNotice("")} type="button">✕</button></div>}
        {pendingID && <div className="dashboard-alert is-warning"><span>A connectivity-critical change is awaiting confirmation.</span><button className="button primary" disabled={busy} onClick={() => void confirmPending()} type="button">Confirm access</button></div>}

        {active === "overview" && <section className="dashboard-section" id="overview">
          <div className="dashboard-section-heading"><div><p className="eyebrow">Live status</p><h2>Router overview</h2></div><button className="button secondary" onClick={() => void load()} type="button">Refresh</button></div>
          <div className="metric-grid">
            <article><span>Uptime</span><strong>{formatUptime(runtime.uptime_seconds)}</strong><small>{runtime.os || "Runtime unavailable"}</small></article>
            <article><span>CPU</span><strong>{Math.round(runtime.cpu_load_percent || 0)}%</strong><small>{runtime.cpu_count || 0} logical cores</small></article>
            <article><span>Memory</span><strong>{memoryPercent}%</strong><small>{formatBytes(runtime.memory_used_bytes)} / {formatBytes(runtime.memory_total_bytes)}</small></article>
            <article><span>Disk</span><strong>{diskPercent}%</strong><small>{formatBytes(runtime.disk_used_bytes)} / {formatBytes(runtime.disk_total_bytes)}</small></article>
            <article><span>LAN</span><strong>{system.lan_ip || config.lan.ip_address}</strong><small>{config.lan.interface}</small></article>
            <article><span>Update trust</span><strong>{system.update_trust_configured ? "Pinned" : "Disabled"}</strong><small>{system.update_trust_configured ? "Ed25519 key installed" : "No signing key"}</small></article>
          </div>
          <article className="card table-card">
            <div className="card-title-row"><div><h3>Connected DHCP devices</h3><p>Runtime lease view; names and addresses stay local.</p></div><span className="quiet-meta">{leases.length} leases</span></div>
            <div className="table-scroll"><table><thead><tr><th>Host</th><th>IP</th><th>MAC</th><th>Expires</th></tr></thead><tbody>{leases.length === 0 ? <tr><td className="empty-state" colSpan={4}>No active leases reported.</td></tr> : leases.map((lease) => <tr key={`${lease.mac}-${lease.ip_address}`}><td>{lease.hostname || "Unknown"}</td><td><code>{lease.ip_address}</code></td><td><code>{lease.mac}</code></td><td>{new Date(lease.expires_at * 1000).toLocaleString()}</td></tr>)}</tbody></table></div>
          </article>
        </section>}

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
          <div className="status-list"><article><div><strong>WAN input</strong><span>Unsolicited WAN input remains denied.</span></div><b>DENY</b></article><article><div><strong>State tracking</strong><span>Established and related traffic is accepted after security schedules.</span></div><b>{config.firewall.stateful_firewall ? "ON" : "INVALID"}</b></article><article><div><strong>Remote entry</strong><span>WireGuard is the only supported WAN entry point.</span></div><b>{config.firewall.wan_ingress_mode || "wireguard_only"}</b></article><article><div><strong>WAN port forwards</strong><span>The secure profile rejects enabled port forwards.</span></div><b>{(config.firewall.port_forwards || []).filter((item: any) => item.enabled).length}</b></article></div>
        </section>}

        {active === "wireguard" && <section className="dashboard-section" id="wireguard">
          <div className="dashboard-section-heading"><div><p className="eyebrow">Remote access</p><h2>WireGuard</h2></div><button className="button secondary" disabled={busy} onClick={() => void applyConfig((next) => { next.wireguard.enabled = !next.wireguard.enabled; }, `WireGuard ${config.wireguard.enabled ? "disabled" : "enabled"}.`)} type="button">{config.wireguard.enabled ? "Disable" : "Enable"}</button></div>
          <div className="metric-grid compact"><article><span>Interface</span><strong>{config.wireguard.interface}</strong></article><article><span>Listen port</span><strong>{config.wireguard.listen_port}</strong></article><article><span>Tunnel network</span><strong>{config.wireguard.address}</strong></article><article><span>Enabled peers</span><strong>{(config.wireguard.peers || []).filter((peer: any) => peer.enabled).length}</strong></article></div>
          <article className="card table-card"><div className="table-scroll"><table><thead><tr><th>Name</th><th>Allowed IPs</th><th>Endpoint</th><th>Status</th></tr></thead><tbody>{(config.wireguard.peers || []).length === 0 ? <tr><td className="empty-state" colSpan={4}>No peers configured.</td></tr> : config.wireguard.peers.map((peer: any) => <tr key={peer.id}><td>{peer.name}</td><td><code>{(peer.allowed_ips || []).join(", ")}</code></td><td>{peer.endpoint || "Dynamic"}</td><td>{peer.enabled ? "Enabled" : "Disabled"}</td></tr>)}</tbody></table></div></article>
        </section>}

        {active === "cloudflare" && <section className="dashboard-section" id="cloudflare"><div className="dashboard-section-heading"><div><p className="eyebrow">Optional</p><h2>Cloudflare Dynamic DNS</h2></div></div><form className="settings-form" key={`cf-${config.revision}`} onSubmit={submitCloudflare}><label className="checkbox-row"><input defaultChecked={config.cloudflare.ddns_enabled} name="enabled" type="checkbox" /><span>Enable DDNS</span></label><div className="form-grid two"><label className="field"><span>Hostname</span><input defaultValue={config.cloudflare.domain} name="domain" /></label><label className="field"><span>Zone</span><input defaultValue={config.cloudflare.zone_name} name="zone" /></label><label className="field form-span"><span>New scoped API token</span><input autoComplete="new-password" name="token" placeholder="Leave blank to keep stored secret" type="password" /></label></div><p className="form-note">Cloudflare Tunnel remains unavailable; WireGuard is the only accepted remote-entry path.</p><div className="form-actions"><button className="button primary" disabled={busy} type="submit">Apply DDNS configuration</button></div></form></section>}

        {active === "squid" && <section className="dashboard-section" id="squid"><div className="dashboard-section-heading"><div><p className="eyebrow">Optional</p><h2>Squid forward proxy</h2></div></div><form className="settings-form" key={`squid-${config.revision}`} onSubmit={submitSquid}><label className="checkbox-row"><input defaultChecked={config.squid_proxy.enabled} name="enabled" type="checkbox" /><span>Enable non-caching proxy</span></label><div className="form-grid two"><label className="field"><span>Port</span><input defaultValue={config.squid_proxy.port} name="port" type="number" /></label><label className="field"><span>Username</span><input defaultValue={config.squid_proxy.username} name="username" /></label><label className="field form-span"><span>New password</span><input autoComplete="new-password" name="password" placeholder="Leave blank to keep stored secret" type="password" /></label></div><div className="form-actions"><button className="button primary" disabled={busy} type="submit">Apply proxy configuration</button></div></form></section>}

        {active === "dns-filter" && <DNSFilterPanel apiConnected onError={setError} />}

        {active === "wifi" && <section className="dashboard-section" id="wifi"><div className="dashboard-section-heading"><div><p className="eyebrow">Optional hardware</p><h2>Wi-Fi access point</h2></div></div><form className="settings-form" key={`wifi-${config.revision}`} onSubmit={submitWiFi}><label className="checkbox-row"><input defaultChecked={config.wifi.enabled} name="enabled" type="checkbox" /><span>Enable access point</span></label><div className="form-grid two"><label className="field"><span>Radio interface</span><input defaultValue={config.wifi.interface} name="interface" /></label><label className="field"><span>SSID</span><input defaultValue={config.wifi.ssid} name="ssid" /></label><label className="field"><span>Band</span><select defaultValue={config.wifi.band} name="band"><option value="2.4ghz">2.4 GHz</option><option value="5ghz">5 GHz</option></select></label><label className="field"><span>Channel</span><input defaultValue={config.wifi.channel} name="channel" type="number" /></label><label className="field form-span"><span>New passphrase</span><input autoComplete="new-password" name="passphrase" placeholder="Leave blank to keep stored secret" type="password" /></label></div><label className="checkbox-row"><input defaultChecked={config.wifi.hide_ssid} name="hide_ssid" type="checkbox" /><span>Hide SSID</span></label><div className="form-actions"><button className="button primary" disabled={busy} type="submit">Apply Wi-Fi configuration</button></div></form></section>}

        {active === "recovery" && <section className="dashboard-section" id="recovery"><div className="dashboard-section-heading"><div><p className="eyebrow">Recoverability</p><h2>Snapshots and local console</h2></div><button className="button primary" disabled={busy} onClick={() => void createSnapshot()} type="button">Create snapshot</button></div><div className="dashboard-callout"><strong>Network recovery is intentionally unavailable.</strong><p>Password/TOTP reset, LAN repair, snapshot recovery, and factory reset use <code>router-recovery</code> on the local console.</p></div><article className="card table-card"><div className="table-scroll"><table><thead><tr><th>Created</th><th>Revision</th><th>Checksum</th><th>Action</th></tr></thead><tbody>{snapshots.length === 0 ? <tr><td className="empty-state" colSpan={4}>No snapshots yet.</td></tr> : snapshots.map((snapshot) => <tr key={snapshot.id}><td>{new Date(snapshot.created_at).toLocaleString()}</td><td>{snapshot.revision}</td><td><code>{snapshot.checksum.slice(0, 16)}…</code></td><td><button className="button secondary small" disabled={busy} onClick={() => void restoreSnapshot(snapshot.id)} type="button">Restore</button></td></tr>)}</tbody></table></div></article></section>}

        {active === "security" && <section className="dashboard-section" id="security"><div className="dashboard-section-heading"><div><p className="eyebrow">Administrator</p><h2>Security settings</h2></div></div><form className="settings-form narrow" onSubmit={changePassword}><div className="form-grid"><label className="field"><span>Current password</span><input autoComplete="current-password" name="old_password" required type="password" /></label><label className="field"><span>New password</span><input autoComplete="new-password" minLength={15} name="new_password" required type="password" /></label><label className="field"><span>Confirm new password</span><input autoComplete="new-password" minLength={15} name="confirm_password" required type="password" /></label></div><p className="form-note">Changing the password revokes every session. TOTP enrollment remains available through the authenticated API; lost TOTP recovery is local-console only.</p><div className="form-actions"><button className="button primary" disabled={busy} type="submit">Change password</button></div></form></section>}
        {active === "logs" && <AuditLogPanel />}
      </main>
    </div>
  );
}

export default function DashboardApp() {
  return <AuthGate><Dashboard /></AuthGate>;
}
