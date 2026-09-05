import { useEffect, useRef, useState } from "react";
import type { FormEvent } from "react";
import { apiFetch } from "../lib/api";
import { isDemoMode } from "../lib/demoApi";
import "./ProfileMenu.css";

type AuditEvent = {
  event_type: string;
  timestamp: string;
  actor: string;
};

type Props = {
  changePassword: (event: FormEvent<HTMLFormElement>) => Promise<void>;
  logout: () => Promise<void>;
  error: string;
  setError: (message: string) => void;
  /** Opens the one shared update dialog; this menu no longer owns its own. */
  openUpdates: () => void;
  updateAvailable: boolean;
};

export default function ProfileMenu({ changePassword, logout, error, setError, openUpdates, updateAvailable }: Props) {
  const [open, setOpen] = useState(false);
  const [panel, setPanel] = useState<"account" | "password">("account");
  const [lastLogin, setLastLogin] = useState<AuditEvent | null>(null);
  const rootRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const onPointerDown = (event: PointerEvent) => {
      if (rootRef.current && !rootRef.current.contains(event.target as Node)) setOpen(false);
    };
    document.addEventListener("pointerdown", onPointerDown);
    return () => document.removeEventListener("pointerdown", onPointerDown);
  }, [open]);

  useEffect(() => {
    if (!open) return;
    void apiFetch("/api/v1/audit/events")
      .then(res => res.ok ? res.json() : Promise.reject())
      .then((body: { events?: AuditEvent[] }) => {
        const data = Array.isArray(body.events) ? body.events : [];
        const logins = data.filter((e) => e.event_type === "auth.login_succeeded");
        const prior = logins.length > 1 ? logins[1] : logins.length === 1 ? logins[0] : null;
        setLastLogin(prior);
      })
      .catch(() => undefined);
  }, [open]);

  const submitPassword = async (event: FormEvent<HTMLFormElement>) => {
    setError("");
    await changePassword(event);
    if (!error) setOpen(false);
  };

  return (
    <div className="profile-menu" ref={rootRef}>
      <button
        className={`classic-account-button${open ? " is-open" : ""}`}
        onClick={() => setOpen((value) => !value)}
        type="button"
        aria-haspopup="true"
        aria-expanded={open}
        title="Account"
      >
        <span className="classic-account-monogram" aria-hidden="true">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.65" strokeLinecap="round" strokeLinejoin="round">
            <circle cx="12" cy="8" r="3.4" />
            <path d="M5.5 20v-1.7c0-3.1 2.9-5.6 6.5-5.6s6.5 2.5 6.5 5.6V20" />
          </svg>
        </span>
        <strong>Admin</strong>
        <svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="m8 10 4 4 4-4" /></svg>
      </button>
      {open && (
        <div className="profile-menu-pop" role="menu">
          {panel === "account" ? (
            <>
              <div className="profile-menu-header">
                <strong>Administrator</strong>
                <small>{lastLogin ? `Last login ${new Date(lastLogin.timestamp).toLocaleString()} from ${lastLogin.actor}` : "First login on this browser"}</small>
              </div>
              {!isDemoMode && (
                <button type="button" className="profile-menu-item" onClick={() => { setOpen(false); openUpdates(); }}>
                  Software update{updateAvailable ? " — new version available" : ""}
                </button>
              )}
              <button type="button" className="profile-menu-item" onClick={() => setPanel("password")}>Change password</button>
              <button type="button" className="profile-menu-item is-danger" onClick={() => void logout()}>Sign out</button>
            </>
          ) : (
            <>
              <div className="profile-menu-header">
                <strong>Change password</strong>
                <small>Minimum 12 characters. Signs you out of every session.</small>
              </div>
              <form className="profile-menu-form" onSubmit={submitPassword}>
                <input autoComplete="current-password" name="old_password" placeholder="Current password" required type="password" />
                <input autoComplete="new-password" minLength={12} name="new_password" placeholder="New password" required type="password" />
                <input autoComplete="new-password" minLength={12} name="confirm_password" placeholder="Confirm new password" required type="password" />
                <div className="profile-menu-actions">
                  <button type="button" className="button secondary small" onClick={() => setPanel("account")}>Back</button>
                  <button type="submit" className="button primary small">Update password</button>
                </div>
              </form>
            </>
          )}
        </div>
      )}
    </div>
  );
}
