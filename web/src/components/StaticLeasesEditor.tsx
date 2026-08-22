import { type FormEvent, useState } from "react";
import { apiFetch } from "../lib/api";
import type { RouterConfig, StaticLease } from "../api-types";

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

const MAC_PATTERN = /^([0-9a-f]{2}:){5}[0-9a-f]{2}$/i;

function isValidIPv4(value: string): boolean {
  const parts = value.split(".");
  if (parts.length !== 4) return false;
  return parts.every((part) => /^\d{1,3}$/.test(part) && Number(part) >= 0 && Number(part) <= 255);
}

// A reservation inside the dynamic pool is the classic way to end up with two
// devices holding the same address, so warn before the router does.
function insidePool(ip: string, start: string, end: string): boolean {
  const toNumber = (value: string) =>
    value.split(".").reduce((total, octet) => total * 256 + Number(octet), 0);
  if (!isValidIPv4(ip) || !isValidIPv4(start) || !isValidIPv4(end)) return false;
  const target = toNumber(ip);
  return target >= toNumber(start) && target <= toNumber(end);
}

export default function StaticLeasesEditor({ config, busy, applyConfig, prefill, onPrefillConsumed }: Props) {
  const leases: StaticLease[] = config.dhcp.static_leases || [];
  const [hostname, setHostname] = useState(prefill?.hostname ?? "");
  const [mac, setMac] = useState(prefill?.mac ?? "");
  const [ip, setIp] = useState(prefill?.ip ?? "");
  const [editingId, setEditingId] = useState<string | null>(null);
  const [error, setError] = useState("");
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
    setHostname(lease.hostname || "");
    setMac(lease.mac);
    setIp(lease.ip_address);
    setError("");
  };

  const cancelEdit = () => {
    setEditingId(null);
    setHostname("");
    setMac("");
    setIp("");
    setError("");
  };

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setError("");
    const normalisedMac = mac.trim().toLowerCase();
    if (!MAC_PATTERN.test(normalisedMac)) {
      setError("MAC address must look like aa:bb:cc:dd:ee:ff.");
      return;
    }
    if (!isValidIPv4(ip.trim())) {
      setError("Reserved address must be a valid IPv4 address.");
      return;
    }
    if (leases.some((lease) => lease.id !== editingId && lease.mac.toLowerCase() === normalisedMac)) {
      setError("That MAC address already has a reservation.");
      return;
    }
    if (leases.some((lease) => lease.id !== editingId && lease.ip_address === ip.trim())) {
      setError("That address is already reserved for another device.");
      return;
    }
    if (editingId) {
      applyConfig((next) => {
        next.dhcp = {
          ...next.dhcp,
          static_leases: (next.dhcp.static_leases || []).map((lease) =>
            lease.id === editingId
              ? { ...lease, hostname: hostname.trim(), mac: normalisedMac, ip_address: ip.trim() }
              : lease,
          ),
        };
      }, "DHCP reservation updated.");
      cancelEdit();
      return;
    }
    applyConfig((next) => {
      next.dhcp = {
        ...next.dhcp,
        static_leases: [
          ...(next.dhcp.static_leases || []),
          {
            id: `lease-${Date.now().toString(36)}`,
            hostname: hostname.trim(),
            mac: normalisedMac,
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
              filteredLeases.map((lease) => (
                <tr key={lease.id}>
                  <td className="elegant-cell-name">{lease.hostname || "Unnamed device"}</td>
                  <td className="elegant-cell-mac"><code>{lease.mac}</code></td>
                  <td className="elegant-cell-ip"><code>{lease.ip_address}</code></td>
                  <td className="elegant-cell-actions">
                    <div className="device-row-actions">
                      <button className="button secondary small" disabled={busy} onClick={() => startEdit(lease)} type="button">Edit</button>
                      <button className="button secondary small" disabled={busy} onClick={() => void wakeOnLan(lease.mac)} title="Send a Wake-on-LAN magic packet" type="button">Wake</button>
                      <button className="button secondary small danger" disabled={busy} onClick={() => remove(lease.id)} type="button">Remove</button>
                    </div>
                  </td>
                </tr>
              ))
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
          <p className="form-note is-warning">
            {ip} is inside the DHCP pool ({config.dhcp.range_start}–{config.dhcp.range_end}). Reserve an address outside the
            pool, or shrink the pool, to avoid handing the same address to two devices.
          </p>
        )}
        {error && <p className="form-note is-error" role="alert">{error}</p>}
        <div className="form-actions">
          {editingId && <button className="button secondary" disabled={busy} onClick={cancelEdit} type="button">Cancel edit</button>}
          <button className="button primary" disabled={busy} type="submit">{editingId ? "Save changes" : "Reserve address"}</button>
        </div>
      </form>
    </article>
  );
}
