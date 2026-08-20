import { useCallback, useEffect, useMemo, useState } from "react";
import type { AccountingSnapshot, DeviceUsage, RouterConfig } from "../api-types";
import { apiFetch } from "../lib/api";

type Lease = { expires_at: number; mac: string; ip_address: string; hostname?: string };
type DevicePause = { ip: string; until_unix: number };
type DeviceRow = {
  key: string;
  hostname?: string;
  mac?: string;
  ip_address: string;
  expires_at?: number;
  online: boolean;
  last_seen_epoch?: number;
  is_new: boolean;
  liveLease?: Lease;
};

function formatRelativeFuture(timestamp: number) {
  const diff = timestamp * 1000 - Date.now();
  if (diff <= 0) return "Expired";
  const minutes = Math.floor(diff / 60000);
  if (minutes < 60) return `in ${Math.max(1, minutes)} min`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `in ${hours} h`;
  const days = Math.floor(hours / 24);
  return `in ${days} d`;
}

function formatLastSeen(timestamp?: number) {
  if (!timestamp) return "Previously seen";
  const seconds = Math.max(0, Math.floor(Date.now() / 1000) - timestamp);
  if (seconds < 60) return "just now";
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes} min ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours} h ago`;
  const days = Math.floor(hours / 24);
  if (days < 30) return `${days} d ago`;
  return new Date(timestamp * 1000).toLocaleDateString();
}

function pauseLabel(pause?: DevicePause) {
  if (!pause) return "";
  if (!pause.until_unix) return "Paused until resumed";
  const remaining = Math.max(0, pause.until_unix * 1000 - Date.now());
  const minutes = Math.max(1, Math.ceil(remaining / 60000));
  return `Paused · ${minutes} min left`;
}

function deviceIdentity(device: { mac?: string; address?: string }) {
  const mac = device.mac?.trim().toLowerCase();
  return mac || device.address || "";
}

export default function DeviceLeasesTable({ leases, config, onAddStatic }: { leases: Lease[]; config: RouterConfig; onAddStatic: (lease: Lease) => void }) {
  const [searchQuery, setSearchQuery] = useState("");
  const [accounting, setAccounting] = useState<AccountingSnapshot | null>(null);
  const [pauses, setPauses] = useState<DevicePause[]>([]);
  const [pauseMenuIP, setPauseMenuIP] = useState<string | null>(null);
  const [pauseBusyIP, setPauseBusyIP] = useState<string | null>(null);
  const [pauseError, setPauseError] = useState("");

  const loadActivity = useCallback(async (signal?: AbortSignal) => {
    if (!config.accounting?.enabled) {
      setAccounting(null);
      return;
    }
    try {
      const response = await apiFetch("/api/v1/accounting?months=2", { signal });
      if (!response.ok) return;
      setAccounting((await response.json()) as AccountingSnapshot);
    } catch (error) {
      if ((error as Error).name !== "AbortError") setAccounting(null);
    }
  }, [config.accounting?.enabled]);

  const loadPauses = useCallback(async (signal?: AbortSignal) => {
    try {
      const response = await apiFetch("/api/v1/devices/pauses", { signal });
      if (!response.ok) return;
      const body = await response.json() as { pauses?: DevicePause[] };
      setPauses(Array.isArray(body.pauses) ? body.pauses : []);
    } catch (error) {
      if ((error as Error).name !== "AbortError") setPauseError("Pause state unavailable");
    }
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    void loadActivity(controller.signal);
    void loadPauses(controller.signal);
    const timer = window.setInterval(() => {
      void loadActivity();
      void loadPauses();
    }, 30000);
    return () => {
      controller.abort();
      window.clearInterval(timer);
    };
  }, [loadActivity, loadPauses]);

  const setDevicePause = async (ip: string, seconds: number) => {
    setPauseBusyIP(ip);
    setPauseError("");
    try {
      const response = await apiFetch("/api/v1/devices/pause", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ ip, seconds }),
      });
      const body = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(body.error || `Pause failed (${response.status})`);
      setPauses(Array.isArray(body.pauses) ? body.pauses : []);
      setPauseMenuIP(null);
    } catch (error) {
      setPauseError(error instanceof Error ? error.message : "Pause failed");
    } finally {
      setPauseBusyIP(null);
    }
  };

  const resumeDevice = async (ip: string) => {
    setPauseBusyIP(ip);
    setPauseError("");
    try {
      const response = await apiFetch("/api/v1/devices/resume", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ ip, seconds: 0 }),
      });
      const body = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(body.error || `Resume failed (${response.status})`);
      setPauses(Array.isArray(body.pauses) ? body.pauses : []);
    } catch (error) {
      setPauseError(error instanceof Error ? error.message : "Resume failed");
    } finally {
      setPauseBusyIP(null);
    }
  };

  const pauseByIP = useMemo(() => new Map(pauses.map((pause) => [pause.ip, pause])), [pauses]);
  const staticMacs = useMemo(() => new Set((config.dhcp.static_leases || []).map((sl) => sl.mac.toLowerCase())), [config.dhcp.static_leases]);

  const rows = useMemo<DeviceRow[]>(() => {
    const currentDevices = accounting?.months?.[0]?.devices || [];
    const previousDevices = accounting?.months?.[1]?.devices || [];
    const previousIdentities = new Set(previousDevices.map(deviceIdentity).filter(Boolean));
    const usageByIdentity = new Map<string, DeviceUsage>();
    const usageByAddress = new Map<string, DeviceUsage>();
    currentDevices.forEach((device) => {
      const identity = deviceIdentity(device);
      if (identity) usageByIdentity.set(identity, device);
      usageByAddress.set(device.address, device);
    });

    const liveIdentities = new Set<string>();
    const result: DeviceRow[] = leases.map((lease) => {
      const identity = lease.mac.toLowerCase();
      liveIdentities.add(identity);
      const usage = usageByIdentity.get(identity) || usageByAddress.get(lease.ip_address);
      const lastSeen = usage?.last_seen_epoch || Math.floor(Date.now() / 1000);
      const newEnough = Math.floor(Date.now() / 1000) - lastSeen <= 24 * 60 * 60;
      return {
        key: identity || lease.ip_address,
        hostname: lease.hostname || usage?.hostname,
        mac: lease.mac || usage?.mac,
        ip_address: lease.ip_address,
        expires_at: lease.expires_at,
        online: true,
        last_seen_epoch: lastSeen,
        is_new: Boolean(config.accounting?.enabled && usage && newEnough && !previousIdentities.has(deviceIdentity(usage))),
        liveLease: lease,
      };
    });

    currentDevices.forEach((device) => {
      const identity = deviceIdentity(device);
      const alreadyLive = (identity && liveIdentities.has(identity)) || leases.some((lease) => lease.ip_address === device.address);
      if (alreadyLive) return;
      result.push({
        key: identity || device.address,
        hostname: device.hostname,
        mac: device.mac,
        ip_address: device.address,
        online: false,
        last_seen_epoch: device.last_seen_epoch,
        is_new: false,
      });
    });

    return result.sort((a, b) => {
      if (a.online !== b.online) return a.online ? -1 : 1;
      return (b.last_seen_epoch || 0) - (a.last_seen_epoch || 0);
    });
  }, [accounting, config.accounting?.enabled, leases]);

  const filteredRows = useMemo(() => {
    if (!searchQuery) return rows;
    const lower = searchQuery.toLowerCase();
    return rows.filter((row) =>
      (row.hostname && row.hostname.toLowerCase().includes(lower)) ||
      row.ip_address.includes(lower) ||
      (row.mac || "").toLowerCase().includes(lower)
    );
  }, [rows, searchQuery]);

  const onlineCount = rows.filter((row) => row.online).length;

  return (
    <section className="modern-device-section">
      <div className="modern-section-heading">
        <div className="modern-heading-titles">
          <h2>Connected devices</h2>
          <span className="modern-heading-sub">{onlineCount} online · {staticMacs.size} static reservation{staticMacs.size === 1 ? "" : "s"}{config.accounting?.enabled ? " · recent devices retained" : ""}</span>
        </div>
        <div className="modern-device-tools">
          <div className="modern-search-wrapper">
            <svg className="modern-search-icon" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" aria-hidden="true"><circle cx="11" cy="11" r="8" /><path d="M21 21l-4.35-4.35" /></svg>
            <input type="text" placeholder="Search name, IP or MAC" value={searchQuery} onChange={(e) => setSearchQuery(e.target.value)} className="modern-search-input" />
            {searchQuery && <button type="button" className="modern-search-clear" onClick={() => setSearchQuery("")} aria-label="Clear search">✕</button>}
          </div>
          <span className="modern-device-count">{searchQuery ? `${filteredRows.length} match${filteredRows.length === 1 ? "" : "es"}` : `${rows.length} known`}</span>
        </div>
      </div>
      {pauseError && <div className="device-pause-error" role="alert">{pauseError}</div>}

      <div className="elegant-table-container">
        <table className="elegant-device-table">
          <colgroup><col className="elegant-col-num" /><col /><col className="elegant-col-ip" /><col className="elegant-col-mac" /><col className="elegant-col-expires" /><col className="elegant-col-actions" /></colgroup>
          <thead><tr><th className="elegant-th-num">#</th><th>Host name</th><th>IP address</th><th>MAC address</th><th>Activity</th><th className="elegant-th-actions">Actions</th></tr></thead>
          <tbody>
            {filteredRows.length === 0 ? (
              <tr><td colSpan={6} className="elegant-empty"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round"><rect x="3" y="4" width="18" height="14" rx="2" /><path d="M3 9h18M8 4v14" /></svg><span>{searchQuery ? "No devices match your search." : "No devices connected yet."}</span></td></tr>
            ) : filteredRows.map((row, index) => {
              const isStatic = Boolean(row.mac && staticMacs.has(row.mac.toLowerCase()));
              const pause = pauseByIP.get(row.ip_address);
              const busy = pauseBusyIP === row.ip_address;
              return (
                <tr className={`${row.online ? "is-online" : "is-offline"}${pause ? " is-paused" : ""}`} key={row.key}>
                  <td className="elegant-cell-num">{String(index + 1).padStart(2, "0")}</td>
                  <td className="elegant-cell-name"><span className="elegant-device-identity">{row.hostname || "Unknown device"}{isStatic && <span className="elegant-badge-static">Static</span>}{row.is_new && <span className="device-activity-badge is-new">New</span>}{pause && <span className="device-activity-badge is-paused">Paused</span>}</span></td>
                  <td className="elegant-cell-ip">{row.ip_address}</td>
                  <td className="elegant-cell-mac">{row.mac || "Unknown"}</td>
                  <td className="elegant-cell-expires">
                    {pause ? <span className="device-activity-state is-paused">{pauseLabel(pause)}</span> : row.online ? <span className="device-activity-state is-online"><i aria-hidden="true" />Online{row.expires_at ? <small> · lease {formatRelativeFuture(row.expires_at)}</small> : null}</span> : <span className="device-activity-state is-offline" title={row.last_seen_epoch ? new Date(row.last_seen_epoch * 1000).toLocaleString() : undefined}>Last seen {formatLastSeen(row.last_seen_epoch)}</span>}
                  </td>
                  <td className="elegant-cell-actions">
                    <div className="device-row-actions">
                      {row.liveLease && !isStatic && <button type="button" onClick={() => onAddStatic(row.liveLease!)} className="elegant-btn-wol" title="Add as static DHCP reservation" aria-label={`Add ${row.hostname || row.mac} as static reservation`}><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M12 5v14M5 12h14" /></svg><span>Static</span></button>}
                      {pause ? (
                        <button className="device-pause-button is-resume" disabled={busy} onClick={() => void resumeDevice(row.ip_address)} type="button">{busy ? "Working…" : "Resume"}</button>
                      ) : (
                        <div className="device-pause-control">
                          <button className="device-pause-button" disabled={busy} onClick={() => setPauseMenuIP(pauseMenuIP === row.ip_address ? null : row.ip_address)} type="button">Pause Internet</button>
                          {pauseMenuIP === row.ip_address && <div className="device-pause-menu" role="menu"><button onClick={() => void setDevicePause(row.ip_address, 900)} type="button">15 min</button><button onClick={() => void setDevicePause(row.ip_address, 3600)} type="button">1 hour</button><button onClick={() => void setDevicePause(row.ip_address, 0)} type="button">Until resumed</button></div>}
                        </div>
                      )}
                    </div>
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </section>
  );
}
