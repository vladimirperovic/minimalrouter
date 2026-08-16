import { useCallback, useEffect, useMemo, useState } from "react";
import { apiFetch } from "../lib/api";
import "./BootActivityPanel.css";

type Boot = {
  id: string;
  started_at: string;
  completed: boolean;
  readiness: {
    management_seconds?: number;
    pppoe_seconds?: number;
    dns_seconds?: number;
    internet_seconds?: number;
    wireguard_seconds?: number;
  };
};

type ActivityRow = {
  label: string;
  detail: string;
  seconds?: number;
  state: "done" | "active" | "waiting";
};

type Props = {
  pppoeEnabled: boolean;
  wireGuardEnabled: boolean;
};

function pendingRow(label: string, active: string, completed: boolean): ActivityRow {
  return {
    label,
    detail: completed ? "Not reached during the 10-minute startup capture" : active,
    state: completed ? "waiting" : "active",
  };
}

export default function BootActivityPanel({ pppoeEnabled, wireGuardEnabled }: Props) {
  const [boot, setBoot] = useState<Boot | null>(null);
  const [unavailable, setUnavailable] = useState(false);

  const load = useCallback(async () => {
    try {
      const response = await apiFetch("/api/v1/startup/boots");
      if (!response.ok) throw new Error(`startup timeline unavailable (${response.status})`);
      const body = await response.json() as { boots?: Boot[] };
      setBoot(Array.isArray(body.boots) && body.boots.length > 0 ? body.boots[0] : null);
      setUnavailable(false);
    } catch {
      setUnavailable(true);
    }
  }, []);

  useEffect(() => { void load(); }, [load]);

  useEffect(() => {
    if (!boot || boot.completed) return;
    const timer = window.setInterval(() => void load(), 3000);
    return () => window.clearInterval(timer);
  }, [boot, load]);

  const rows = useMemo<ActivityRow[]>(() => {
    if (!boot) return [];
    const result: ActivityRow[] = [
      { label: "System", detail: "Router services started", seconds: 0, state: "done" },
    ];
    const readiness = boot.readiness || {};
    if (readiness.management_seconds !== undefined) {
      result.push({ label: "Management", detail: "Dashboard and API ready", seconds: readiness.management_seconds, state: "done" });
    } else {
      result.push(pendingRow("Management", "Reconciling configuration…", boot.completed));
    }
    if (pppoeEnabled) {
      if (readiness.pppoe_seconds !== undefined) {
        result.push({ label: "PPPoE", detail: "WAN session connected", seconds: readiness.pppoe_seconds, state: "done" });
      } else {
        result.push(pendingRow("PPPoE", "Connecting to provider…", boot.completed));
      }
    }
    if (readiness.dns_seconds !== undefined) {
      result.push({ label: "DNS", detail: "Resolver ready", seconds: readiness.dns_seconds, state: "done" });
    } else {
      result.push(pendingRow("DNS", "Waiting for resolver…", boot.completed));
    }
    if (readiness.internet_seconds !== undefined) {
      result.push({ label: "Internet", detail: "Outbound path verified", seconds: readiness.internet_seconds, state: "done" });
    } else {
      result.push(pendingRow("Internet", "Verifying external connectivity…", boot.completed));
    }
    if (wireGuardEnabled) {
      if (readiness.wireguard_seconds !== undefined) {
        result.push({ label: "WireGuard", detail: "Tunnel interface ready", seconds: readiness.wireguard_seconds, state: "done" });
      } else {
        result.push(pendingRow("WireGuard", "Waiting for tunnel interface…", boot.completed));
      }
    }
    return result;
  }, [boot, pppoeEnabled, wireGuardEnabled]);

  const live = Boolean(boot && !boot.completed);

  return (
    <section className="boot-activity" aria-labelledby="boot-activity-title">
      <header className="boot-activity-head">
        <div>
          <span className="boot-activity-kicker">Startup trace</span>
          <h2 id="boot-activity-title">Boot activity</h2>
          <p>Readable service milestones — credentials and raw command output are never shown.</p>
        </div>
        <div className="boot-activity-actions">
          <span className={`boot-activity-state ${live ? "is-live" : ""}`}><i aria-hidden="true" />{live ? "Live" : "Last boot"}</span>
          <a href="#logs">Details →</a>
        </div>
      </header>

      <div className="boot-terminal" role="log" aria-live="polite">
        {unavailable ? (
          <div className="boot-terminal-row is-waiting"><span className="boot-prompt">›</span><code>Startup activity is unavailable</code></div>
        ) : rows.length === 0 ? (
          <div className="boot-terminal-row is-active"><span className="boot-prompt">›</span><code>Waiting for the first startup capture…</code></div>
        ) : rows.map((row) => (
          <div className={`boot-terminal-row is-${row.state}`} key={row.label}>
            <span className="boot-prompt">›</span>
            <code><b>{row.label}</b> {row.detail}</code>
            <span className="boot-terminal-time">{row.seconds !== undefined ? `+${row.seconds}s` : row.state === "active" ? "…" : "—"}</span>
          </div>
        ))}
      </div>
    </section>
  );
}
