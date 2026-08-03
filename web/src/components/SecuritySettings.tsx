import { useCallback, useEffect, useMemo, useState } from "react";
import { apiFetch } from "../lib/api";
import type { RouterConfig } from "../api-types";
import TrustedNetworksPanel from "./TrustedNetworksPanel";

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
    <section className="classic-dashboard-overview" aria-label="Security">
      <article className="classic-hero-card">
        <div className="classic-hero-heading">
          <div>
            <div className="classic-kicker">Router hardening</div>
            <h1>Security</h1>
          </div>
          <span className={`classic-state-pill ${posture}`}>
            <span className="classic-dot" />{config.firewall.stateful_firewall ? "Protected" : "Unprotected"}
          </span>
        </div>
        <p className="classic-security-intro">
          Firewall posture, recent sign-in activity, and blocked requests.
        </p>

        <div className="classic-security-grid">
          <div className="classic-live-card classic-security-card">
            <h3>Protection status</h3>
            <dl className="classic-security-stats">
              <div><dt>Stateful firewall</dt><dd className={config.firewall.stateful_firewall ? "is-good" : "is-bad"}>{config.firewall.stateful_firewall ? "Enabled" : "Disabled"}</dd></div>
              <div><dt>Default policy</dt><dd>{config.firewall.default_wan_input_policy || "drop"}</dd></div>
              <div><dt>Logged-in sessions</dt><dd>{loading ? "…" : "Active"}</dd></div>
              <div><dt>Failed logins</dt><dd className={failureCount > 0 ? "is-bad" : "is-good"}>{failureCount}</dd></div>
              <div><dt>CSRF / origin rejected</dt><dd className={csrfCount > 0 ? "is-warn" : "is-good"}>{csrfCount}</dd></div>
            </dl>
          </div>

          <div className="classic-live-card classic-security-card classic-security-card-column">
            <h3>Recent sign-in activity</h3>
            <div className="classic-security-session">
              {lastLogin ? (
                <>
                  <div className="classic-security-session-block">
                    <label>Previous login</label>
                    <strong className="classic-security-value">{since(lastLogin.timestamp)}</strong>
                    <small>From IP: {lastLogin.actor}</small>
                  </div>
                </>
              ) : (
                <div className="classic-security-session-block">
                  <small>No previous logins recorded.</small>
                </div>
              )}
            </div>
            <div className="classic-security-logout">
              <strong>Security event feed</strong>
              <p>Authentication failures and policy rejects from the last 100 events.</p>
            </div>
          </div>
        </div>

        <div className="classic-security-feed">
          <h3>Recent security events</h3>
          {loading && <p className="classic-security-empty">Loading events…</p>}
          {!loading && secure.length === 0 && <p className="classic-security-empty">No security events recorded.</p>}
          {!loading && secure.length > 0 && (
            <table className="classic-security-table">
              <thead><tr><th>When</th><th>Event</th><th>Actor</th><th>Details</th></tr></thead>
              <tbody>{secure.map((event) => (
                <tr key={event.id}>
                  <td>{since(event.timestamp)}</td>
                  <td><code>{event.event_type}</code></td>
                  <td>{event.actor}</td>
                  <td>{Object.entries(event.details ?? {}).filter(([, v]) => v).slice(0, 3).map(([k, v]) => `${k}: ${v}`).join(" · ") || "Recorded"}</td>
                </tr>
              ))}</tbody>
            </table>
          )}
        </div>

        <TrustedNetworksPanel onError={onError} />
      </article>
    </section>
  );
}