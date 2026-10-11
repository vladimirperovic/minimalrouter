import { useCallback, useState } from "react";
import type { TrafficInsights } from "../api-types";
import { apiFetch } from "../lib/api";
import { useVisiblePolling } from "../lib/useVisiblePolling";
import { usageBytes } from "./TrafficInsightsPanel";

export default function OverviewTopTraffic({ enabled }: { enabled: boolean }) {
  const [data, setData] = useState<TrafficInsights | null>(null);
  const [failed, setFailed] = useState(false);
  const load = useCallback(async (signal: AbortSignal) => {
    try {
      const response = await apiFetch("/api/v1/accounting/insights?period=today", { signal });
      if (!response.ok) throw new Error("Traffic unavailable");
      const body = await response.json() as TrafficInsights;
      if (!body || typeof body.available !== "boolean" || typeof body.enabled !== "boolean" || body.period !== "today"
        || !Array.isArray(body.points) || !Array.isArray(body.devices)
        || !body.points.every(point => point && Number.isFinite(point.samples) && point.samples >= 0)
        || !body.devices.every(device => device && typeof device.address === "string" && device.address.length > 0
          && (device.hostname === undefined || typeof device.hostname === "string")
          && Number.isFinite(device.total_bytes) && device.total_bytes >= 0)) throw new Error("Invalid traffic data");
      if (!signal.aborted) { setData(body); setFailed(false); }
    } catch { if (!signal.aborted) { setData(null); setFailed(true); } }
  }, []);
  useVisiblePolling(load, 30_000, enabled);
  const current = enabled && data?.available && data.enabled ? data : null;
  const observed = current?.points.some(point => point.samples > 0);
  const top = observed ? [...current!.devices].filter(device => device.total_bytes > 0)
    .sort((a, b) => b.total_bytes - a.total_bytes || a.address.localeCompare(b.address)).slice(0, 3) : [];
  const collected = current?.collected_at ? Date.parse(current.collected_at) : NaN;
  const delayed = current && (!Number.isFinite(collected) || Date.now() - collected > 15 * 60_000);
  const message = !enabled || data?.enabled === false ? "Accounting is off. Enable it in Traffic."
    : failed || data?.available === false ? "Traffic usage unavailable."
    : !data ? "Loading measured usage…" : !observed ? "Waiting for traffic samples." : "No device traffic recorded today.";
  return <section className="overview-top-traffic" aria-label="Top traffic consumers">
    <header><h3>Top devices <small>Today · UTC</small></h3><a href="#traffic">{!enabled ? "Enable accounting" : "All traffic"} →</a></header>
    {top.length > 0 ? <ol>{top.map(device => <li key={device.address}>
      <span title={`${device.hostname || device.address} · ${device.address}`}>{device.hostname || device.address}</span>
      <strong>{usageBytes(device.total_bytes)}</strong>
    </li>)}</ol> : <p>{message}</p>}
    {top.length > 0 && <p className="overview-traffic-note">{delayed ? "Collection delayed · last sample " + (Number.isFinite(collected) ? new Date(collected).toLocaleString() : "unavailable") : "Download + upload · sampled every 5 min"}</p>}
  </section>;
}
