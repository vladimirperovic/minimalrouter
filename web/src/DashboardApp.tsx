import { FormEvent, MouseEvent, ReactNode, useCallback, useEffect, useMemo, useRef, useState } from "react";
import AuthGate from "./components/AuthGate";
import ClassicOverview from "./components/ClassicOverview";
import SecuritySettings from "./components/SecuritySettings";
import ProfileMenu from "./components/ProfileMenu";
import UpdateDialog from "./components/UpdateDialog";
import SkinMenu from "./components/SkinMenu";
import { applySkin, initialSkin, type SkinID } from "./skins/skins";
import { apiFetch } from "./lib/api";
import { updateBadgeLabel, useUpdates } from "./lib/updates";
import type { GatewaySettings, GatewaySummary, PendingTransaction, RouterConfig, Snapshot, SystemStatus } from "./api-types";
import DashboardSections, { type SectionID } from "./components/DashboardSections";
import { useApplianceHealth } from "./components/HealthBanner";
import "./DashboardApp.css";
import "./ClassicDashboard.css";
import "./components/DashboardAdditions.css";

const navigationGroups: Array<{ label: string; items: Array<[SectionID, string]> }> = [
  { label: "Monitor", items: [["overview", "Overview"], ["gateway", "Gateway Quality"], ["network", "LAN & DHCP"]] },
  { label: "Protect", items: [["firewall", "Firewall"], ["security", "Security"], ["dns-filter", "DNS Filter"]] },
  { label: "Connect", items: [["qos", "QoS / SQM"], ["wireguard", "WireGuard"], ["cloudflare", "DynDNS"], ["wifi", "Wi-Fi AP"]] },
  { label: "Operate", items: [["traffic", "Traffic"], ["squid", "Squid Proxy"], ["recovery", "Recovery"], ["logs", "Logs"]] },
];

const navigation = navigationGroups.flatMap((group) => group.items);

function sectionFromHash(): SectionID {
  const candidate = window.location.hash.slice(1);
  return navigation.some(([id]) => id === candidate) ? candidate as SectionID : "overview";
}

const navIcons: Record<SectionID, ReactNode> = {
  overview: <path d="M3 3h8v8H3zM13 3h8v5h-8zM13 12h8v9h-8zM3 15h8v6H3z" />,
  gateway: <path d="M22 12h-4l-3 9L9 3l-3 9H2" />,
  network: <><circle cx="5" cy="5" r="2" /><circle cx="19" cy="5" r="2" /><circle cx="12" cy="19" r="2" /><path d="m6.8 6.1 4 10.9M17.2 6.1l-4 10.9M7 5h10" /></>,
  firewall: <path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z" />,
  qos: <path d="M5 20V10M10 20V4M15 20v-7M20 20V7" />,
  wireguard: <path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71" />,
  cloudflare: <path d="M18 10h-1.26A8 8 0 1 0 9 20h9a5 5 0 0 0 0-10z" />,
  squid: <path d="M2 20h20M4 20V9h16v11M12 9V5m-4 0h8M12 20v-4h-2m2 4h-2" />,
  "dns-filter": <path d="M22 3H2l8 9.46V19l4 2v-8.54L22 3z" />,
  wifi: <path d="M5 12.55a11 11 0 0 1 14.08 0M1.42 9a16 16 0 0 1 21.16 0M8.53 16.11a6 6 0 0 1 6.95 0M12 20h.01" />,
  recovery: <path d="M23 4v6h-6M1 20v-6h6M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15" />,
  security: <><rect x="5" y="10" width="14" height="11" rx="2" /><path d="M8 10V7a4 4 0 0 1 8 0v3M12 14v3" /></>,
  logs: <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8zM14 2v6h6M16 13H8M16 17H8M10 9H8" />,
  traffic: <><path d="M3 3v18h18" /><path d="m7 15 4-4 3 3 5-6" /></>,
};

const THEME_STORAGE_KEY = "minimalrouter:theme";

function initialTheme(): boolean {
  try {
    const stored = window.localStorage.getItem(THEME_STORAGE_KEY);
    if (stored === "dark") return true;
    if (stored === "light") return false;
  } catch {
    // Private-mode browsers can throw on access; fall through to the OS hint.
  }
  return window.matchMedia?.("(prefers-color-scheme: dark)").matches ?? false;
}

function field(form: FormData, name: string) {
  return String(form.get(name) ?? "").trim();
}

const REQUIRED_CONFIG_SECTIONS = [
  "wan", "lan", "dhcp", "firewall", "wireguard",
  "cloudflare", "squid_proxy", "adguard", "qos", "wifi",
] as const;

