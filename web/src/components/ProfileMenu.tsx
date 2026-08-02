import { useEffect, useRef, useState } from "react";
import type { FormEvent } from "react";
import { apiFetch } from "../lib/api";

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
};

export default function ProfileMenu({ changePassword, logout, error, setError }: Props) {
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

  const initials = "Admin";

  return (
    <div className="profile-menu" ref={rootRef}>
      <button
        className="classic-avatar"
        onClick={() => setOpen((value) => !value)}
        type="button"
        aria-haspopup="true"
        aria-expanded={open}
        title="Account"
      >
        {initials}
      </button>
      {open && (
        <div className="profile-menu-pop" role="menu">
          {panel === "account" ? (
            <>
              <div className="profile-menu-header">
                <strong>Administrator</strong>
                <small>{lastLogin ? `Last login ${new Date(lastLogin.timestamp).toLocaleString()} from ${lastLogin.actor}` : "First login on this browser"}</small>
              </div>
              <button type="button" className="profile-menu-item" onClick={() => setPanel("password")}>Change password</button>
              <button type="button" className="profile-menu-item is-danger" onClick={() => void logout()}>Sign out</button>
            </>
          ) : (
            <>
              <div className="profile-menu-header">
                <strong>Change password</strong>
                <small>Minimum 15 characters. Signs you out of every session.</small>
              </div>
              <form className="profile-menu-form" onSubmit={submitPassword}>
                <input autoComplete="current-password" name="old_password" placeholder="Current password" required type="password" />
                <input autoComplete="new-password" minLength={15} name="new_password" placeholder="New password" required type="password" />
                <input autoComplete="new-password" minLength={15} name="confirm_password" placeholder="Confirm new password" required type="password" />
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