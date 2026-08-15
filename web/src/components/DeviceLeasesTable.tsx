import { useMemo, useState } from "react";
import type { RouterConfig } from "../api-types";
import { apiFetch } from "../lib/api";

type Lease = { expires_at: number; mac: string; ip_address: string; hostname?: string };

function formatRelative(timestamp: number) {
  const diff = timestamp * 1000 - Date.now();
  if (diff <= 0) return "Expired";
  const minutes = Math.floor(diff / 60000);
  if (minutes < 60) return `in ${Math.max(1, minutes)} min`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `in ${hours} h`;
  const days = Math.floor(hours / 24);
  return `in ${days} d`;
}

export default function DeviceLeasesTable({ leases, config, onAddStatic }: { leases: Lease[]; config: RouterConfig; onAddStatic: (lease: Lease) => void }) {
  const [searchQuery, setSearchQuery] = useState("");

  const staticMacs = useMemo(() => new Set((config.dhcp.static_leases || []).map((sl) => sl.mac.toLowerCase())), [config.dhcp.static_leases]);

  const filteredLeases = useMemo(() => {
    if (!searchQuery) return leases;
    const lower = searchQuery.toLowerCase();
    return leases.filter((l) =>
      (l.hostname && l.hostname.toLowerCase().includes(lower)) ||
      l.ip_address.includes(lower) ||
      l.mac.includes(lower)
    );
  }, [leases, searchQuery]);

  return (
    <section className="modern-device-section">
      <div className="modern-section-heading">
        <div className="modern-heading-titles">
          <h2>Connected devices</h2>
          <span className="modern-heading-sub">Current LAN clients · {staticMacs.size} static reservation{staticMacs.size === 1 ? "" : "s"}</span>
        </div>
        <div className="modern-device-tools">
          <div className="modern-search-wrapper">
            <svg className="modern-search-icon" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" aria-hidden="true"><circle cx="11" cy="11" r="8" /><path d="M21 21l-4.35-4.35" /></svg>
            <input
              type="text"
              placeholder="Search name, IP or MAC"
              value={searchQuery}
              onChange={(e) => setSearchQuery(e.target.value)}
              className="modern-search-input"
            />
            {searchQuery && (
              <button type="button" className="modern-search-clear" onClick={() => setSearchQuery("")} aria-label="Clear search">✕</button>
            )}
          </div>
          <span className="modern-device-count">{searchQuery ? `${filteredLeases.length} match${filteredLeases.length === 1 ? "" : "es"}` : "Live lease table"}</span>
        </div>
      </div>

      <div className="elegant-table-container">
        <table className="elegant-device-table">
          <colgroup>
            <col className="elegant-col-num" />
            <col />
            <col className="elegant-col-ip" />
            <col className="elegant-col-mac" />
            <col className="elegant-col-expires" />
            <col className="elegant-col-actions" />
          </colgroup>
          <thead>
            <tr>
              <th className="elegant-th-num">#</th>
              <th>Host name</th>
              <th>IP address</th>
              <th>MAC address</th>
              <th>Expires</th>
              <th className="elegant-th-actions">Actions</th>
            </tr>
          </thead>
          <tbody>
            {filteredLeases.length === 0 ? (
              <tr>
                <td colSpan={6} className="elegant-empty">
                  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round"><rect x="3" y="4" width="18" height="14" rx="2" /><path d="M3 9h18M8 4v14" /></svg>
                  <span>{searchQuery ? "No devices match your search." : "No devices connected yet."}</span>
                </td>
              </tr>
            ) : filteredLeases.map((lease, index) => {
              const isStatic = staticMacs.has(lease.mac.toLowerCase());
              return (
                <tr key={`${lease.mac}-${lease.ip_address}`}>
                  <td className="elegant-cell-num">{String(index + 1).padStart(2, "0")}</td>
                  <td className="elegant-cell-name">
                    <span className="elegant-device-identity">
                      {lease.hostname || "Unknown device"}
                      {isStatic && <span className="elegant-badge-static">Static</span>}
                    </span>
                  </td>
                  <td className="elegant-cell-ip">{lease.ip_address}</td>
                  <td className="elegant-cell-mac">{lease.mac}</td>
                  <td className="elegant-cell-expires">{isStatic ? <span className="elegant-expires-static">Never</span> : <span title={new Date(lease.expires_at * 1000).toLocaleString()}>{formatRelative(lease.expires_at)}</span>}</td>
                  <td className="elegant-cell-actions">
                    <button
                      type="button"
                      onClick={() => onAddStatic(lease)}
                      className="elegant-btn-wol"
                      title="Add as static DHCP reservation"
                      aria-label={`Add ${lease.hostname || lease.mac} as static reservation`}
                    >
                      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M12 5v14M5 12h14" /></svg>
                      <span>Static</span>
                    </button>
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
