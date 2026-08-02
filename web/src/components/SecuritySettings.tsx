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

  useEffect(() => {
    void apiFetch("/api/v1/audit/events")
      .then(res => res.ok ? res.json() : Promise.reject())
      .then((data: AuditEvent[]) => {
        if (!Array.isArray(data)) return;
        const loginEvents = data.filter(e => e.event_type === "auth.login_succeeded");
        loginEvents.sort((a, b) => new Date(b.timestamp).getTime() - new Date(a.timestamp).getTime());
        if (loginEvents.length > 1) {
          setLastLogin(loginEvents[1]);
        } else if (loginEvents.length === 1) {
          setLastLogin(loginEvents[0]);
        }
      })
      .catch(() => undefined);
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
        <p style={{ color: "var(--classic-muted)", fontSize: "14px", marginTop: "10px", marginBottom: "30px" }}>
          Manage your password and review recent account activity.
        </p>

        <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fit, minmax(300px, 1fr))", gap: "30px" }}>
          <div className="classic-live-card" style={{ display: "block", minHeight: "auto", padding: "30px" }}>
            <h3 style={{ fontSize: "18px", fontWeight: 600, marginBottom: "20px" }}>Change Administrator Password</h3>
            
            <form onSubmit={changePassword} style={{ display: "flex", flexDirection: "column", gap: "15px" }}>
              <div>
                <label style={{ display: "block", fontSize: "12px", color: "var(--classic-muted)", marginBottom: "5px" }}>Current Password</label>
                <input 
                  type="password" 
                  name="old_password"
                  required
                  style={{ width: "100%", padding: "10px", borderRadius: "8px", border: "1px solid var(--classic-border)", background: "var(--classic-panel)", color: "var(--classic-text)" }}
                />
              </div>
              <div>
                <label style={{ display: "block", fontSize: "12px", color: "var(--classic-muted)", marginBottom: "5px" }}>New Password</label>
                <input 
                  type="password" 
                  name="new_password"
                  required
                  style={{ width: "100%", padding: "10px", borderRadius: "8px", border: "1px solid var(--classic-border)", background: "var(--classic-panel)", color: "var(--classic-text)" }}
                />
              </div>
              <div>
                <label style={{ display: "block", fontSize: "12px", color: "var(--classic-muted)", marginBottom: "5px" }}>Confirm New Password</label>
                <input 
                  type="password" 
                  name="confirm_password"
                  required
                  style={{ width: "100%", padding: "10px", borderRadius: "8px", border: "1px solid var(--classic-border)", background: "var(--classic-panel)", color: "var(--classic-text)" }}
                />
              </div>
              <button 
                type="submit" 
                className="button primary" 
                style={{ alignSelf: "flex-start", marginTop: "10px" }}
              >
                Update Password
              </button>
            </form>
          </div>

          <div className="classic-live-card" style={{ display: "flex", flexDirection: "column", minHeight: "auto", padding: "30px" }}>
            <h3 style={{ fontSize: "18px", fontWeight: 600, marginBottom: "20px" }}>Session Information</h3>
            
            <div style={{ flex: 1 }}>
              <div style={{ marginBottom: "20px" }}>
                <span style={{ display: "block", fontSize: "12px", color: "var(--classic-muted)", marginBottom: "5px" }}>Previous Login</span>
                <strong style={{ fontSize: "15px" }}>
                  {lastLogin ? new Date(lastLogin.timestamp).toLocaleString() : "Loading..."}
                </strong>
                {lastLogin && <small style={{ display: "block", color: "var(--classic-muted)", marginTop: "3px" }}>From IP: {lastLogin.actor}</small>}
              </div>
              
              <div style={{ padding: "15px", background: "var(--classic-panel)", border: "1px solid var(--classic-border)", borderRadius: "8px", marginTop: "20px" }}>
                <strong style={{ display: "block", fontSize: "13px", color: "var(--classic-text)" }}>End current session</strong>
                <p style={{ fontSize: "12px", color: "var(--classic-muted)", margin: "5px 0 15px" }}>
                  Sign out of the minimalrouter dashboard on this device.
                </p>
                <button 
                  type="button" 
                  onClick={() => void logout()} 
                  className="button" 
                  style={{ background: "#d9382e", color: "white", border: "none" }}
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
