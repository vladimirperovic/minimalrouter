import { useCallback, useEffect, useState } from "react";
import type { RefObject } from "react";
import { apiFetch } from "../lib/api";
import type { ApplianceHealth, ApplianceHealthCheck, ApplianceHealthState } from "../api-types";

// The appliance-health API has existed since the health package landed, but the
// dashboard never called it: every service chip inferred health from
// configuration instead of from measured state, and two of them were hardcoded
// green. This banner is the single place that answers "does the router need
// attention?" and it only ever reports what /api/v1/health measured.

const STATE_LABEL: Record<ApplianceHealthState, string> = {
  healthy: "All systems normal",
  warning: "Needs attention",
  degraded: "Degraded",
  recovery_required: "Recovery required",
  unknown: "Status unknown",
};

const STATE_CLASS: Record<ApplianceHealthState, string> = {
  healthy: "is-good",
  warning: "is-warning",
  degraded: "is-warning",
  recovery_required: "is-bad",
  unknown: "is-unknown",
};

// Severity order matches internal/health: unknown is never folded into healthy.
const STATE_RANK: Record<ApplianceHealthState, number> = {
  recovery_required: 4,
  degraded: 3,
  warning: 2,
  unknown: 1,
  healthy: 0,
};

function needsAttention(check: ApplianceHealthCheck): boolean {
  return check.state !== "healthy";
}

export function useApplianceHealth(pollMs = 15000) {
  const [health, setHealth] = useState<ApplianceHealth | null>(null);
  const [unavailable, setUnavailable] = useState(false);

  const load = useCallback(async (signal?: AbortSignal) => {
    try {
      const response = await apiFetch("/api/v1/health", { signal });
      if (!response.ok) throw new Error(`Health unavailable (${response.status})`);
      const payload = (await response.json()) as Partial<ApplianceHealth> | null;
      // A 200 carrying a body we cannot read is still an unmeasured router. Without
      // this guard a truncated or older-firmware payload reaches the render as
      // health.checks === undefined, which throws and blanks the whole dashboard.
      if (!payload || typeof payload.state !== "string" || !Array.isArray(payload.checks)) {
        throw new Error("Health response is missing measured checks");
      }
      setHealth(payload as ApplianceHealth);
      setUnavailable(false);
    } catch (error) {
      if ((error as Error).name === "AbortError") return;
      // Missing data is shown as unavailable, never simulated as healthy.
      setHealth(null);
      setUnavailable(true);
    }
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    void load(controller.signal);
    const timer = window.setInterval(() => void load(), pollMs);
    return () => {
      controller.abort();
      window.clearInterval(timer);
    };
  }, [load, pollMs]);

  return { health, unavailable, reload: load };
}

type Props = {
  health: ApplianceHealth | null;
  unavailable: boolean;
  onShowDetails?: () => void;
};

export default function HealthBanner({ health, unavailable, onShowDetails }: Props) {
  if (unavailable || !health) {
    return (
      <section className="health-banner is-unknown" aria-label="Appliance health">
        <div className="health-banner-main">
          <span className="health-banner-dot" aria-hidden="true" />
          <div>
            <strong>Appliance health unavailable</strong>
            <p>The health endpoint could not be read. This is not a healthy result — it means the router could not be measured.</p>
          </div>
        </div>
      </section>
    );
  }

  const attention = health.checks.filter(needsAttention).sort((a, b) => STATE_RANK[b.state] - STATE_RANK[a.state]);
  const generated = new Date(health.generated_at);

  return (
    <section className={`health-banner ${STATE_CLASS[health.state]}`} aria-label="Appliance health">
      <div className="health-banner-main">
        <span className="health-banner-dot" aria-hidden="true" />
        <div className="health-banner-copy">
          <strong>{STATE_LABEL[health.state]}</strong>
          <p>{health.headline}</p>
        </div>
        <div className="health-banner-meta">
          <span>
            {attention.length === 0
              ? `${health.checks.length} checks passing`
              : `${attention.length} of ${health.checks.length} need attention`}
          </span>
          <small>
            {Number.isNaN(generated.getTime())
              ? "Timestamp unavailable"
              : `Checked ${generated.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}`}
          </small>
        </div>
        <button className="health-banner-toggle" onClick={onShowDetails} type="button">
          {attention.length > 0 ? "Review checks" : "View checks"}
        </button>
      </div>
    </section>
  );
}

type HealthCheckDetailsProps = Props & {
  sectionRef?: RefObject<HTMLElement | null>;
};

export function HealthCheckDetails({ health, unavailable, sectionRef }: HealthCheckDetailsProps) {
  if (unavailable || !health) return null;

  const attention = health.checks.filter(needsAttention).sort((a, b) => STATE_RANK[b.state] - STATE_RANK[a.state]);

  return (
    <section className="health-checks-section" id="system-health-checks" aria-labelledby="system-health-checks-title" ref={sectionRef}>
      <header className="health-checks-header">
        <div>
          <span className="health-checks-kicker">Measured status</span>
          <h2 id="system-health-checks-title">System checks</h2>
          <p>{attention.length > 0 ? "Review the checks that need follow-up when convenient." : "All measured checks are currently passing."}</p>
        </div>
        <span className="health-checks-count">{health.checks.length} checks</span>
      </header>
      <ul className="health-check-list">
        {health.checks.map((check) => (
          <li className={`health-check ${STATE_CLASS[check.state]}`} key={check.id}>
            <span className="health-check-dot" aria-hidden="true" />
            <div>
              <strong>{check.label}</strong>
              <p>{check.summary}</p>
            </div>
            <em>{check.state.replace("_", " ")}</em>
          </li>
        ))}
      </ul>
    </section>
  );
}
