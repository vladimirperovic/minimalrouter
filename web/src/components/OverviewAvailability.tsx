import { useCallback, useState } from "react";
import { apiFetch } from "../lib/api";
import { useVisiblePolling } from "../lib/useVisiblePolling";

type Availability = { sampled_hours: number; uptime_percent: number; outages: number };

export default function OverviewAvailability() {
  const [insights, setInsights] = useState<Availability | null>(null);
  const [unavailable, setUnavailable] = useState(false);
  const load = useCallback(async (signal: AbortSignal) => {
    try {
      const response = await apiFetch("/api/v1/gateway/insights", { signal });
      if (!response.ok) throw new Error("Availability unavailable");
      const body = await response.json();
      if (signal.aborted) return;
      if (!body || typeof body.available !== "boolean") throw new Error("Invalid availability");
      if (body.available && (!Number.isInteger(body.sampled_hours) || body.sampled_hours < 0
        || !Number.isFinite(body.uptime_percent) || body.uptime_percent < 0 || body.uptime_percent > 100
        || !Number.isInteger(body.outages) || body.outages < 0)) throw new Error("Invalid availability");
      setInsights(body.available ? body as Availability : null);
      setUnavailable(!body.available);
    } catch {
      if (!signal.aborted) { setInsights(null); setUnavailable(true); }
    }
  }, []);
  useVisiblePolling(load, 30_000);

  const measured = insights && insights.sampled_hours > 0 ? insights : null;
  const coverage = measured && (measured.sampled_hours < 24
    ? `${measured.sampled_hours}h sampled`
    : `${Math.floor(measured.sampled_hours / 24)}d ${measured.sampled_hours % 24}h sampled`);
  return <div className="overview-availability-row">
    <dt>30-day uptime</dt>
    <dd className={measured ? undefined : "is-info"}>{measured ? `${measured.uptime_percent.toFixed(2)}%` : unavailable ? "Unavailable" : "Collecting"}</dd>
    {measured && <small>{coverage} · {measured.outages} {measured.outages === 1 ? "outage" : "outages"}</small>}
  </div>;
}