function isRenderableConfig(value: RouterConfig | null): value is RouterConfig {
  if (!value || typeof value !== "object") return false;
  return REQUIRED_CONFIG_SECTIONS.every((section) => {
    const item = (value as unknown as Record<string, unknown>)[section];
    return item !== null && typeof item === "object";
  });
}

function Dashboard() {
  const [active, setActive] = useState<SectionID>(sectionFromHash);
  const [config, setConfig] = useState<RouterConfig | null>(null);
  const [system, setSystem] = useState<SystemStatus>({});
  const [gatewaySummary, setGatewaySummary] = useState<GatewaySummary | null>(null);
  const [gatewaySettings, setGatewaySettings] = useState<GatewaySettings>({ enabled: true, targets: ["1.1.1.1", "8.8.8.8"], interval_seconds: 30 });
  const [snapshots, setSnapshots] = useState<Snapshot[]>([]);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [busy, setBusy] = useState(false);
  const [menuOpen, setMenuOpen] = useState(false);
  const [dark, setDark] = useState(initialTheme);
  const [skin, setSkin] = useState<SkinID>(initialSkin);
  // A successful apply bumps config.revision, which remounts DashboardSections.
  // The per-section "Saved" confirmation therefore has to live here, above the
  // remount, or it is destroyed the moment the save it reports succeeds.
  const [savedSection, setSavedSection] = useState("");
  const savedSectionTimer = useRef(0);
  const [skinOpen, setSkinOpen] = useState(false);
  const [pendingTx, setPendingTx] = useState<PendingTransaction | null>(null);
  const [countdown, setCountdown] = useState(0);
  const [lastRefresh, setLastRefresh] = useState<Date | null>(null);
  const pollSequence = useRef(0);
  const pollController = useRef<AbortController | null>(null);
  const { health, unavailable: healthUnavailable } = useApplianceHealth();
  // One update controller for the whole dashboard: the sidebar entry and the
  // profile menu open the same dialog over the same state.
  const [updateDialogOpen, setUpdateDialogOpen] = useState(false);
  const updates = useUpdates(true);
  const updateBadge = updateBadgeLabel(updates.status);

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
        const body = (await configResult.value.json()) as RouterConfig | null;
        if (!isRenderableConfig(body)) throw new Error("Configuration unavailable");
        setConfig(body);
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
        const body = (await gatewayResult.value.json()) as GatewaySummary | null;
        if (body && typeof body === "object" && body.link && typeof body.link === "object") {
          setGatewaySummary(body);
        } else {
          setGatewaySummary(null);
          unavailable.push("gateway quality");
        }
      } else {
        setGatewaySummary(null);
        unavailable.push("gateway quality");
      }
      if (gatewaySettingsResult.status === "fulfilled" && gatewaySettingsResult.value.ok) {
        const body = (await gatewaySettingsResult.value.json()) as GatewaySettings | null;
        if (body && Array.isArray(body.targets)) setGatewaySettings(body);
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
    let polling = true;
    let timer = 0;

    const schedule = () => {
      window.clearTimeout(timer);
      if (!polling || document.hidden) return;
      timer = window.setTimeout(() => void poll(), 30_000);
    };

    const poll = async () => {
      if (!polling || document.hidden) return;
      await load();
      schedule();
    };

    const onVisibilityChange = () => {
      window.clearTimeout(timer);
      if (!polling) return;
      if (document.hidden) {
        pollController.current?.abort();
        return;
      }
      // Returning to the dashboard gets one fresh coherent snapshot immediately.
      void poll();
    };

    document.addEventListener("visibilitychange", onVisibilityChange);
    void poll();
    return () => {
      polling = false;
      window.clearTimeout(timer);
      document.removeEventListener("visibilitychange", onVisibilityChange);
      pollController.current?.abort();
    };
  }, [load]);

  // Depend on the deadline value rather than the object identity: the 30-second
  // poll replaces pendingTx with an equal-but-new object, which used to restart
  // this interval on every dashboard refresh.
  const confirmationDeadline = pendingTx?.confirmation_deadline ?? null;
  useEffect(() => {
    if (!confirmationDeadline) {
      setCountdown(0);
      return;
    }
    const tick = () => setCountdown(Math.max(0, Math.ceil((new Date(confirmationDeadline).getTime() - Date.now()) / 1000)));
    tick();
    const timer = window.setInterval(tick, 1000);
    return () => window.clearInterval(timer);
  }, [confirmationDeadline]);

  useEffect(() => {
    document.documentElement.dataset.theme = dark ? "dark" : "light";
    try {
      window.localStorage.setItem(THEME_STORAGE_KEY, dark ? "dark" : "light");
    } catch {
      // Persisting the theme is a convenience; failing to store it must never
      // break the dashboard.
    }
  }, [dark]);

  useEffect(() => {
    applySkin(skin);
  }, [skin]);

  useEffect(() => () => window.clearTimeout(savedSectionTimer.current), []);

  const markSectionSaved = (section: string) => {
    window.clearTimeout(savedSectionTimer.current);
    setSavedSection(section);
    savedSectionTimer.current = window.setTimeout(() => setSavedSection(""), 4000);
  };

  const dashboardReady = Boolean(config);

  useEffect(() => {
    if (!dashboardReady) return;
    const frame = window.requestAnimationFrame(() => window.scrollTo({ top: 0, behavior: "auto" }));
    return () => window.cancelAnimationFrame(frame);
  }, [active, dashboardReady]);

  useEffect(() => {
    const syncSectionFromHash = () => {
      setActive(sectionFromHash());
      window.requestAnimationFrame(() => window.scrollTo({ top: 0, behavior: "auto" }));
    };
    window.addEventListener("hashchange", syncSectionFromHash);
    return () => window.removeEventListener("hashchange", syncSectionFromHash);
  }, []);

  const showSection = (id: SectionID) => {
    if (window.location.hash !== `#${id}`) window.history.pushState(null, "", `#${id}`);
    setActive(id);
    setMenuOpen(false);
    window.requestAnimationFrame(() => window.scrollTo({ top: 0, behavior: "auto" }));
  };

  const navigateToSection = (event: MouseEvent<HTMLAnchorElement>, id: SectionID) => {
    event.preventDefault();
    showSection(id);
  };

  // Returns whether the apply succeeded so callers can show a per-section
  // confirmation only when the write actually landed.
  const applyConfig = async (mutate: (next: RouterConfig) => void, success: string): Promise<boolean> => {
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
      setNotice(body.state === "AwaitingConfirmation" ? "Change is provisionally active and is waiting for access confirmation." : success);
      await load();
      return true;
    } catch (applyError) {
      setError(applyError instanceof Error ? applyError.message : "Configuration apply failed");
      return false;
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

  const deleteSnapshot = async (id: string) => {
    if (!window.confirm(`Delete snapshot ${id}? This restore point cannot be recovered.`)) return;
    setBusy(true);
    try {
      const response = await apiFetch(`/api/v1/snapshots/${encodeURIComponent(id)}`, { method: "DELETE" });
      const body = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(body.error || `Delete failed (${response.status})`);
      setNotice("Snapshot deleted.");
      await load();
    } catch (deleteError) {
      setError(deleteError instanceof Error ? deleteError.message : "Delete failed");
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
      setError("New password must be at least 12 characters and both entries must match.");
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

  const activeLabel = navigation.find(([id]) => id === active)?.[1] || "Overview";
  const failingChecks = health?.checks?.filter((check) => check.state !== "healthy") ?? [];
  const alertCount = system.recovery_required ? failingChecks.length + 1 : failingChecks.length;
  const alertSummary = healthUnavailable
    ? "Appliance health could not be read, so alert state is unknown."
    : system.recovery_required
      ? `Recovery required. ${failingChecks.length} additional check(s) need attention.`
      : failingChecks.length === 0
        ? "No active appliance alerts. All health checks are passing."
        : `${failingChecks.length} check(s) need attention: ${failingChecks.map((check) => check.label).join(", ")}.`;

  return (
    <div className="dashboard-app">
      <aside className={menuOpen ? "dashboard-sidebar is-open" : "dashboard-sidebar"}>
        <div className="dashboard-brand"><div className="dashboard-brand-title"><strong>minimalrouter</strong></div></div>
        <nav className="dashboard-navigation" aria-label="Router sections">
          {navigationGroups.map((group) => <section className="dashboard-nav-group" key={group.label}>
            <h2>{group.label}</h2>
            <div>{group.items.map(([id, label]) => (
              <a className={active === id ? "is-active" : ""} href={`#${id}`} key={id} onClick={(event) => navigateToSection(event, id)}><svg className="dashboard-nav-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">{navIcons[id]}</svg><span>{label}</span></a>
            ))}</div>
          </section>)}
        </nav>
        <div className="dashboard-sidebar-footer">
          {updateBadge
            ? <button className="sidebar-update-button" onClick={() => setUpdateDialogOpen(true)} type="button">
                <svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M12 19V5M5 12l7-7 7 7" /></svg>
                <span>{updateBadge}</span>
              </button>
            : <button className="sidebar-update-link" onClick={() => setUpdateDialogOpen(true)} type="button">Updates</button>}
          <div className="dashboard-brand-revision">Minimal Router OS <span>{system.version && system.version !== "dev" ? system.version : `r${config.revision}`}</span></div>
        </div>
      </aside>

      <main className="dashboard-main">
        <header className="dashboard-topbar classic-topbar">
          <button aria-label="Open navigation" className="dashboard-menu" onClick={() => setMenuOpen((value) => !value)} type="button">☰</button>
          <div className="classic-page-heading"><h1>{activeLabel}</h1></div>
          <div className="classic-topbar-actions">
            <div className="classic-live-sync"><i aria-hidden="true" /><span><strong>Live</strong><small>{lastRefresh ? `Updated ${lastRefresh.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}` : "Connecting"}</small></span></div>
            <SkinMenu onSelect={setSkin} open={skinOpen} setOpen={setSkinOpen} skin={skin} />
            <button className="classic-topbar-button" onClick={() => setDark((value) => !value)} type="button" aria-label="Toggle appearance">
              {dark
                ? <svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round"><path d="M20.7 15.2A8.5 8.5 0 0 1 8.8 3.3 8.5 8.5 0 1 0 20.7 15.2Z" /></svg>
                : <svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round"><circle cx="12" cy="12" r="3.8" /><path d="M12 2v2M12 20v2M4.93 4.93l1.42 1.42M17.65 17.65l1.42 1.42M2 12h2M20 12h2M4.93 19.07l1.42-1.42M17.65 6.35l1.42-1.42" /></svg>}
            </button>
            <button aria-label={`Notifications: ${alertSummary}`} className="classic-topbar-button classic-notification-button" onClick={() => { showSection("overview"); setNotice(alertSummary); }} type="button">
              <svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round"><path d="M18 8a6 6 0 0 0-12 0c0 7-3 7-3 9h18c0-2-3-2-3-9M10 21h4" /></svg>
              {alertCount > 0 && <i aria-hidden="true" />}
            </button>
            <a aria-label="Help and operator guide" className="classic-topbar-button classic-help-button" href="/help.html" rel="noreferrer" target="_blank"><svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round"><circle cx="12" cy="12" r="9.2" /><path d="M9.2 9.2a2.8 2.8 0 1 1 3.9 2.6c-.7.3-1.1.8-1.1 1.6" /><circle cx="12" cy="16.8" r=".5" /></svg><span>Help</span></a>
            <ProfileMenu changePassword={changePassword} logout={logout} error={error} setError={setError} openUpdates={() => setUpdateDialogOpen(true)} updateAvailable={Boolean(updates.status?.update_available)} />
          </div>
        </header>

        {error && <div className="dashboard-alert is-error" role="alert">{error}<button aria-label="Dismiss error" onClick={() => setError("")} type="button">✕</button></div>}
        {notice && <div className="dashboard-alert is-success" role="status">{notice}<button aria-label="Dismiss notice" onClick={() => setNotice("")} type="button">✕</button></div>}
        {system.recovery_required && <div className="dashboard-alert is-error" role="alert"><strong>Recovery required:</strong> {system.recovery_reason || "Canonical reconciliation failed."}<button className="button primary classic-alert-recover" disabled={busy} onClick={() => void triggerRecovery()} type="button">{busy ? "Recovering..." : "Reconcile now"}</button></div>}
        {pendingTx && <div className="dashboard-alert is-warning"><span>A connectivity-critical change is awaiting confirmation. Automatic rollback in {countdown}s.</span><button className="button primary" disabled={busy} onClick={() => void confirmPending()} type="button">Confirm access</button></div>}

        {active === "overview" && <ClassicOverview config={config} system={system} runtime={runtime} gatewaySummary={gatewaySummary} gatewayTargetCount={gatewaySettings.targets.length} memoryPercent={memoryPercent} diskPercent={diskPercent} lastRefresh={lastRefresh} health={health} healthUnavailable={healthUnavailable} />}
        {active === "security" && <SecuritySettings config={config} onError={setError} />}
        {active !== "security" && (
          <DashboardSections
            key={`dashboard-sections-${config.revision}`}
            active={active}
            applyConfig={applyConfig}
            markSectionSaved={markSectionSaved}
            savedSection={savedSection}
            applyGatewayMonitoring={applyGatewayMonitoring}
            busy={busy}
            config={config}
            createSnapshot={createSnapshot}
            gatewaySummary={gatewaySummary}
            gatewaySettings={gatewaySettings}
            leases={leases}
            load={load}
            restoreSnapshot={restoreSnapshot}
            deleteSnapshot={deleteSnapshot}
            setError={setError}
            snapshots={snapshots}
            submitCloudflare={submitCloudflare}
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
            runtime={runtime}
            onNavigate={showSection}
          />
        )}
      </main>

      {updateDialogOpen && <UpdateDialog controller={updates} onClose={() => setUpdateDialogOpen(false)} />}
    </div>
  );
}

export default function DashboardApp() {
  return <AuthGate><Dashboard /></AuthGate>;
}
