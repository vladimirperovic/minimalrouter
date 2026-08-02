import { useEffect, useState } from "react";
import { apiFetch } from "../lib/api";

type Props = {
  changePassword: (e: React.FormEvent<HTMLFormElement>) => void;
  logout: () => void;
};

type AuditEvent = {
  id: string;
  event_type: string;
  actor: string;
  timestamp: string;
  details: Record<string, string>;
};

export default function SecuritySettings({ changePassword, logout }: Props) {
  const [lastLogin, setLastLogin] = useState<AuditEvent | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    void apiFetch("/api/v1/audit/events")
      .then(res => res.ok ? res.json() : Promise.reject())
      .then((body: { events?: AuditEvent[] }) => {
        const data = Array.isArray(body.events) ? body.events : [];
        const loginEvents = data.filter(e => e.event_type === "auth.login_succeeded");
        loginEvents.sort((a, b) => new Date(b.timestamp).getTime() - new Date(a.timestamp).getTime());
        if (loginEvents.length > 1) {
          setLastLogin(loginEvents[1]);
        } else if (loginEvents.length === 1) {
          setLastLogin(loginEvents[0]);
        }
      })
      .catch(() => undefined)
      .finally(() => setLoading(false));
  }, []);

  return (
    <section className="classic-dashboard-overview" aria-label="Security Settings">
      <article className="classic-hero-card">
        <div className="classic-hero-heading">
          <div>
            <div className="classic-kicker">Account</div>
            <h1>Security Settings</h1>
          </div>
        </div>
        <p className="classic-security-intro">
          Manage your password and review recent account activity.
        </p>

        <div className="classic-security-grid">
          <div className="classic-live-card classic-security-card">
            <h3>Change Administrator Password</h3>
            
            <form onSubmit={changePassword} className="classic-security-form">
              <div>
                <label>Current Password</label>
                <input 
                  type="password" 
                  name="old_password"
                  required
                />
              </div>
              <div>
                <label>New Password</label>
                <input 
                  type="password" 
                  name="new_password"
                  required
                />
              </div>
              <div>
                <label>Confirm New Password</label>
                <input 
                  type="password" 
                  name="confirm_password"
                  required
                />
              </div>
              <button 
                type="submit" 
                className="button primary" 
              >
                Update Password
              </button>
            </form>
          </div>

          <div className="classic-live-card classic-security-card classic-security-card-column">
            <h3>Session Information</h3>
            
            <div className="classic-security-session">
              <div className="classic-security-session-block">
                <label>Previous Login</label>
                <strong className="classic-security-value">
                  {loading ? "Loading..." : lastLogin ? new Date(lastLogin.timestamp).toLocaleString() : "First login"}
                </strong>
                {lastLogin && <small>From IP: {lastLogin.actor}</small>}
              </div>
              
              <div className="classic-security-logout">
                <strong>End current session</strong>
                <p>
                  Sign out of the minimalrouter dashboard on this device.
                </p>
                <button 
                  type="button" 
                  onClick={() => void logout()} 
                  className="button" 
                >
                  Sign Out
                </button>
              </div>
            </div>
          </div>
        </div>
      </article>
    </section>
  );
}
