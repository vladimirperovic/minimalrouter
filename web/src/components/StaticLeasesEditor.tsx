import { type FormEvent, useState } from "react";
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
  const [error, setError] = useState("");

  const poolWarning = ip && insidePool(ip, config.dhcp.range_start, config.dhcp.range_end);

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
    if (leases.some((lease) => lease.mac.toLowerCase() === normalisedMac)) {
      setError("That MAC address already has a reservation.");
      return;
    }
    if (leases.some((lease) => lease.ip_address === ip.trim())) {
      setError("That address is already reserved for another device.");
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

  return (
    <article className="card table-card static-leases">
      <div className="card-title-row">
        <div>
          <h3>DHCP reservations</h3>
          <p>Always hand the same address to a device. Written to dnsmasq as <code>dhcp-host</code>.</p>
        </div>
        <span className="quiet-meta">{leases.length} reserved</span>
      </div>

      <div className="table-scroll">
        <table>
          <thead>
            <tr><th>Device</th><th>MAC</th><th>Reserved address</th><th>Action</th></tr>
          </thead>
          <tbody>
            {leases.length === 0 ? (
              <tr><td className="empty-state" colSpan={4}>No reservations yet.</td></tr>
            ) : (
              leases.map((lease) => (
                <tr key={lease.id}>
                  <td>{lease.hostname || "Unnamed device"}</td>
                  <td><code>{lease.mac}</code></td>
                  <td><code>{lease.ip_address}</code></td>
                  <td>
                    <button className="button secondary small" disabled={busy} onClick={() => remove(lease.id)} type="button">
                      Remove
                    </button>
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
          <button className="button primary" disabled={busy} type="submit">Reserve address</button>
        </div>
      </form>
    </article>
  );
}
