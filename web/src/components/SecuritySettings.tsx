import { useCallback, useEffect, useMemo, useState } from "react";
import { apiFetch } from "../lib/api";
import type { RouterConfig } from "../api-types";
import TrustedNetworksPanel from "./TrustedNetworksPanel";
import PortForwardsPanel from "./PortForwardsPanel";
import TOTPSettingsPanel from "./TOTPSettingsPanel";
import RecoveryToolsPanel from "./RecoveryToolsPanel";

type AuditEvent = {
  id: string;
  event_type: string;
  actor: string;
  timestamp: string;
  details: Record<string, string>;
};

type Props = {
  config: RouterConfig;
  onError: (message: string) => void;
};

function since(iso: string): string {
  const ms = Date.now() - new Date(iso).getTime();
  if (ms < 60000) return "just now";
  if (ms < 3600000) return `${Math.floor(ms / 60000)}m ago`;
  if (ms < 86400000) return `${Math.floor(ms / 3600000)}h ago`;
  return `${Math.floor(ms / 86400000)}d ago`;
}

function isSecurityEvent(event: AuditEvent): boolean {
  const value = event.event_type + " " + (event.details?.path ?? "");
  return /auth|session|login|totp|csrf|rate_limit|origin|firewall/.test(value);
}

export default function SecuritySettings({ config, onError }: Props) {
  const [events, setEvents] = useState<AuditEvent[]>([]);
  const [loading, setLoading] = useState(true);
  const [failureCount, setFailureCount] = useState(0);
  const [csrfCount, setCsrfCount] = useState(0);

  const load = useCallback(async () => {
    try {
      const response = await apiFetch("/api/v1/audit/events?limit=100");
      if (!response.ok) throw new Error(`Audit unavailable (${response.status})`);
      const body = await response.json();
      setEvents(Array.isArray(body.events) ? body.events : []);
    } catch {
      setEvents([]);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
    const timer = window.setInterval(() => void load(), 30000);
    return () => window.clearInterval(timer);
  }, [load]);

  useEffect(() => {
    setFailureCount(events.filter((e) => e.event_type === "auth.login_failed").length);
    setCsrfCount(events.filter((e) => /auth.(csrf|origin|cross_site)_rejected/.test(e.event_type)).length);
  }, [events]);

  const secure = events.filter(isSecurityEvent).slice(0, 50);
  const lastLogin = useMemo(() => {
    const logins = events.filter((e) => e.event_type === "auth.login_succeeded");
    return logins.length > 0 ? logins[0] : null;
  }, [events]);

  const posture = config.firewall.stateful_firewall ? "is-good" : "is-bad";

  return (
    <section className="classic-dashboard-overview classic-security-page" aria-label="Security">
      <article className="classic-hero-card">
        <section className="classic-security-command" aria-labelledby="security-posture-title">
          <div className="classic-hero-heading">
            <div className="classic-security-hero-copy">
              <span className="classic-security-hero-icon" aria-hidden="true"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round"><path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z" /><path d="m9 12 2 2 4-5" /></svg></span>
              <div>
                <div className="classic-kicker">Security posture</div>
                <h1 id="security-posture-title">{config.firewall.stateful_firewall ? "Protected by default" : "Protection needs attention"}</h1>
              </div>
            </div>
            <span className={`classic-state-pill ${posture}`}>
              <span className="classic-dot" />{config.firewall.stateful_firewall ? "Protected" : "Unprotected"}
            </span>
          </div>
          <p className="classic-security-intro">Stateful firewall policy, trusted administration paths and recent authentication activity in one local control surface.</p>

          <dl className="classic-security-command-facts">
            <div><dt>Firewall</dt><dd>{config.firewall.stateful_firewall ? "Enabled" : "Disabled"}</dd><small>stateful inspection</small></div>
            <div><dt>WAN policy</dt><dd>{config.firewall.default_wan_input_policy || "drop"}</dd><small>unsolicited input</small></div>
            <div><dt>Failed logins</dt><dd className={failureCount > 0 ? "is-bad" : "is-good"}>{failureCount}</dd><small>last 100 events</small></div>
            <div><dt>Rejected requests</dt><dd className={csrfCount > 0 ? "is-warn" : "is-good"}>{csrfCount}</dd><small>CSRF and origin</small></div>
            <div><dt>Last sign-in</dt><dd>{lastLogin ? since(lastLogin.timestamp) : loading ? "…" : "None"}</dd><small>{lastLogin ? lastLogin.actor : "no previous login"}</small></div>
          </dl>
        </section>

        <div className="classic-security-feed">
          <div className="classic-security-feed-heading"><div><h3>Recent security events</h3><p>Authentication and policy events retained locally by the appliance.</p></div><span>{secure.length} events</span></div>
          {loading && <p className="classic-security-empty">Loading events…</p>}
          {!loading && secure.length === 0 && <p className="classic-security-empty">No security events recorded.</p>}
          {!loading && secure.length > 0 && (
            <div className="classic-security-table-wrap"><table className="classic-security-table">
              <thead><tr><th>When</th><th>Event</th><th>Actor</th><th>Details</th></tr></thead>
              <tbody>{secure.map((event) => (
                <tr key={event.id}>
                  <td>{since(event.timestamp)}</td>
                  <td><code>{event.event_type}</code></td>
                  <td>{event.actor}</td>
                  <td>{Object.entries(event.details ?? {}).filter(([, v]) => v).slice(0, 3).map(([k, v]) => `${k}: ${v}`).join(" · ") || "Recorded"}</td>
                </tr>
              ))}</tbody>
            </table></div>
          )}
        </div>

        <TrustedNetworksPanel onError={onError} />
        <PortForwardsPanel onError={onError} />
        <TOTPSettingsPanel onError={onError} />
        <RecoveryToolsPanel config={config} onError={onError} />
      </article>
    </section>
  );
}
