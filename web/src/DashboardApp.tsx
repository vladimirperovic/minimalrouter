import { FormEvent, ReactNode, useCallback, useEffect, useMemo, useRef, useState } from "react";
import AuthGate from "./components/AuthGate";
import ApplianceHealthBanner from "./components/ApplianceHealthBanner";
import ClassicOverview from "./components/ClassicOverview";
import SecuritySettings from "./components/SecuritySettings";
import ProfileMenu from "./components/ProfileMenu";
import { apiFetch } from "./lib/api";
import type { GatewaySettings, GatewaySummary, PendingTransaction, RouterConfig, Snapshot, SystemStatus } from "./api-types";
import DashboardSections, { type SectionID } from "./components/DashboardSections";
import "./DashboardApp.css";
import "./ClassicDashboard.css";

const navigation: Array<[SectionID, string]> = [
  ["overview", "Overview"],
  ["gateway", "Gateway Quality"],
  ["network", "LAN & DHCP"],
  ["firewall", "Firewall"],
  ["qos", "QoS / SQM"],
  ["wireguard", "WireGuard"],
  ["cloudflare", "Dynamic DNS"],
  ["squid", "Squid Proxy"],
  ["dns-filter", "DNS Filter"],
  ["wifi", "Wi-Fi AP"],
  ["recovery", "Recovery"],
  ["security", "Security"],
  ["logs", "Logs"],
];

const navIcons: Record<SectionID, ReactNode> = {
  overview: <path d="M3 3h8v8H3zM13 3h8v5h-8zM13 12h8v9h-8zM3 15h8v6H3z" />,
  gateway: <path d="M22 12h-4l-3 9L9 3l-3 9H2" />,
  network: <path d="M17 3a2 2 0 0 0-2 2c0 .56.23 1.06.6 1.42l-5.18 5.18a2 2 0 0 0-2.84 0L2 6.1M6.1 22l3.5-3.5M17 13a2 2 0 0 1 0 4" />,
  firewall: <path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z" />,
  qos: <path d="M3 12h4v3H3zM9 12h4v3H9zM15 12h4v3h-4zM3 17h4v3H3zM9 17h4v3H9zM15 17h4v3h-4z" />,
  wireguard: <path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71" />,
  cloudflare: <path d="M18 10h-1.26A8 8 0 1 0 9 20h9a5 5 0 0 0 0-10z" />,
  squid: <path d="M2 20h20M4 20V9h16v11M12 9V5m-4 0h8M12 20v-4h-2m2 4h-2" />,
  "dns-filter": <path d="M22 3H2l8 9.46V19l4 2v-8.54L22 3z" />,
  wifi: <path d="M5 12.55a11 11 0 0 1 14.08 0M1.42 9a16 16 0 0 1 21.16 0M8.53 16.11a6 6 0 0 1 6.95 0M12 20h.01" />,
  recovery: <path d="M23 4v6h-6M1 20v-6h6M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15" />,
  security: <path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10zM9 11.5l2 2 4-4" />,
  logs: <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8zM14 2v6h6M16 13H8M16 17H8M10 9H8" />,
};

function field(form: FormData, name: string) {
  return String(form.get(name) ?? "").trim();
}

