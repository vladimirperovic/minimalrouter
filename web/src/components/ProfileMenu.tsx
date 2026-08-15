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

type FirmwareStatus = {
  enabled?: boolean;
  current_version?: string;
  previous_version?: string;
  pending_version?: string;
  latest_version?: string;
  update_available?: boolean;
  prerelease?: boolean;
  published_at?: string;
  check_error?: string;
};

type FirmwareUpdateResult = {
  error?: string;
  message?: string;
  state?: string;
  success?: boolean;
  version?: string;
};

type Props = {
  changePassword: (event: FormEvent<HTMLFormElement>) => Promise<void>;
  logout: () => Promise<void>;
  error: string;
  setError: (message: string) => void;
};

function sleep(milliseconds: number) {
  return new Promise<void>((resolve) => window.setTimeout(resolve, milliseconds));
}

export default function ProfileMenu({ changePassword, logout, error, setError }: Props) {
  const [open, setOpen] = useState(false);
  const [panel, setPanel] = useState<"account" | "password" | "update">("account");
  const [lastLogin, setLastLogin] = useState<AuditEvent | null>(null);
  const [firmware, setFirmware] = useState<FirmwareStatus | null>(null);
  const [checkingUpdate, setCheckingUpdate] = useState(false);
  const [updating, setUpdating] = useState(false);
  const [updateMessage, setUpdateMessage] = useState("");
  const rootRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open || updating) return;
    const onPointerDown = (event: PointerEvent) => {
      if (rootRef.current && !rootRef.current.contains(event.target as Node)) setOpen(false);
    };
    document.addEventListener("pointerdown", onPointerDown);
    return () => document.removeEventListener("pointerdown", onPointerDown);
  }, [open, updating]);

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

  const checkFirmware = async () => {
    if (isDemoMode) return;
    setCheckingUpdate(true);
    try {
      const response = await apiFetch("/api/v1/firmware/status");
      const body = (await response.json().catch(() => ({}))) as FirmwareStatus;
      if (!response.ok) throw new Error(body.check_error || `Update status unavailable (${response.status})`);
      setFirmware(body);
    } catch (statusError) {
      setFirmware({ check_error: statusError instanceof Error ? statusError.message : "Update status unavailable" });
    } finally {
      setCheckingUpdate(false);
    }
  };

  useEffect(() => {
    if (open && panel === "update" && !isDemoMode) void checkFirmware();
  }, [open, panel]);

  const submitPassword = async (event: FormEvent<HTMLFormElement>) => {
    setError("");
    await changePassword(event);
    if (!error) setOpen(false);
  };

  const waitForActivatedVersion = async (version: string) => {
    for (let attempt = 0; attempt < 90; attempt += 1) {
      await sleep(3000);
      try {
        const response = await apiFetch("/api/v1/system");
        if (!response.ok) continue;
        const body = (await response.json()) as { version?: string };
        if (body.version === version) {
          setUpdateMessage(`${version} is active. Reloading dashboard…`);
          await sleep(700);
          window.location.reload();
          return true;
        }
      } catch {
        // routerd is expected to be temporarily unreachable while both runtime
        // daemons restart from the new A/B slot.
      }
    }
    return false;
  };

  const finishUpdateRequest = async (response: Response) => {
    const body = (await response.json().catch(() => ({}))) as FirmwareUpdateResult;
    if (!response.ok || !body.success || !body.version) {
      throw new Error(body.error || `Update failed (${response.status})`);
    }
    setUpdateMessage(`${body.version} verified. Restarting into the new slot…`);
    const activated = await waitForActivatedVersion(body.version);
    if (!activated) {
      setUpdateMessage("The new version did not become active within the recovery window. The previous slot may have been retained or restored; check the local update log before trying again.");
      await checkFirmware();
    }
  };

  const installUpdate = async () => {
    const version = firmware?.latest_version;
    if (!version || !firmware?.update_available || updating) return;
    if (!window.confirm(`Install ${version} now? Minimal Router will verify the signed release, switch A/B slots, restart its services, and automatically roll back if the new slot does not start cleanly.`)) return;

    setError("");
    setUpdateMessage(`Preparing and verifying ${version}…`);
    setUpdating(true);
    try {
      const response = await apiFetch("/api/v1/firmware/update", { method: "POST" });
      await finishUpdateRequest(response);
    } catch (updateError) {
      const message = updateError instanceof Error ? updateError.message : "Update failed";
      setUpdateMessage(message);
      setError(message);
    } finally {
      setUpdating(false);
    }
  };

  const uploadSignedBuild = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (updating) return;
    const form = event.currentTarget;
    const data = new FormData(form);
    if (!window.confirm("Install this signed build now? The manifest and archive will be verified by the pinned firmware key before the A/B slot can activate.")) return;

    setError("");
    setUpdateMessage("Uploading and verifying signed build…");
    setUpdating(true);
    try {
      const response = await apiFetch("/api/v1/firmware/upload", { method: "POST", body: data });
      await finishUpdateRequest(response);
    } catch (uploadError) {
      const message = uploadError instanceof Error ? uploadError.message : "Build upload failed";
      setUpdateMessage(message);
      setError(message);
    } finally {
      setUpdating(false);
    }
  };

  const openUpdatePanel = () => {
    setUpdateMessage("");
    setPanel("update");
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
        <div className={`profile-menu-pop${panel === "update" ? " profile-menu-pop-update" : ""}`} role="menu">
          {panel === "account" ? (
            <>
              <div className="profile-menu-header">
                <strong>Administrator</strong>
                <small>{lastLogin ? `Last login ${new Date(lastLogin.timestamp).toLocaleString()} from ${lastLogin.actor}` : "First login on this browser"}</small>
              </div>
              {!isDemoMode && <button type="button" className="profile-menu-item" onClick={openUpdatePanel}>Software update</button>}
              <button type="button" className="profile-menu-item" onClick={() => setPanel("password")}>Change password</button>
              <button type="button" className="profile-menu-item is-danger" onClick={() => void logout()}>Sign out</button>
            </>
          ) : panel === "password" ? (
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
          ) : (
            <>
              <div className="profile-menu-header profile-update-header">
                <strong>Software update</strong>
                <small>Signed Minimal Router releases only. A/B rollback remains available automatically.</small>
              </div>
              <div className="profile-update-body">
                <dl className="profile-update-versions">
                  <div><dt>Installed</dt><dd>{firmware?.current_version || (checkingUpdate ? "Checking…" : "Unknown")}</dd></div>
                  <div><dt>Latest</dt><dd>{firmware?.latest_version || (checkingUpdate ? "Checking…" : "Unknown")}</dd></div>
                </dl>
                {firmware?.pending_version && <p className="profile-update-note is-warning">Verified release {firmware.pending_version} is already pending activation.</p>}
                {firmware?.check_error && <p className="profile-update-note is-warning">{firmware.check_error}</p>}
                {!checkingUpdate && firmware?.enabled && firmware.latest_version && !firmware.update_available && !firmware.pending_version && !firmware.check_error && <p className="profile-update-note is-current">You are on the newest published release.</p>}
                {firmware?.update_available && <p className="profile-update-note">{firmware.latest_version} is available{firmware.prerelease ? " as a Beta release" : ""}. It will be downloaded, signature-verified, staged, and activated.</p>}
                {updateMessage && <p className={`profile-update-note${updating ? " is-running" : ""}`} role="status">{updateMessage}</p>}
                <div className="profile-menu-actions profile-update-actions">
                  <button type="button" className="button secondary small" disabled={updating} onClick={() => setPanel("account")}>Back</button>
                  <button type="button" className="button secondary small" disabled={updating || checkingUpdate} onClick={() => void checkFirmware()}>{checkingUpdate ? "Checking…" : "Check again"}</button>
                  <button type="button" className="button primary small" disabled={updating || checkingUpdate || !firmware?.enabled || !firmware?.update_available || Boolean(firmware?.pending_version)} onClick={() => void installUpdate()}>{updating ? "Updating…" : "Update now"}</button>
                </div>

                <div className="profile-update-divider"><span>or install a signed build</span></div>
                <form className="profile-update-upload" onSubmit={uploadSignedBuild}>
                  <label><span>Signed manifest</span><input accept=".json,application/json" disabled={updating || !firmware?.enabled || Boolean(firmware?.pending_version)} name="manifest" required type="file" /></label>
                  <label><span>Release archive</span><input accept=".tar.gz,.tgz,application/gzip" disabled={updating || !firmware?.enabled || Boolean(firmware?.pending_version)} name="archive" required type="file" /></label>
                  <p>Use the matching architecture archive and its signed <code>.manifest.json</code>. Unsigned, altered, wrong-architecture, same-version, or older builds are rejected by the updater.</p>
                  <button className="button secondary small" disabled={updating || !firmware?.enabled || Boolean(firmware?.pending_version)} type="submit">{updating ? "Updating…" : "Upload signed build"}</button>
                </form>
              </div>
            </>
          )}
        </div>
      )}
    </div>
  );
}
