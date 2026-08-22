import { useCallback, useEffect, useState } from "react";
import { apiFetch } from "../lib/api";
import type { AccountingSnapshot, RouterConfig } from "../api-types";

// Per-device traffic is measured by two dynamic nftables sets in the forward
// chain and folded into calendar-month buckets by routerd. Only byte totals per
// LAN address are stored — no destinations, ports or payload — so this answers
// "who used the line this month" without becoming a household traffic log.

type Props = {
  config: RouterConfig;
  busy: boolean;
  applyConfig: (mutate: (next: RouterConfig) => void, success: string) => void;
};

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

function monthLabel(month: string): string {
  const [year, index] = month.split("-").map(Number);
  if (!year || !index) return month;
  return new Date(Date.UTC(year, index - 1, 1)).toLocaleDateString([], { month: "long", year: "numeric" });
}

export default function TrafficPanel({ config, busy, applyConfig }: Props) {
  const [snapshot, setSnapshot] = useState<AccountingSnapshot | null>(null);
  const [unavailable, setUnavailable] = useState(false);
  const [selectedMonth, setSelectedMonth] = useState<string | null>(null);

  const load = useCallback(async (signal?: AbortSignal) => {
    try {
      const response = await apiFetch("/api/v1/accounting?months=6", { signal });
      if (!response.ok) throw new Error(`Accounting unavailable (${response.status})`);
      setSnapshot((await response.json()) as AccountingSnapshot);
      setUnavailable(false);
    } catch (error) {
      if ((error as Error).name === "AbortError") return;
      setSnapshot(null);
      setUnavailable(true);
    }
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    void load(controller.signal);
    const timer = window.setInterval(() => void load(), 30000);
    return () => {
      controller.abort();
      window.clearInterval(timer);
    };
  }, [load]);

  const toggle = (enabled: boolean) => {
    if (!enabled && !window.confirm("Disable traffic accounting? Existing per-device history is deleted.")) return;
    applyConfig((next) => {
      next.accounting = { ...next.accounting, enabled, retention_months: next.accounting?.retention_months || 13 };
    }, enabled ? "Traffic accounting enabled." : "Traffic accounting disabled.");
  };

  const months = snapshot?.months ?? [];
  const active = months.find((month) => month.month === selectedMonth) ?? months[0];
  const activeIndex = months.findIndex((month) => month.month === active?.month);
  const previous = activeIndex >= 0 ? months[activeIndex + 1] : undefined;
  const delta = active && previous && previous.total_bytes > 0
    ? ((active.total_bytes - previous.total_bytes) / previous.total_bytes) * 100
    : null;

  return (
    <section className="dashboard-section" id="traffic">
      <div className="dashboard-section-heading has-facts">
        <div className="subpage-hero-head"><div><p className="eyebrow">Usage</p><h2>Traffic per device</h2><p className="section-copy">Monthly byte totals per LAN device, counted in the firewall forward chain. Only totals per address are stored — no destinations, ports or payload.</p></div><span className={`classic-status-chip ${config.accounting?.enabled ? "" : "is-off"}`}>Accounting {config.accounting?.enabled ? "On" : "Off"}</span></div>
        <dl className="subpage-hero-facts"><div><dt>Collection</dt><dd>{config.accounting?.enabled ? unavailable ? "Unavailable" : "Active" : "Disabled"}</dd><small>local firewall counters</small></div><div><dt>Current period</dt><dd>{active ? monthLabel(active.month) : "Collecting"}</dd><small>calendar month</small></div><div><dt>Total traffic</dt><dd>{active ? formatBytes(active.total_bytes) : "—"}</dd><small>download and upload</small></div><div><dt>Devices</dt><dd>{active?.devices.length || 0}</dd><small>{config.accounting?.retention_months || 13} months retained</small></div></dl>
      </div>

      <article className="service-inline-control"><div><strong>Per-device accounting</strong><p>Local byte totals only; destinations, ports and payloads are never retained.</p></div><label className="checkbox-row"><input checked={Boolean(config.accounting?.enabled)} onChange={(event) => toggle(event.target.checked)} type="checkbox" /><span>Count traffic per device</span></label></article>

      {config.accounting?.enabled && unavailable && (
        <div className="dashboard-callout">
          <strong>Traffic data unavailable.</strong>
          <p>The accounting store could not be read. No usage is shown rather than showing zeroes.</p>
        </div>
      )}

      {config.accounting?.enabled && !unavailable && months.length === 0 && (
        <div className="dashboard-callout">
          <strong>Collecting.</strong>
          <p>Counters are read once a minute. Totals appear after the first collection round following a configuration apply.</p>
        </div>
      )}

      {config.accounting?.enabled && active && (
        <>
          {months.length > 1 && (
            <div className="traffic-month-tabs" role="tablist" aria-label="Accounting month">
              {months.map((month) => (
                <button
                  aria-selected={month.month === active.month}
                  className={month.month === active.month ? "is-active" : ""}
                  key={month.month}
                  onClick={() => setSelectedMonth(month.month)}
                  role="tab"
                  type="button"
                >
                  {monthLabel(month.month)}
                </button>
              ))}
            </div>
          )}

          <article className="card table-card">
            <div className="card-title-row">
              <div>
                <h3>{monthLabel(active.month)}</h3>
                <p>{active.devices.length} devices · {formatBytes(active.total_bytes)} total{previous && delta !== null ? <span title={`Previous month: ${formatBytes(previous.total_bytes)}`}> · {delta >= 0 ? "+" : ""}{delta.toFixed(0)}% vs {monthLabel(previous.month)}</span> : ""}</p>
              </div>
              <button className="button secondary small" disabled={busy} onClick={() => void load()} type="button">Refresh</button>
            </div>
            <div className="table-scroll traffic-table-scroll">
              <table>
                <thead>
                  <tr><th>Device</th><th>Address</th><th>Download</th><th>Upload</th><th>Total</th><th>Share</th></tr>
                </thead>
                <tbody>
                  {active.devices.map((device) => {
                    const share = active.total_bytes > 0 ? (device.total_bytes / active.total_bytes) * 100 : 0;
                    return (
                      <tr key={device.address}>
                        <td>{device.hostname || "Unknown device"}</td>
                        <td><code>{device.address}</code></td>
                        <td>{formatBytes(device.rx_bytes)}</td>
                        <td>{formatBytes(device.tx_bytes)}</td>
                        <td><strong>{formatBytes(device.total_bytes)}</strong></td>
                        <td className="traffic-share">
                          <progress max="100" value={share} />
                          <small>{share.toFixed(1)}%</small>
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          </article>
        </>
      )}
    </section>
  );
}
