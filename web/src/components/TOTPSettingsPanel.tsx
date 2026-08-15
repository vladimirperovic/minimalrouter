import { FormEvent, useState } from "react";
import { apiFetch } from "../lib/api";

type Props = {
  onError: (message: string) => void;
};

type Enrollment = {
  secret: string;
  qr_uri: string;
};

async function responseError(response: Response, fallback: string): Promise<string> {
  const clone = response.clone();
  try {
    const body = await response.json() as { error?: string };
    if (body?.error) return body.error;
  } catch {
    // Some hardened API errors intentionally use plain text.
  }
  try {
    const text = (await clone.text()).trim();
    if (text) return text;
  } catch {
    // Fall through to the stable local message.
  }
  return fallback;
}

export default function TOTPSettingsPanel({ onError }: Props) {
  const [enrollment, setEnrollment] = useState<Enrollment | null>(null);
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState("");
  const [copied, setCopied] = useState(false);

  const startEnrollment = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const formElement = event.currentTarget;
    const form = new FormData(formElement);
    const currentPassword = String(form.get("current_password") ?? "");
    setBusy(true);
    setNotice("");
    onError("");
    try {
      const response = await apiFetch("/api/v1/auth/totp/enroll", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ current_password: currentPassword }),
      });
      if (!response.ok) throw new Error(await responseError(response, `TOTP enrollment failed (${response.status})`));
      const body = await response.json() as Enrollment;
      if (!body.secret || !body.qr_uri) throw new Error("TOTP enrollment response was incomplete");
      setEnrollment(body);
      setNotice("Enrollment secret created. Add it to your authenticator, then verify a code below within 10 minutes.");
      formElement.reset();
    } catch (error) {
      onError(error instanceof Error ? error.message : "TOTP enrollment failed");
    } finally {
      setBusy(false);
    }
  };

  const enableTOTP = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const code = String(new FormData(event.currentTarget).get("code") ?? "").trim();
    setBusy(true);
    onError("");
    try {
      const response = await apiFetch("/api/v1/auth/totp/enable", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ code }),
      });
      if (!response.ok) throw new Error(await responseError(response, `TOTP enable failed (${response.status})`));
      setNotice("Two-factor authentication enabled. All existing sessions were revoked; sign in again with your authenticator code.");
      setEnrollment(null);
      window.dispatchEvent(new Event("minimalrouter:unauthorized"));
    } catch (error) {
      onError(error instanceof Error ? error.message : "TOTP enable failed");
    } finally {
      setBusy(false);
    }
  };

  const disableTOTP = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    const currentPassword = String(form.get("disable_current_password") ?? "");
    const code = String(form.get("disable_code") ?? "").trim();
    if (!window.confirm("Disable two-factor authentication for the administrator account?")) return;
    setBusy(true);
    setNotice("");
    onError("");
    try {
      const response = await apiFetch("/api/v1/auth/totp/disable", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ current_password: currentPassword, code }),
      });
      if (!response.ok) throw new Error(await responseError(response, `TOTP disable failed (${response.status})`));
      setNotice("Two-factor authentication disabled. Existing sessions were revoked; sign in again.");
      setEnrollment(null);
      window.dispatchEvent(new Event("minimalrouter:unauthorized"));
    } catch (error) {
      onError(error instanceof Error ? error.message : "TOTP disable failed");
    } finally {
      setBusy(false);
    }
  };

  const copySecret = async () => {
    if (!enrollment?.secret || !navigator.clipboard?.writeText) return;
    try {
      await navigator.clipboard.writeText(enrollment.secret);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1800);
    } catch {
      onError("Could not copy the authenticator secret. Select it manually instead.");
    }
  };

  return (
    <article className="card security-control-card security-totp-card" aria-labelledby="totp-settings-title">
      <div className="card-title-row">
        <div>
          <h3 id="totp-settings-title">Two-factor authentication</h3>
          <p>Enroll or disable TOTP using the administrator password. The server verifies the current state and refuses conflicting actions.</p>
        </div>
        <span className="quiet-meta">TOTP</span>
      </div>

      {notice && <div className="dashboard-callout" role="status"><strong>Authentication update</strong><p>{notice}</p></div>}

      {!enrollment ? (
        <form className="settings-form" onSubmit={startEnrollment}>
          <div className="form-grid two">
            <label className="field form-span">
              <span>Current administrator password</span>
              <input autoComplete="current-password" name="current_password" required type="password" />
            </label>
          </div>
          <p className="form-note">Starting enrollment does not enable 2FA. The generated secret stays pending for 10 minutes and is committed only after a valid authenticator code is verified.</p>
          <div className="form-actions"><button className="button primary" disabled={busy} type="submit">{busy ? "Preparing…" : "Start 2FA enrollment"}</button></div>
        </form>
      ) : (
        <>
          <div className="dashboard-callout">
            <strong>Add this account to your authenticator</strong>
            <p>Use the secret below or import the <code>otpauth://</code> URI. Minimal Router never stores the pending secret until you verify a code.</p>
            <div className="form-grid two">
              <label className="field"><span>Secret</span><input readOnly value={enrollment.secret} /></label>
              <label className="field"><span>Authenticator URI</span><input readOnly value={enrollment.qr_uri} /></label>
            </div>
            <div className="form-actions"><button className="button secondary" onClick={() => void copySecret()} type="button">{copied ? "Copied" : "Copy secret"}</button></div>
          </div>
          <form className="settings-form" onSubmit={enableTOTP}>
            <div className="form-grid two">
              <label className="field"><span>6-digit authenticator code</span><input autoComplete="one-time-code" inputMode="numeric" maxLength={8} name="code" pattern="[0-9]{6,8}" required /></label>
            </div>
            <div className="form-actions">
              <button className="button secondary" disabled={busy} onClick={() => setEnrollment(null)} type="button">Cancel enrollment</button>
              <button className="button primary" disabled={busy} type="submit">{busy ? "Verifying…" : "Verify and enable 2FA"}</button>
            </div>
          </form>
        </>
      )}

      <details>
        <summary>Disable two-factor authentication</summary>
        <form className="settings-form" onSubmit={disableTOTP}>
          <div className="form-grid two">
            <label className="field"><span>Current administrator password</span><input autoComplete="current-password" name="disable_current_password" required type="password" /></label>
            <label className="field"><span>Current authenticator code</span><input autoComplete="one-time-code" inputMode="numeric" maxLength={8} name="disable_code" pattern="[0-9]{6,8}" required /></label>
          </div>
          <p className="form-note">Disabling TOTP revokes all sessions. If TOTP is not enabled, the server rejects this request rather than silently changing authentication state.</p>
          <div className="form-actions"><button className="button danger" disabled={busy} type="submit">Disable 2FA</button></div>
        </form>
      </details>
    </article>
  );
}
