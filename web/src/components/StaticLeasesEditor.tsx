import { type FormEvent, Fragment, type KeyboardEvent, useState } from "react";
import { apiFetch } from "../lib/api";
import type { RouterConfig, StaticLease } from "../api-types";
import { MAC_PATTERN, insidePool, isValidIPv4 } from "./deviceReservation";

// dnsmasq already receives `dhcp-host=` for every static lease, so reservations
// have always worked on the appliance — there was simply no way to create one
// from the dashboard. Reserving an address is one of the most common things a
// home operator does, so it belongs next to the device table.

type Props = {
  config: RouterConfig;
  busy: boolean;
  applyConfig: (mutate: (next: RouterConfig) => void, success: string) => void;
  prefill?: { mac?: string; ip?: string; hostname?: string } | null;
  onPrefillConsumed?: () => void;
  liveLeases?: { mac: string }[];
};

const POOL_GUARD = "The router refuses a reservation that overlaps the pool.";

// validateLease checks one reservation against the rest. excludeId lets the
// inline row editor keep the lease it is editing out of the duplicate checks.
function validateLease(
  leases: StaticLease[],
  mac: string,
  ip: string,
  poolStart: string,
  poolEnd: string,
  excludeId: string | null,
): string {
  const normalisedMac = mac.trim().toLowerCase();
  const normalisedIp = ip.trim();
  if (!MAC_PATTERN.test(normalisedMac)) {
    return "MAC address must look like aa:bb:cc:dd:ee:ff.";
  }
  if (!isValidIPv4(normalisedIp)) {
    return "Reserved address must be a valid IPv4 address.";
  }
  if (leases.some((lease) => lease.id !== excludeId && lease.mac.toLowerCase() === normalisedMac)) {
    return "That MAC address already has a reservation.";
  }
  if (leases.some((lease) => lease.id !== excludeId && lease.ip_address === normalisedIp)) {
    return "That address is already reserved for another device.";
  }
  if (insidePool(normalisedIp, poolStart, poolEnd)) {
    return `${normalisedIp} is inside the dynamic DHCP pool (${poolStart}–${poolEnd}). ${POOL_GUARD}`;
  }
  return "";
}

