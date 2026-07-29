import { FormEvent, ReactNode, useEffect, useState } from "react";
import SetupWizard from "./SetupWizard";
import { refreshSession, setCSRFToken } from "../lib/api";

type AuthState = "loading" | "setup" | "login" | "offline" | "authenticated";

export default function AuthGate({ children }: { children: ReactNode }) {
  const [state, setState] = useState<AuthState>("loading");
  const [password, setPassword] = useState("");
  const [totpCode, setTotpCode] = useState("");
  const [error, setError] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [requiresTOTP, setRequiresTOTP] = useState(false);
  const [previewMode, setPreviewMode] = useState(false);

  useEffect(() => {
    let active = true;
    const initialize = async () => {
      const localPreviewHosts = new Set(["localhost", "127.0.0.1", "::1"]);
      try {
        if (await refreshSession()) {
          if (active) setState("authenticated");
          return;
        }
        const status = await fetch("/api/v1/setup/status", {
          credentials: "same-origin",
          cache: "no-store",
        });
        const contentType = status.headers.get("content-type") ?? "";
        if (!status.ok || !contentType.includes("application/json")) {
          throw new Error("Router API unavailable");
        }
        const result = (await status.json()) as { is_configured?: boolean };
        if (active) setState(result.is_configured ? "login" : "setup");
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

  const login = async (event: FormEvent) => {
    event.preventDefault();
    setSubmitting(true);
    setError("");
    try {
      if (previewMode) {
        if (password.trim() !== "minimalrouter-preview") {
          throw new Error("Incorrect preview password");
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
      const result = (await response.json()) as {
        csrf_token?: string;
        error?: string;
        totp_required?: string;
      };
      if (!response.ok || !result.csrf_token) {
        if (result.totp_required === "true") {
          setRequiresTOTP(true);
        }
        throw new Error(result.error ?? "Sign-in failed");
      }
      setCSRFToken(result.csrf_token);
      setPassword("");
      setTotpCode("");
      setState("authenticated");
    } catch (loginError) {
      const message = loginError instanceof Error ? loginError.message : "";
      setError(
        message === "Incorrect preview password"
          ? message
          : "The router sign-in service is unavailable.",
      );
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
          <h1 id="offline-title">Router unavailable</h1>
          <p>Connect to the router LAN and try again.</p>
          <button className="button primary" type="button" onClick={() => window.location.reload()}>Try again</button>
        </section>
      </main>
    );
  }

  return (
    <main className="auth-shell">
      <section className="auth-panel" aria-labelledby="login-title">
        <div className="auth-brand"><span aria-hidden="true">M</span><strong>Minimal Router OS</strong></div>
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
          <span>Six-digit TOTP code</span>
          <input
            autoComplete="one-time-code"
            inputMode="numeric"
            maxLength={6}
            onChange={(event) => setTotpCode(event.target.value.replace(/\D/g, ""))}
            value={totpCode}
          />
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
