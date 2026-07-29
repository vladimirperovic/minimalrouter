import { useCallback, useEffect, useMemo, useState } from "react";
import { createPortal } from "react-dom";
import { apiFetch } from "../lib/api";

type AuditEvent = {
  id: string;
  event_type: string;
  actor: string;
  timestamp: string;
  details: Record<string, string>;
};

type RouterSystem = {
  status?: string;
  version?: string;
  runtime?: {
    available?: boolean;
    wan_connected?: boolean;
    uptime_seconds?: number;
  };
};

type RouterConfig = {
  firewall?: { stateful_firewall?: boolean };
  wireguard?: { enabled?: boolean };
  cloudflare?: { ddns_enabled?: boolean; tunnel_enabled?: boolean };
  wifi?: { enabled?: boolean };
  squid_proxy?: { enabled?: boolean };
  adguard?: { enabled?: boolean };
  qos?: { enabled?: boolean };
};

type LogFilter = "all" | "security" | "configuration" | "network" | "recovery";

function eventCategory(event: AuditEvent): Exclude<LogFilter, "all"> {
  const path = event.details?.path ?? "";
  const value = `${event.event_type} ${path}`.toLowerCase();
  if (value.includes("auth") || value.includes("session") || value.includes("login") || value.includes("totp")) {
    return "security";
  }
  if (value.includes("backup") || value.includes("restore") || value.includes("snapshot") || value.includes("update") || value.includes("factory")) {
    return "recovery";
  }
  if (value.includes("wireguard") || value.includes("firewall") || value.includes("dhcp") || value.includes("dns") || value.includes("wifi") || value.includes("squid") || value.includes("qos")) {
    return "network";
  }
  return "configuration";
}

function readableDetails(details: Record<string, string> = {}) {
  const entries = Object.entries(details)
    .filter(([, value]) => value !== "")
    .slice(0, 8);
  if (entries.length === 0) return "Recorded";
  return entries.map(([key, value]) => `${key}: ${value}`).join(" · ");
}

