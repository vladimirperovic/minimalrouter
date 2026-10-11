import { useCallback, useState } from "react";
import { apiFetch } from "../lib/api";
import { useVisiblePolling } from "../lib/useVisiblePolling";
import { HistoryBars } from "./TrafficInsightsPanel";

type Activity = {
  available: boolean;
  collected_at: string | null;
  allowed: number;
  blocked: number;
  points: Array<{
    start: string;
    allowed: number;
    blocked: number;
    samples: number;
  }>;
};
const packets = (n: number) =>
  new Intl.NumberFormat(undefined, {
    notation: "compact",
    maximumFractionDigits: 2,
  }).format(n);

export default function FirewallActivityPanel({
  connections,
  revision,
}: {
  connections: number | null;
  revision: number;
}) {
  const [data, setData] = useState<Activity | null>(null);
  const [error, setError] = useState("");
  const [metric, setMetric] = useState<"allowed" | "blocked">("blocked");
  const load = useCallback(async (signal: AbortSignal) => {
    try {
      const response = await apiFetch("/api/v1/firewall/activity", { signal });
      if (!response.ok) throw Error("Firewall activity is unavailable.");
      const body = (await response.json()) as Activity;
      if (!Array.isArray(body.points))
        throw Error("Firewall history is not available on this firmware.");
      if (!signal.aborted) {
        setData(body);
        setError("");
      }
    } catch (e) {
      if (!signal.aborted)
        setError(e instanceof Error ? e.message : "Activity unavailable.");
    }
  }, []);
  useVisiblePolling(load, 30000, true, String(revision));
  const observed = data?.points.some((p) => p.samples > 0);
  return (
    <article className={`fw-panel firewall-activity fw-activity is-${metric}`}>
      <header className="fw-panel-head">
        <div>
          <p className="fw-eyebrow">Measured traffic · 24 hours</p>
          <h3>Your firewall, at work.</h3>
          <p>See allowed traffic and blocked packets separately.</p>
        </div>
        <span
          className={`fw-badge ${data?.available && !error ? "is-good" : ""}`}
        >
          {data?.available && !error
            ? "Collecting"
            : data
              ? "Delayed"
              : "Waiting for samples"}
        </span>
      </header>
      <div className="fw-activity-stats">
        <button
          className={metric === "blocked" ? "is-selected" : ""}
          aria-pressed={metric === "blocked"}
          onClick={() => setMetric("blocked")}
        >
          <span>Blocked packets</span>
          <strong>{observed ? packets(data!.blocked) : "—"}</strong>
          <small>Inspect blocked traffic ↗</small>
        </button>
        <button
          className={metric === "allowed" ? "is-selected" : ""}
          aria-pressed={metric === "allowed"}
          onClick={() => setMetric("allowed")}
        >
          <span>Allowed packets</span>
          <strong>{observed ? packets(data!.allowed) : "—"}</strong>
          <small>Inspect allowed traffic ↗</small>
        </button>
        <div>
          <span>Active connections</span>
          <strong>{connections === null ? "—" : packets(connections)}</strong>
          <small>Latest dashboard observation</small>
        </div>
      </div>
      {error && (
        <p role="alert" className="fw-error">
          {error}{" "}
          {data
            ? "Showing the last available history."
            : "Packet counts are unknown."}
        </p>
      )}
      {data && !data.available && (
        <p className="fw-caution fw-inset">
          Collection is delayed. Saved history is still shown. Last sample:{" "}
          {data.collected_at
            ? new Date(data.collected_at).toLocaleString()
            : "unknown"}
          .
        </p>
      )}
      {!observed && (
        <p className="fw-empty">
          Waiting for measured history. The first interval needs two collection
          rounds.
        </p>
      )}
      {data && (
        <div className="fw-chart">
          <div className="fw-chart-caption">
            <strong>
              {metric === "blocked" ? "Blocked" : "Allowed"} packets
            </strong>
            <span>30-minute intervals · UTC</span>
          </div>
          <HistoryBars
            points={data.points.map((p) => ({
              value: p[metric],
              observed: p.samples > 0,
            }))}
            labels={data.points.map((p) =>
              new Date(p.start).toLocaleTimeString([], {
                hour: "2-digit",
                minute: "2-digit",
                hour12: false,
                timeZone: "UTC",
              }),
            )}
            format={(n) => `${packets(n)} ${metric} packets`}
          />
        </div>
      )}
      <p className="fw-note fw-inset">
        Input and forwarded packets, sampled every minute. Gaps mean no samples;
        resets and interrupted intervals are excluded. Blocked packets are not
        necessarily attacks. Each view uses its own vertical scale.
      </p>
    </article>
  );
}
