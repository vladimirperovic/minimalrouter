import { useEffect, useLayoutEffect, useState } from "react";
import { createPortal } from "react-dom";
import type { RouterConfig } from "../api-types";
import { apiFetch } from "../lib/api";
import { isDemoMode } from "../lib/demoApi";
import RecoveryToolsPanel from "./RecoveryToolsPanel";

function recoveryRouteActive() {
  // GitHub Pages keeps the legacy demo portal for its mocked recovery actions.
  // Production mounts this bridge instead. Using the same wrapper classes below
  // keeps both render paths visually identical without mounting the panel twice
  // when VITE_DEMO_MODE is enabled.
  return !isDemoMode && window.location.hash === "#recovery";
}

// The v0.1.5 CSS pass hid the old Security placement before the production
// component move existed. This bridge mounts the real RecoveryToolsPanel into
// the real Recovery section. It is event-driven React (no MutationObserver and
// no DOM rewriting) and can disappear once DashboardSections owns the panel
// directly in a later structural cleanup.
export default function RecoveryRouteTools() {
  const [active, setActive] = useState(recoveryRouteActive);
  const [host, setHost] = useState<HTMLElement | null>(null);
  const [config, setConfig] = useState<RouterConfig | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    const sync = () => setActive(recoveryRouteActive());
    window.addEventListener("hashchange", sync);
    return () => window.removeEventListener("hashchange", sync);
  }, []);

  useLayoutEffect(() => {
    if (!active) {
      setHost(null);
      return;
    }
    const section = document.querySelector<HTMLElement>("#recovery");
    if (!section) return;
    const container = document.createElement("div");
    container.className = "production-recovery-tools-host demo-015-recovery-slot demo-015-recovery-moved";
    section.appendChild(container);
    setHost(container);
    return () => container.remove();
  }, [active]);

  useEffect(() => {
    if (!active) {
      setConfig(null);
      setError("");
      return;
    }
    const controller = new AbortController();
    void (async () => {
      try {
        const response = await apiFetch("/api/v1/config", { signal: controller.signal });
        if (!response.ok) throw new Error(`Configuration unavailable (${response.status})`);
        setConfig((await response.json()) as RouterConfig);
      } catch (loadError) {
        if ((loadError as Error).name !== "AbortError") {
          setError(loadError instanceof Error ? loadError.message : "Recovery tools unavailable");
        }
      }
    })();
    return () => controller.abort();
  }, [active]);

  if (!active || !host) return null;
  return createPortal(
    <>
      {error && <div className="dashboard-alert is-error" role="alert">{error}</div>}
      {config && <RecoveryToolsPanel config={config} onError={setError} />}
    </>,
    host,
  );
}
