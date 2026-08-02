import { FormEvent, useCallback, useEffect, useMemo, useRef, useState } from "react";
import AuthGate from "./components/AuthGate";
import ApplianceHealthBanner from "./components/ApplianceHealthBanner";
import ClassicOverview from "./components/ClassicOverview";
import SecuritySettings from "./components/SecuritySettings";
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
  ["wireguard", "WireGuard"],
  ["cloudflare", "Dynamic DNS"],
  ["squid", "Squid Proxy"],
  ["dns-filter", "DNS Filter"],
  ["wifi", "Wi-Fi AP"],
  ["recovery", "Recovery"],
  ["security", "Security"],
  ["logs", "Logs"],
];

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
      if (systemResult.status === "fulfilled" && systemResult.value.ok) {
        setSystem((await systemResult.value.json()) as SystemStatus);
      }
      if (gatewayResult.status === "fulfilled" && gatewayResult.value.ok) {
        setGatewaySummary((await gatewayResult.value.json()) as GatewaySummary);
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
      setLastRefresh(new Date());
      setError("");
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
        ddns_enabled: form.get("enabled") === "on",
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

  const ddnsProvider = config.cloudflare.ddns_provider === "noip" ? "No-IP" : "Cloudflare";
  const gatewayState = gatewaySummary?.state || "unknown";

  return (
    <div className="dashboard-app">
      <aside className={menuOpen ? "dashboard-sidebar is-open" : "dashboard-sidebar"}>
        <div className="dashboard-brand"><span className="classic-brand-mark" aria-hidden="true"><i /><i /><i /><i /></span><div><strong>Minimal Router</strong><small>Home gateway</small></div></div>
        <nav aria-label="Router sections">
          {navigation.map(([id, label], index) => (
            <a className={active === id ? "is-active" : ""} href={`#${id}`} key={id} onClick={() => { setActive(id); setMenuOpen(false); }}><span>{String(index + 1).padStart(2, "0")}</span>{label}</a>
          ))}
        </nav>
      </aside>

      <main className="dashboard-main">
        <header className="dashboard-topbar classic-topbar">
          <button aria-label="Open navigation" className="dashboard-menu" onClick={() => setMenuOpen((value) => !value)} type="button">☰</button>
          <div className="classic-topbar-status" aria-label="Router service status">
            <span className={config.firewall.stateful_firewall ? "classic-status-chip" : "classic-status-chip is-off"}>Firewall</span>
            <span className={config.wireguard.enabled ? "classic-status-chip" : "classic-status-chip is-off"}>WireGuard {config.wireguard.enabled && <b className="chip-badge">{(config.wireguard.peers || []).filter(p => p.enabled).length}</b>}</span>
            <span className={config.dhcp.enabled ? "classic-status-chip" : "classic-status-chip is-off"}>DHCP {config.dhcp.enabled && <b className="chip-badge">{system.runtime?.dhcp_leases?.length || 0}</b>}</span>
            <span className="classic-status-chip">DNS</span>
            <span className={config.cloudflare.ddns_enabled ? "classic-status-chip" : "classic-status-chip is-off"}>{config.cloudflare.ddns_enabled ? ddnsProvider : "DDNS off"}</span>
            <span className={gatewayState === "healthy" ? "classic-status-chip" : gatewayState === "unknown" ? "classic-status-chip is-off" : "classic-status-chip is-warning"}>Gateway {gatewayState}</span>
          </div>
          <div className="classic-topbar-actions">
            <button className="classic-topbar-button" onClick={() => setDark((value) => !value)} type="button" aria-label="Toggle appearance">{dark ? "☀" : "◐"}</button>
            <span className="classic-setup-pill">Setup complete</span>
            <button className="classic-avatar" onClick={() => { setActive("security"); setMenuOpen(false); }} type="button" title="Security Settings">VP</button>
          </div>
        </header>

        {error && <div className="dashboard-alert is-error" role="alert">{error}<button aria-label="Dismiss error" onClick={() => setError("")} type="button">✕</button></div>}
        {notice && <div className="dashboard-alert is-success" role="status">{notice}<button aria-label="Dismiss notice" onClick={() => setNotice("")} type="button">✕</button></div>}
        {system.recovery_required && <div className="dashboard-alert is-error" role="alert"><strong>Recovery required:</strong> {system.recovery_reason || "Canonical reconciliation failed."}</div>}
        {pendingTx && <div className="dashboard-alert is-warning"><span>A connectivity-critical change is awaiting confirmation. Automatic rollback in {countdown}s.</span><button className="button primary" disabled={busy} onClick={() => void confirmPending()} type="button">Confirm access</button></div>}

        {active === "overview" && <ClassicOverview config={config} system={system} runtime={runtime} gatewaySummary={gatewaySummary} memoryPercent={memoryPercent} diskPercent={diskPercent} leases={leases} lastRefresh={lastRefresh} />}
        {active === "overview" && <ApplianceHealthBanner />}

        {active === "security" && <SecuritySettings changePassword={changePassword} logout={logout} />}

        {active !== "security" && (
          <DashboardSections
            active={active}
            applyConfig={applyConfig}
            applyGatewayMonitoring={applyGatewayMonitoring}
            busy={busy}
            changePassword={changePassword}
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