function Dashboard() {
  const [active, setActive] = useState<SectionID>("overview");
  const [config, setConfig] = useState<RouterConfig | null>(null);
  const [system, setSystem] = useState<SystemStatus>({});
  const [gatewaySummary, setGatewaySummary] = useState<GatewaySummary | null>(null);
  const [gatewaySettings, setGatewaySettings] = useState<GatewaySettings>({ enabled: true, targets: ["1.1.1.1", "8.8.8.8"], interval_seconds: 30 });
  const [snapshots, setSnapshots] = useState<Snapshot[]>([]);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [busy, setBusy] = useState(false);
  const [menuOpen, setMenuOpen] = useState(false);
  const [dark, setDark] = useState(false);
  const [pendingTx, setPendingTx] = useState<PendingTransaction | null>(null);
  const [countdown, setCountdown] = useState(0);
  const [lastRefresh, setLastRefresh] = useState<Date | null>(null);
  const pollSequence = useRef(0);
  const pollController = useRef<AbortController | null>(null);

  const load = useCallback(async () => {
    const sequence = ++pollSequence.current;
    pollController.current?.abort();
    const controller = new AbortController();
    pollController.current = controller;
    try {
      const [configResult, systemResult, gatewayResult, gatewaySettingsResult, snapshotsResult, pendingResult] = await Promise.allSettled([
        apiFetch("/api/v1/config", { signal: controller.signal }),
        apiFetch("/api/v1/system", { signal: controller.signal }),
        apiFetch("/api/v1/gateway/summary", { signal: controller.signal }),
        apiFetch("/api/v1/gateway/settings", { signal: controller.signal }),
        apiFetch("/api/v1/snapshots", { signal: controller.signal }),
        apiFetch("/api/v1/transactions/pending", { signal: controller.signal }),
      ]);
      if (sequence !== pollSequence.current) return;
      if (configResult.status === "fulfilled" && configResult.value.ok) {
        setConfig((await configResult.value.json()) as RouterConfig);
      } else {
        throw new Error("Configuration unavailable");
      }

      const unavailable: string[] = [];
      if (systemResult.status === "fulfilled" && systemResult.value.ok) {
        setSystem((await systemResult.value.json()) as SystemStatus);
        setLastRefresh(new Date());
      } else {
        setSystem({});
        setLastRefresh(null);
        unavailable.push("system status");
      }
      if (gatewayResult.status === "fulfilled" && gatewayResult.value.ok) {
        setGatewaySummary((await gatewayResult.value.json()) as GatewaySummary);
      } else {
        setGatewaySummary(null);
        unavailable.push("gateway quality");
      }
      if (gatewaySettingsResult.status === "fulfilled" && gatewaySettingsResult.value.ok) {
        setGatewaySettings((await gatewaySettingsResult.value.json()) as GatewaySettings);
      }
      if (snapshotsResult.status === "fulfilled" && snapshotsResult.value.ok) {
        const body = await snapshotsResult.value.json();
        setSnapshots(Array.isArray(body) ? body : Array.isArray(body.snapshots) ? body.snapshots : []);
      }
      if (pendingResult.status === "fulfilled" && pendingResult.value.ok) {
        const body = (await pendingResult.value.json()) as PendingTransaction;
        setPendingTx(body?.id ? body : null);
      }
      setError(unavailable.length > 0 ? `Live data unavailable: ${unavailable.join(", ")}.` : "");
    } catch (loadError) {
      if ((loadError as Error).name !== "AbortError" && sequence === pollSequence.current) {
        setError(loadError instanceof Error ? loadError.message : "Router API unavailable");
      }
    }
  }, []);

  useEffect(() => {
    let active = true;
    let timer = 0;
    const poll = async () => {
      await load();
      if (active) timer = window.setTimeout(poll, 15000);
    };
    void poll();
    return () => {
      active = false;
      window.clearTimeout(timer);
      pollController.current?.abort();
    };
  }, [load]);

  useEffect(() => {
    if (!pendingTx?.confirmation_deadline) {
      setCountdown(0);
      return;
    }
    const tick = () => setCountdown(Math.max(0, Math.ceil((new Date(pendingTx.confirmation_deadline!).getTime() - Date.now()) / 1000)));
    tick();
    const timer = window.setInterval(tick, 1000);
    return () => window.clearInterval(timer);
  }, [pendingTx]);

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
      const next = (await response.json()) as RouterConfig;
      mutate(next);
      const applyResponse = await apiFetch("/api/v1/config", {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(next),
      });
      const body = await applyResponse.json().catch(() => ({}));
      if (!applyResponse.ok) throw new Error(body.error || `Apply failed (${applyResponse.status})`);
      if (body.state === "AwaitingConfirmation" && body.id) setPendingTx(body as PendingTransaction);
      setNotice(body.state === "AwaitingConfirmation" ? "Promjena je privremeno aktivna i čeka potvrdu pristupa." : success);
      await load();
    } catch (applyError) {
      setError(applyError instanceof Error ? applyError.message : "Configuration apply failed");
    } finally {
      setBusy(false);
    }
  };

  const triggerRecovery = async () => {
    setBusy(true);
    setError("");
    try {
      const res = await apiFetch("/api/v1/recovery/reconcile", { method: "POST" });
      const body = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(body.error || `Recovery failed (${res.status})`);
      setNotice("Recovery successful. Services reconciled.");
      await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Recovery reconciliation failed");
    } finally {
      setBusy(false);
    }
  };

  const submitNetwork = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    void applyConfig((next) => {
      next.wan = {
        ...next.wan,
        interface: field(form, "wan_interface"),
        enabled: next.wan.enabled,
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
        enabled: next.dhcp.enabled,
        range_start: field(form, "dhcp_start"),
        range_end: field(form, "dhcp_end"),
        lease_time: field(form, "lease_time"),
        dns_servers: field(form, "dns_servers").split(",").map((item) => item.trim()).filter(Boolean),
      };
      const records: Array<{ name: string; ip: string }> = [];
      for (const [key, value] of Array.from(form.entries())) {
        if (!key.startsWith("dns_record_name_")) continue;
        const name = String(value).trim();
        const ip = String(form.get(key.replace("dns_record_name_", "dns_record_ip_")) || "").trim();
        if (name || ip) records.push({ name, ip });
      }
      next.dns = { ...next.dns, records };
    }, "Network configuration applied.");
  };

  const submitWireGuardClient = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    const keepalive = Number(field(form, "client_keepalive"));
    const persistentKeepalive = Number.isFinite(keepalive) && keepalive >= 0 ? keepalive : 25;
    void applyConfig((next) => {
      next.wg_client = {
        ...next.wg_client,
        enabled: next.wg_client.enabled,
        endpoint: field(form, "client_endpoint"),
        public_key: field(form, "client_public_key"),
        preshared_key: field(form, "client_preshared_key") || next.wg_client.preshared_key,
        address: field(form, "client_address"),
        allowed_ips: field(form, "client_allowed_ips").split(",").map((item) => item.trim()).filter(Boolean),
        persistent_keepalive: persistentKeepalive,
        private_key: field(form, "client_private_key") || next.wg_client.private_key,
      };
    }, "WireGuard client settings applied.");
  };

  const toggleWGClient = (enabled: boolean) => {
    void applyConfig((next) => {
      next.wg_client = { ...next.wg_client, enabled };
    }, enabled ? "WireGuard client enabled." : "WireGuard client disabled.");
  };

  const toggleWAN = (enabled: boolean) => {
    void applyConfig((next) => {
      next.wan = { ...next.wan, enabled };
    }, enabled ? "WAN enabled." : "WAN disabled.");
  };

  const toggleDHCP = (enabled: boolean) => {
    void applyConfig((next) => {
      next.dhcp = { ...next.dhcp, enabled };
    }, enabled ? "DHCP server enabled." : "DHCP server disabled.");
  };

  const applyGatewayMonitoring = (settings: GatewaySettings) => {
    void (async () => {
      setBusy(true);
      setNotice("");
      setError("");
      try {
        const response = await apiFetch("/api/v1/gateway/settings", {
          method: "PUT",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(settings),
        });
        const body = await response.json().catch(() => ({}));
        if (!response.ok) throw new Error(body.error || `Gateway settings update failed (${response.status})`);
        setGatewaySettings(body as GatewaySettings);
        setNotice("Gateway monitoring settings applied.");
        await load();
      } catch (settingsError) {
        setError(settingsError instanceof Error ? settingsError.message : "Gateway settings update failed");
      } finally {
        setBusy(false);
      }
    })();
  };

  const submitCloudflare = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    void applyConfig((next) => {
      const provider = field(form, "provider") || "noip";
      const previousProvider = next.cloudflare.ddns_provider || "cloudflare";
      const newCredential = field(form, "credential");
      next.cloudflare = {
        ...next.cloudflare,
        ddns_enabled: next.cloudflare.ddns_enabled,
        ddns_provider: provider,
        ddns_username: field(form, "username"),
        domain: field(form, "domain"),
        zone_name: field(form, "zone"),
        api_token: newCredential || (provider === previousProvider ? next.cloudflare.api_token : ""),
        tunnel_enabled: false,
      };
    }, "Dynamic DNS configuration applied.");
  };

  const submitSquid = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    void applyConfig((next) => {
      next.squid_proxy = {
        ...next.squid_proxy,
        enabled: next.squid_proxy.enabled,
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
        enabled: next.wifi.enabled,
        interface: field(form, "interface"),
        ssid: field(form, "ssid"),
        passphrase: field(form, "passphrase") || next.wifi.passphrase,
        band: field(form, "band"),
        channel: Number(field(form, "channel")),
        hide_ssid: form.get("hide_ssid") === "on",
      };
    }, "Wi-Fi configuration applied.");
  };

  const submitQoS = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    void applyConfig((next) => {
      next.qos = {
        enabled: next.qos.enabled,
        algorithm: field(form, "algorithm") || "cake",
        download_limit_mbps: Number(field(form, "download_limit_mbps")) || 100,
        upload_limit_mbps: Number(field(form, "upload_limit_mbps")) || 20,
      };
    }, "QoS configuration applied.");
  };

  const toggleQoS = (enabled: boolean) => {
    void applyConfig((next) => {
      next.qos = { ...next.qos, enabled };
    }, enabled ? "QoS enabled." : "QoS disabled.");
  };

  const toggleCloudflare = (enabled: boolean) => {
    void applyConfig((next) => {
      next.cloudflare = { ...next.cloudflare, ddns_enabled: enabled };
    }, enabled ? "Dynamic DNS enabled." : "Dynamic DNS disabled.");
  };

  const toggleSquid = (enabled: boolean) => {
    void applyConfig((next) => {
      next.squid_proxy = { ...next.squid_proxy, enabled };
    }, enabled ? "Squid proxy enabled." : "Squid proxy disabled.");
  };

  const toggleWiFi = (enabled: boolean) => {
    void applyConfig((next) => {
      next.wifi = { ...next.wifi, enabled };
    }, enabled ? "Wi-Fi access point enabled." : "Wi-Fi access point disabled.");
  };

  const [speedTest, setSpeedTest] = useState<{ download_mbps: number; upload_mbps: number; suggested_download_mbps: number; suggested_upload_mbps: number } | null>(null);
  const [speedTesting, setSpeedTesting] = useState(false);

  const runSpeedTest = async () => {
    setSpeedTesting(true);
    setSpeedTest(null);
    setNotice("");
    setError("");
    try {
      const response = await apiFetch("/api/v1/qos/speedtest", { method: "POST" });
      const body = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(body.error || `Speed test failed (${response.status})`);
      setSpeedTest(body);
      setNotice("Measured. Suggested limits are 90% of the result — apply to enable QoS.");
    } catch (speedTestError) {
      setError(speedTestError instanceof Error ? speedTestError.message : "Speed test failed");
    } finally {
      setSpeedTesting(false);
    }
  };

  const confirmPending = async () => {
    if (!pendingTx?.id) return;
    setBusy(true);
    try {
      const response = await apiFetch(`/api/v1/transactions/${encodeURIComponent(pendingTx.id)}/confirm`, { method: "POST" });
      if (!response.ok) throw new Error(`Confirmation failed (${response.status})`);
      setPendingTx(null);
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
      if (body.state === "AwaitingConfirmation" && body.id) setPendingTx(body as PendingTransaction);
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
    if (newPassword.length < 12 || newPassword !== confirm) {
      setError("Nova lozinka mora imati najmanje 12 karaktera i potvrda mora biti ista.");
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

  const ddnsProvider = config.cloudflare.ddns_provider === "noip" ? "No-IP" : "Cloudflare";
  const gatewayState = gatewaySummary?.state || "unknown";

  return (
    <div className="dashboard-app">
      <aside className={menuOpen ? "dashboard-sidebar is-open" : "dashboard-sidebar"}>
        <div className="dashboard-brand">
          <span className="classic-brand-mark" aria-hidden="true"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round"><path d="M2 9h18M4 9l1.2 5.2a3 3 0 0 0 2.9 2.3h7.8a3 3 0 0 0 2.9-2.3L20 9" /><circle cx="5.5" cy="17.5" r="1.2" fill="currentColor" stroke="none" /><circle cx="8.5" cy="17.5" r="1.2" fill="currentColor" stroke="none" /><path d="M7 5.5h10M10 3.5h4" /></svg></span>
          <div className="dashboard-brand-title"><strong>Minimal</strong><small>Router</small></div>
        </div>
        <nav aria-label="Router sections">
          {navigation.map(([id, label]) => (
            <a className={active === id ? "is-active" : ""} href={`#${id}`} key={id} onClick={() => { setActive(id); setMenuOpen(false); }}><svg className="dashboard-nav-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">{navIcons[id]}</svg><span>{label}</span></a>
          ))}
        </nav>
        <div className="dashboard-brand-revision">Revision {config.revision}</div>
      </aside>

      <main className="dashboard-main">
        <header className="dashboard-topbar classic-topbar">
          <button aria-label="Open navigation" className="dashboard-menu" onClick={() => setMenuOpen((value) => !value)} type="button">☰</button>
          <div className="classic-topbar-status" aria-label="Router service status">
            <span className={config.firewall.stateful_firewall ? "classic-status-chip" : "classic-status-chip is-off"}>Firewall</span>
            <span className={config.wireguard.enabled ? "classic-status-chip" : "classic-status-chip is-off"}>WireGuard {config.wireguard.enabled && <b className="chip-badge">{system.runtime?.wireguard_active_peers || 0} / {(config.wireguard.peers || []).filter(p => p.enabled).length}</b>}</span>
            <span className={config.dhcp.enabled ? "classic-status-chip" : "classic-status-chip is-off"}>DHCP {config.dhcp.enabled && <b className="chip-badge">{system.runtime?.dhcp_leases?.length || 0}</b>}</span>
            <span className="classic-status-chip">DNS</span>
            <span className={config.cloudflare.ddns_enabled ? (system.runtime?.ddns?.running ? "classic-status-chip" : "classic-status-chip is-info") : "classic-status-chip is-off"}>{config.cloudflare.ddns_enabled ? `DDNS: ${ddnsProvider}` : "DDNS off"}</span>
            <span className={config.squid_proxy.enabled ? "classic-status-chip" : "classic-status-chip is-off"}>Squid Proxy {config.squid_proxy.enabled ? "on" : "off"}</span>
            <span className={config.qos.enabled ? "classic-status-chip" : "classic-status-chip is-off"}>QoS {config.qos.enabled ? `${config.qos.algorithm}` : "off"}</span>
            <span className={config.cloudflare.tunnel_enabled ? "classic-status-chip" : "classic-status-chip is-off"}>{config.cloudflare.tunnel_enabled ? "Cloudflare Tunnel" : "Cloudflare Tunnel off"}</span>
            <span className={gatewayState === "healthy" ? "classic-status-chip" : gatewayState === "unknown" ? "classic-status-chip is-off" : "classic-status-chip is-warning"}>Gateway {gatewayState}</span>
          </div>
          <div className="classic-topbar-actions">
            <button className="classic-topbar-button" onClick={() => setDark((value) => !value)} type="button" aria-label="Toggle appearance">{dark ? "☀" : "◐"}</button>
            <span className="classic-setup-pill">Setup complete</span>
            <ProfileMenu changePassword={changePassword} logout={logout} error={error} setError={setError} />
          </div>
        </header>

        {error && <div className="dashboard-alert is-error" role="alert">{error}<button aria-label="Dismiss error" onClick={() => setError("")} type="button">✕</button></div>}
        {notice && <div className="dashboard-alert is-success" role="status">{notice}<button aria-label="Dismiss notice" onClick={() => setNotice("")} type="button">✕</button></div>}
        {system.recovery_required && <div className="dashboard-alert is-error" role="alert"><strong>Recovery required:</strong> {system.recovery_reason || "Canonical reconciliation failed."}<button className="button primary classic-alert-recover" disabled={busy} onClick={() => void triggerRecovery()} type="button">{busy ? "Recovering..." : "Reconcile now"}</button></div>}
        {pendingTx && <div className="dashboard-alert is-warning"><span>A connectivity-critical change is awaiting confirmation. Automatic rollback in {countdown}s.</span><button className="button primary" disabled={busy} onClick={() => void confirmPending()} type="button">Confirm access</button></div>}

        {active === "overview" && <ClassicOverview config={config} system={system} runtime={runtime} gatewaySummary={gatewaySummary} memoryPercent={memoryPercent} diskPercent={diskPercent} lastRefresh={lastRefresh} />}
        {active === "overview" && <ApplianceHealthBanner />}

        {active === "security" && <SecuritySettings config={config} onError={setError} />}

        {active !== "security" && (
          <DashboardSections
            key={`dashboard-sections-${config.revision}`}
            active={active}
            applyConfig={applyConfig}
            applyGatewayMonitoring={applyGatewayMonitoring}
            busy={busy}
            config={config}
            createSnapshot={createSnapshot}
            diskPercent={diskPercent}
            gatewaySummary={gatewaySummary}
            gatewaySettings={gatewaySettings}
            lastRefresh={lastRefresh}
            leases={leases}
            load={load}
            memoryPercent={memoryPercent}
            restoreSnapshot={restoreSnapshot}
            setError={setError}
            snapshots={snapshots}
            submitCloudflare={submitCloudflare}
            submitNetwork={submitNetwork}
            submitSquid={submitSquid}
            submitWiFi={submitWiFi}
            submitQoS={submitQoS}
            submitWireGuardClient={submitWireGuardClient}
            toggleWGClient={toggleWGClient}
            runSpeedTest={runSpeedTest}
            toggleQoS={toggleQoS}
            toggleWAN={toggleWAN}
            toggleDHCP={toggleDHCP}
            toggleCloudflare={toggleCloudflare}
            toggleSquid={toggleSquid}
            toggleWiFi={toggleWiFi}
            speedTest={speedTest}
            speedTesting={speedTesting}
            system={system}
            runtime={runtime}
          />
        )}
      </main>
    </div>
  );
}

export default function DashboardApp() {
  return <AuthGate><Dashboard /></AuthGate>;
}
