import { useCallback, useEffect, useState } from "react";
import type { ApplianceHealth } from "../api-types";
import { apiFetch } from "../lib/api";
import "./ApplianceHealthBanner.css";

const labels: Record<ApplianceHealth["state"], string> = {
  healthy: "Healthy",
  warning: "Warning",
  degraded: "Degraded",
  recovery_required: "Recovery required",
  unknown: "Unknown",
};

export default function ApplianceHealthBanner() {
  const [health, setHealth] = useState<ApplianceHealth | null>(null);

  const load = useCallback(async (signal?: AbortSignal) => {
    const response = await apiFetch("/api/v1/health", { signal });
    if (!response.ok) throw new Error(`Health summary unavailable (${response.status})`);
    setHealth((await response.json()) as ApplianceHealth);
  }, []);

  useEffect(() => {
    let active = true;
    let timer = 0;
    let controller: AbortController | null = null;
    const poll = async () => {
      controller?.abort();
      controller = new AbortController();
      try {
        await load(controller.signal);
      } catch (error) {
        if ((error as Error).name !== "AbortError" && active) setHealth(null);
      }
      if (active) timer = window.setTimeout(poll, 15000);
    };
    void poll();
    return () => {
      active = false;
      window.clearTimeout(timer);
      controller?.abort();
    };
  }, [load]);

  if (!health) {
    return <section className="appliance-health is-unknown" aria-live="polite"><div><span className="appliance-health-kicker">Appliance health</span><strong>Health summary unavailable</strong></div><p>The router is still exposing its normal status endpoints; refresh to retry the aggregate health view.</p></section>;
  }

  const attention = health.checks.filter((check) => check.state !== "healthy");
  return <section className={`appliance-health is-${health.state}`} aria-live="polite">
    <div className="appliance-health-heading">
      <div><span className="appliance-health-kicker">Appliance health</span><strong>{health.headline}</strong></div>
      <b>{labels[health.state]}</b>
    </div>
    {attention.length === 0 ? <p>Core routing, persistence and appliance signals currently report healthy.</p> : <div className="appliance-health-checks">
      {attention.slice(0, 4).map((check) => <article key={check.id}><span>{check.label}</span><strong>{labels[check.state]}</strong><p>{check.summary}</p></article>)}
      {attention.length > 4 && <small>+ {attention.length - 4} more health signal{attention.length - 4 === 1 ? "" : "s"}</small>}
    </div>}
  </section>;
}
