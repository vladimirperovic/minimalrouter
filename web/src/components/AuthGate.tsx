import { FormEvent, ReactNode, useEffect, useState } from "react";
import SetupWizard from "./SetupWizard";
import { refreshSession, setCSRFToken } from "../lib/api";
import { isDemoMode } from "../lib/demoApi";

type AuthState = "loading" | "setup" | "login" | "offline" | "authenticated";

type LoginResult = {
  csrf_token?: string;
  error?: string;
  totp_required?: string;
};

function loginFailureMessage(status: number, result: LoginResult) {
  if (result.totp_required === "true") {
    return "Sign-in failed. Re-enter your password and, if two-factor authentication is enabled, the current six-digit code.";
  }
  if (status === 429) return "Too many sign-in attempts. Try again shortly.";
  if (result.error) return result.error;
  if (status === 401) return "Incorrect password or TOTP code.";
  return `Sign-in failed (${status}).`;
}

async function probeRouterState(): Promise<"setup" | "login" | "authenticated"> {
  if (await refreshSession()) return "authenticated";
  const status = await fetch("/api/v1/setup/status", {
    credentials: "same-origin",
    cache: "no-store",
  });
  const contentType = status.headers.get("content-type") ?? "";
  if (!status.ok || !contentType.includes("application/json")) {
    throw new Error("Router API unavailable");
  }
  const result = (await status.json()) as { is_configured?: boolean };
  return result.is_configured ? "login" : "setup";
}

export default function AuthGate({ children }: { children: ReactNode }) {
  const [state, setState] = useState<AuthState>(isDemoMode ? "authenticated" : "loading");
  const [password, setPassword] = useState("");
  const [totpCode, setTotpCode] = useState("");
  const [error, setError] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [requiresTOTP, setRequiresTOTP] = useState(false);
  const [previewMode, setPreviewMode] = useState(false);

  useEffect(() => {
    if (isDemoMode) return;

    let active = true;
    const initialize = async () => {
      const localPreviewHosts = new Set(["localhost", "127.0.0.1", "::1"]);
      try {
        const nextState = await probeRouterState();
        if (active) setState(nextState);
      } catch {
        if (active) {
          if (localPreviewHosts.has(window.location.hostname)) {
            setPreviewMode(true);
            setState("login");
          } else {
            setState("offline");
          }
        }
      }
    };
    void initialize();
    const unauthorized = () => setState("login");
    window.addEventListener("minimalrouter:unauthorized", unauthorized);
    return () => {
      active = false;
      window.removeEventListener("minimalrouter:unauthorized", unauthorized);
    };
  }, []);

  useEffect(() => {
    if (state !== "offline" || previewMode) return;

    let active = true;
    let timer = 0;
    const retry = async () => {
      try {
        const nextState = await probeRouterState();
        if (active) {
          setError("");
          setState(nextState);
        }
      } catch {
        if (active) timer = window.setTimeout(retry, 5000);
      }
    };
    timer = window.setTimeout(retry, 5000);
    return () => {
      active = false;
      window.clearTimeout(timer);
    };
  }, [previewMode, state]);

  const login = async (event: FormEvent) => {
    event.preventDefault();
    setSubmitting(true);
    setError("");
    try {
      if (previewMode) {
        if (password.trim() !== "password") {
          setError("Incorrect preview password");
          return;
        }
        setPassword("");
        setState("authenticated");
        return;
      }

      const response = await fetch("/api/v1/auth/login", {
        method: "POST",
        credentials: "same-origin",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ password, totp_code: totpCode }),
      });
      const contentType = response.headers.get("content-type") ?? "";
      if (!contentType.includes("application/json")) {
        throw new Error("The router sign-in service returned an invalid response.");
      }
      const result = (await response.json()) as LoginResult;
      if (!response.ok || !result.csrf_token) {
        const needsTOTP = result.totp_required === "true";
        setRequiresTOTP(needsTOTP);
        setError(loginFailureMessage(response.status, result));
        return;
      }

      setCSRFToken(result.csrf_token);
      setPassword("");
      setTotpCode("");
      setRequiresTOTP(false);
      setState("authenticated");
    } catch (loginError) {
      setError(loginError instanceof Error && loginError.message
        ? loginError.message
        : "The router sign-in service is unavailable.");
    } finally {
      setSubmitting(false);
    }
  };

  if (state === "authenticated") return <>{children}</>;
  if (state === "setup") {
    return (
      <SetupWizard
        onComplete={() => {
          void refreshSession().then((ok) => setState(ok ? "authenticated" : "login"));
        }}
      />
    );
  }
  if (state === "loading") {
    return <main className="auth-shell"><p className="auth-loading">Checking secure session…</p></main>;
  }
  if (state === "offline") {
    return (
      <main className="auth-shell">
        <section className="auth-panel auth-offline" aria-labelledby="offline-title">
          <div className="auth-brand"><span aria-hidden="true">M</span><strong>Minimal Router OS</strong></div>
          <p className="auth-kicker">Secure access</p>
          <h1 id="offline-title">Router unavailable</h1>
          <p>Connect to the router LAN and try again. This page will reconnect automatically.</p>
          <button className="button primary" type="button" onClick={() => window.location.reload()}>Try again now</button>
        </section>
      </main>
    );
  }

  return (
    <main className="auth-shell">
      <section className="auth-panel" aria-labelledby="login-title">
        <div className="auth-brand"><span aria-hidden="true">M</span><strong>Minimal Router OS</strong></div>
        <p className="auth-kicker">Secure access</p>
        <div className="auth-heading">
          <h1 id="login-title">Sign in</h1>
          {previewMode && <span className="auth-preview-badge">UI preview</span>}
        </div>
        <form className="auth-form" onSubmit={login}>
        <label className="field">
          <span>Password</span>
          <input
            autoComplete="current-password"
            autoFocus
            onChange={(event) => setPassword(event.target.value)}
            required
            type="password"
            value={password}
          />
        </label>
        {requiresTOTP && <label className="field">
          <span>Two-factor code <small>(only if enabled)</small></span>
          <input
            aria-describedby="totp-help"
            autoComplete="one-time-code"
            inputMode="numeric"
            maxLength={6}
            onChange={(event) => setTotpCode(event.target.value.replace(/\D/g, ""))}
            value={totpCode}
          />
          <small id="totp-help">Leave this blank if two-factor authentication is not configured.</small>
        </label>}
        {error && <p className="auth-error" role="alert">{error}</p>}
        <button className="button primary auth-submit" disabled={submitting} type="submit">
          {submitting ? "Signing in…" : "Sign in"}
        </button>
        </form>
      </section>
    </main>
  );
}