export default function StaticLeasesEditor({ config, busy, applyConfig, prefill, onPrefillConsumed }: Props) {
  const leases: StaticLease[] = config.dhcp.static_leases || [];
  // Add form (bottom) — only used for new reservations.
  const [hostname, setHostname] = useState(prefill?.hostname ?? "");
  const [mac, setMac] = useState(prefill?.mac ?? "");
  const [ip, setIp] = useState(prefill?.ip ?? "");
  const [error, setError] = useState("");
  // Inline row editor — edits happen in place in the table, so the operator
  // never has to scroll down to the add form to change a reservation.
  const [editingId, setEditingId] = useState<string | null>(null);
  const [editHostname, setEditHostname] = useState("");
  const [editMac, setEditMac] = useState("");
  const [editIp, setEditIp] = useState("");
  const [editError, setEditError] = useState("");
  const [searchQuery, setSearchQuery] = useState("");

  const wakeOnLan = async (mac: string) => {
    try {
      await apiFetch("/api/v1/network/wol", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ mac }),
      });
    } catch (e) {
      console.error("WOL failed", e);
    }
  };

  const poolWarning = ip && insidePool(ip, config.dhcp.range_start, config.dhcp.range_end);

  const startEdit = (lease: StaticLease) => {
    setEditingId(lease.id);
    setEditHostname(lease.hostname || "");
    setEditMac(lease.mac);
    setEditIp(lease.ip_address);
    setEditError("");
  };

  const cancelEdit = () => {
    setEditingId(null);
    setEditHostname("");
    setEditMac("");
    setEditIp("");
    setEditError("");
  };

  const saveEdit = () => {
    if (!editingId) return;
    const problem = validateLease(
      leases,
      editMac,
      editIp,
      config.dhcp.range_start,
      config.dhcp.range_end,
      editingId,
    );
    if (problem) {
      setEditError(problem);
      return;
    }
    const targetId = editingId;
    const nextHostname = editHostname.trim();
    const nextMac = editMac.trim().toLowerCase();
    const nextIp = editIp.trim();
    applyConfig((next) => {
      next.dhcp = {
        ...next.dhcp,
        static_leases: (next.dhcp.static_leases || []).map((lease) =>
          lease.id === targetId
            ? { ...lease, hostname: nextHostname, mac: nextMac, ip_address: nextIp }
            : lease,
        ),
      };
    }, "DHCP reservation updated.");
    cancelEdit();
  };

  const handleEditKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    if (event.key === "Enter") {
      event.preventDefault();
      saveEdit();
    } else if (event.key === "Escape") {
      event.preventDefault();
      cancelEdit();
    }
  };

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const problem = validateLease(
      leases,
      mac,
      ip,
      config.dhcp.range_start,
      config.dhcp.range_end,
      null,
    );
    if (problem) {
      setError(problem);
      return;
    }
    setError("");
    applyConfig((next) => {
      next.dhcp = {
        ...next.dhcp,
        static_leases: [
          ...(next.dhcp.static_leases || []),
          {
            id: `lease-${Date.now().toString(36)}`,
            hostname: hostname.trim(),
            mac: mac.trim().toLowerCase(),
            ip_address: ip.trim(),
          },
        ],
      };
    }, "DHCP reservation saved.");
    setHostname("");
    setMac("");
    setIp("");
    onPrefillConsumed?.();
  };

  const remove = (id: string) => {
    applyConfig((next) => {
      next.dhcp = {
        ...next.dhcp,
        static_leases: (next.dhcp.static_leases || []).filter((lease) => lease.id !== id),
      };
    }, "DHCP reservation removed.");
  };

  const filteredLeases = searchQuery
    ? leases.filter((lease) =>
        (lease.hostname || "").toLowerCase().includes(searchQuery.toLowerCase()) ||
        lease.mac.toLowerCase().includes(searchQuery.toLowerCase()) ||
        lease.ip_address.toLowerCase().includes(searchQuery.toLowerCase())
      )
    : leases;

  return (
    <article className="card table-card static-leases">
      <div className="card-title-row">
        <div>
          <h3>DHCP reservations</h3>
          <p>Always hand the same address to a device. Written to dnsmasq as <code>dhcp-host</code>.</p>
        </div>
        <span className="quiet-meta">{filteredLeases.length} / {leases.length} reserved</span>
      </div>
      <div className="modern-search-wrapper static-lease-search">
        <svg className="modern-search-icon" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" aria-hidden="true"><circle cx="11" cy="11" r="8" /><path d="M21 21l-4.35-4.35" /></svg>
        <input
          type="text"
          placeholder="Search name, IP or MAC"
          value={searchQuery}
          onChange={(e) => setSearchQuery(e.target.value)}
          className="modern-search-input"
        />
      </div>

      <div className="elegant-table-container">
        <table className="elegant-device-table">
          <colgroup><col className="elegant-col-num" /><col className="elegant-col-mac" /><col className="elegant-col-ip" /><col className="elegant-col-actions" /></colgroup>
          <thead>
            <tr><th>Device</th><th>MAC</th><th>Reserved address</th><th className="elegant-th-actions">Action</th></tr>
          </thead>
          <tbody>
            {filteredLeases.length === 0 ? (
              <tr><td className="empty-state" colSpan={4}>No matching reservations.</td></tr>
            ) : (
              filteredLeases.map((lease) => {
                const isEditing = editingId === lease.id;
                const editPoolWarning =
                  isEditing && editIp && insidePool(editIp.trim(), config.dhcp.range_start, config.dhcp.range_end);
                return (
                  <Fragment key={lease.id}>
                    <tr className={isEditing ? "is-editing" : ""}>
                      <td className="elegant-cell-name">
                        {isEditing ? (
                          <input
                            aria-label="Device name"
                            autoFocus
                            className="row-edit-input"
                            disabled={busy}
                            onChange={(event) => setEditHostname(event.target.value)}
                            onKeyDown={handleEditKeyDown}
                            placeholder="living-room-tv"
                            value={editHostname}
                          />
                        ) : (
                          lease.hostname || "Unnamed device"
                        )}
                      </td>
                      <td className="elegant-cell-mac">
                        {isEditing ? (
                          <input
                            aria-label="MAC address"
                            className="row-edit-input row-edit-input-mono"
                            disabled={busy}
                            onChange={(event) => setEditMac(event.target.value)}
                            onKeyDown={handleEditKeyDown}
                            placeholder="aa:bb:cc:dd:ee:ff"
                            value={editMac}
                          />
                        ) : (
                          <code>{lease.mac}</code>
                        )}
                      </td>
                      <td className="elegant-cell-ip">
                        {isEditing ? (
                          <input
                            aria-label="Reserved address"
                            className="row-edit-input row-edit-input-mono"
                            disabled={busy}
                            onChange={(event) => setEditIp(event.target.value)}
                            onKeyDown={handleEditKeyDown}
                            placeholder="192.168.1.20"
                            value={editIp}
                          />
                        ) : (
                          <code>{lease.ip_address}</code>
                        )}
                      </td>
                      <td className="elegant-cell-actions">
                        <div className="device-row-actions">
                          {isEditing ? (
                            <>
                              <button className="button primary small" disabled={busy || Boolean(editPoolWarning)} onClick={saveEdit} type="button">Save</button>
                              <button className="button secondary small" disabled={busy} onClick={cancelEdit} type="button">Cancel</button>
                            </>
                          ) : (
                            <>
                              <button className="button secondary small" disabled={busy || editingId !== null} onClick={() => startEdit(lease)} type="button">Edit</button>
                              <button className="button secondary small" disabled={busy} onClick={() => void wakeOnLan(lease.mac)} title="Send a Wake-on-LAN magic packet" type="button">Wake</button>
                              <button className="button secondary small danger" disabled={busy} onClick={() => remove(lease.id)} type="button">Remove</button>
                            </>
                          )}
                        </div>
                      </td>
                    </tr>
                    {isEditing && (editError || editPoolWarning) && (
                      <tr className="row-edit-note-row">
                        <td colSpan={4}>
                          {editPoolWarning ? (
                            <p className="form-note is-warning" role="alert">
                              {editIp.trim()} is inside the DHCP pool ({config.dhcp.range_start}–{config.dhcp.range_end}). {POOL_GUARD}
                            </p>
                          ) : (
                            <p className="form-note is-error" role="alert">{editError}</p>
                          )}
                        </td>
                      </tr>
                    )}
                  </Fragment>
                );
              })
            )}
          </tbody>
        </table>
      </div>

      <form className="settings-form static-lease-form" onSubmit={submit}>
        <div className="form-grid three">
          <label className="field">
            <span>Device name</span>
            <input onChange={(event) => setHostname(event.target.value)} placeholder="living-room-tv" value={hostname} />
          </label>
          <label className="field">
            <span>MAC address</span>
            <input onChange={(event) => setMac(event.target.value)} placeholder="aa:bb:cc:dd:ee:ff" required value={mac} />
          </label>
          <label className="field">
            <span>Reserved address</span>
            <input onChange={(event) => setIp(event.target.value)} placeholder="192.168.1.20" required value={ip} />
          </label>
        </div>
        {poolWarning && (
          <p className="form-note is-warning" role="alert">
            {ip} is inside the DHCP pool ({config.dhcp.range_start}–{config.dhcp.range_end}). The router refuses a
            reservation that overlaps the pool — choose an address outside it, or shrink the pool first.
          </p>
        )}
        {error && <p className="form-note is-error" role="alert">{error}</p>}
        <div className="form-actions">
          <button className="button primary" disabled={busy || Boolean(poolWarning)} type="submit">Reserve address</button>
        </div>
      </form>
    </article>
  );
}