function formatUptime(seconds = 0) {
  if (seconds <= 0) return "Unavailable";
  const days = Math.floor(seconds / 86400);
  const hours = Math.floor((seconds % 86400) / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  return days > 0 ? `${days}d ${hours}h ${minutes}m` : `${hours}h ${minutes}m`;
}

function StatusCard({ label, enabled, detail }: { label: string; enabled: boolean; detail: string }) {
  return (
    <article className="card" style={{ padding: "18px 20px", minHeight: "112px" }}>
      <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", gap: "12px" }}>
        <strong style={{ fontSize: "14px" }}>{label}</strong>
        <span className={`status-label ${enabled ? "success" : ""}`}>
          <i className="status-dot" /> {enabled ? "Active" : "Off"}
        </span>
      </div>
      <p style={{ margin: "12px 0 0", color: "var(--text-secondary)", fontSize: "12.5px", lineHeight: 1.45 }}>
        {detail}
      </p>
    </article>
  );
}

export default function DashboardLogs() {
  const [navHost, setNavHost] = useState<HTMLElement | null>(null);
  const [sectionHost, setSectionHost] = useState<HTMLElement | null>(null);
  const [events, setEvents] = useState<AuditEvent[]>([]);
  const [system, setSystem] = useState<RouterSystem>({});
  const [config, setConfig] = useState<RouterConfig>({});
  const [filter, setFilter] = useState<LogFilter>("all");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [lastUpdated, setLastUpdated] = useState<Date | null>(null);
  const [logsActive, setLogsActive] = useState(false);

  useEffect(() => {
    let disposed = false;

    const mountHosts = () => {
      if (disposed) return;
      const nav = document.querySelector<HTMLElement>(".side-nav");
      const content = document.querySelector<HTMLElement>(".main-panel .content");
      if (!nav || !content) {
        setNavHost((current) => (current && !current.isConnected ? null : current));
        setSectionHost((current) => (current && !current.isConnected ? null : current));
        return;
      }

      let nextNavHost = nav.querySelector<HTMLElement>("[data-dashboard-logs-nav-host]");
      if (!nextNavHost) {
        nextNavHost = document.createElement("span");
        nextNavHost.dataset.dashboardLogsNavHost = "true";
        nextNavHost.style.display = "contents";
        nav.appendChild(nextNavHost);
      }

      let nextSectionHost = content.querySelector<HTMLElement>("[data-dashboard-logs-section-host]");
      if (!nextSectionHost) {
        nextSectionHost = document.createElement("div");
        nextSectionHost.dataset.dashboardLogsSectionHost = "true";
        nextSectionHost.style.display = "contents";
        content.appendChild(nextSectionHost);
      }

      setNavHost(nextNavHost);
      setSectionHost(nextSectionHost);
    };

    mountHosts();
    const observer = new MutationObserver(mountHosts);
    observer.observe(document.body, { childList: true, subtree: true });

    return () => {
      disposed = true;
      observer.disconnect();
      document.querySelector("[data-dashboard-logs-nav-host]")?.remove();
      document.querySelector("[data-dashboard-logs-section-host]")?.remove();
    };
  }, []);

  const loadLogs = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const [auditResponse, systemResponse, configResponse] = await Promise.all([
        apiFetch("/api/v1/audit/events?limit=250"),
        apiFetch("/api/v1/system"),
        apiFetch("/api/v1/config"),
      ]);
      if (!auditResponse.ok || !systemResponse.ok || !configResponse.ok) {
        throw new Error("Could not load the complete redacted log view");
      }
      const [auditPayload, systemPayload, configPayload] = await Promise.all([
        auditResponse.json(),
        systemResponse.json(),
        configResponse.json(),
      ]);
      setEvents(Array.isArray(auditPayload.events) ? auditPayload.events : []);
      setSystem(systemPayload ?? {});
      setConfig(configPayload ?? {});
      setLastUpdated(new Date());
    } catch (loadError) {
      setError(loadError instanceof Error ? loadError.message : "Logs are unavailable");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    if (!sectionHost) return;
    void loadLogs();
    const timer = window.setInterval(() => void loadLogs(), 30000);
    return () => window.clearInterval(timer);
  }, [loadLogs, sectionHost]);

  useEffect(() => {
    if (!sectionHost) return;
    const section = sectionHost.querySelector<HTMLElement>("#logs");
    if (!section) return;
    const observer = new IntersectionObserver(
      ([entry]) => {
        const active = entry.isIntersecting;
        setLogsActive(active);
        if (active) {
          document.querySelectorAll(".side-nav a.active").forEach((item) => item.classList.remove("active"));
        }
      },
      { rootMargin: "-20% 0px -60% 0px", threshold: 0 },
    );
    observer.observe(section);
    return () => observer.disconnect();
  }, [sectionHost]);

  const filteredEvents = useMemo(
    () => events.filter((event) => filter === "all" || eventCategory(event) === filter),
    [events, filter],
  );

  const exportLogs = () => {
    const payload = {
      exported_at: new Date().toISOString(),
      note: "Redacted Minimal Router activity log. Passwords, keys, tokens, request bodies and generated configurations are not included.",
      system,
      services: {
        stateful_firewall: config.firewall?.stateful_firewall === true,
        wireguard: config.wireguard?.enabled === true,
        cloudflare_ddns: config.cloudflare?.ddns_enabled === true,
        cloudflare_tunnel: config.cloudflare?.tunnel_enabled === true,
        wifi: config.wifi?.enabled === true,
        squid: config.squid_proxy?.enabled === true,
        dns_filter: config.adguard?.enabled === true,
        qos: config.qos?.enabled === true,
      },
      events: filteredEvents,
    };
    const blob = new Blob([JSON.stringify(payload, null, 2)], { type: "application/json;charset=utf-8" });
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = `minimalrouter-logs-${new Date().toISOString().replace(/[:.]/g, "-")}.json`;
    anchor.click();
    URL.revokeObjectURL(url);
  };

  const nav = (
    <a
      data-logs-link="true"
      className={logsActive ? "active" : ""}
      href="#logs"
      onClick={() => {
        setLogsActive(true);
        document.querySelector<HTMLButtonElement>(".sidebar-close")?.click();
        window.setTimeout(() => void loadLogs(), 0);
      }}
    >
      <span>11</span>
      Logs
    </a>
  );

  const section = (
    <section className="section-block" id="logs">
      <div className="section-heading">
        <div>
          <p className="eyebrow">Logs</p>
          <h2>Security, activity and service state.</h2>
        </div>
        <div style={{ display: "flex", gap: "10px", alignItems: "center", flexWrap: "wrap", justifyContent: "flex-end" }}>
          <span className="quiet-meta">
            {lastUpdated ? `Updated ${lastUpdated.toLocaleTimeString()}` : "Not loaded"} · auto refresh 30s
          </span>
          <button className="button secondary" type="button" onClick={() => void loadLogs()} disabled={loading}>
            {loading ? "Refreshing…" : "Refresh"}
          </button>
          <button className="button secondary" type="button" onClick={exportLogs} disabled={events.length === 0}>
            Export JSON
          </button>
        </div>
      </div>

      {error && (
        <div className="operation-error" role="alert" style={{ marginBottom: "20px" }}>
          <span>{error}</span>
        </div>
      )}

      <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fit, minmax(190px, 1fr))", gap: "14px", marginBottom: "24px" }}>
        <StatusCard
          label="Router API"
          enabled={lastUpdated !== null && system.runtime?.available !== false}
          detail={`${system.version ?? "Minimal Router"} · uptime ${formatUptime(system.runtime?.uptime_seconds)}`}
        />
        <StatusCard
          label="WAN"
          enabled={system.status === "Connected" || system.runtime?.wan_connected === true}
          detail={system.status === "Connected" ? "PPPoE and default route reported online." : "No verified WAN connection."}
        />
        <StatusCard
          label="WireGuard"
          enabled={config.wireguard?.enabled === true}
          detail="The only permitted new inbound WAN service when enabled."
        />
        <StatusCard
          label="Cloudflare DDNS"
          enabled={config.cloudflare?.ddns_enabled === true}
          detail={config.cloudflare?.ddns_enabled ? "inadyn hostname updates enabled." : "Disabled by default."}
        />
        <StatusCard
          label="Wi-Fi AP"
          enabled={config.wifi?.enabled === true}
          detail={config.wifi?.enabled ? "hostapd access point enabled." : "Disabled by default."}
        />
      </div>

      <article className="card table-card">
        <div className="card-title-row" style={{ gap: "16px", flexWrap: "wrap" }}>
          <div>
            <h3>Local audit events</h3>
            <p>Bounded SQLite records. Secrets, request bodies and generated service configurations are not logged.</p>
          </div>
          <div style={{ display: "flex", gap: "8px", flexWrap: "wrap" }}>
            {(["all", "security", "configuration", "network", "recovery"] as LogFilter[]).map((item) => (
              <button
                className={filter === item ? "button primary" : "button secondary"}
                type="button"
                key={item}
                onClick={() => setFilter(item)}
                style={{ padding: "7px 12px", fontSize: "12px", textTransform: "capitalize" }}
              >
                {item}
              </button>
            ))}
          </div>
        </div>
        <div className="table-scroll" style={{ maxHeight: "560px", overflowY: "auto" }}>
          <table>
            <caption className="sr-only">Minimal Router security and activity events</caption>
            <thead>
              <tr>
                <th>Time</th>
                <th>Category</th>
                <th>Event</th>
                <th>Actor</th>
                <th>Details</th>
              </tr>
            </thead>
            <tbody>
              {filteredEvents.length === 0 ? (
                <tr>
                  <td colSpan={5} style={{ textAlign: "center", color: "var(--text-tertiary)", padding: "28px" }}>
                    {loading ? "Loading logs…" : "No matching audit events yet."}
                  </td>
                </tr>
              ) : filteredEvents.map((event) => (
                <tr key={event.id}>
                  <td style={{ whiteSpace: "nowrap" }}>{new Date(event.timestamp).toLocaleString()}</td>
                  <td><span className="micro-status static"><i /> {eventCategory(event)}</span></td>
                  <td><code>{event.event_type}</code></td>
                  <td>{event.actor}</td>
                  <td style={{ minWidth: "280px", color: "var(--text-secondary)", fontSize: "12px" }}>{readableDetails(event.details)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </article>
    </section>
  );

  return (
    <>
      {navHost ? createPortal(nav, navHost) : null}
      {sectionHost ? createPortal(section, sectionHost) : null}
    </>
  );
}
