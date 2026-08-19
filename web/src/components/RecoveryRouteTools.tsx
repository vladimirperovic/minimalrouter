import { useEffect, useState } from "react";
import { createPortal } from "react-dom";
import type { RouterConfig } from "../api-types";
import { apiFetch } from "../lib/api";
import { isDemoMode } from "../lib/demoApi";
import RecoveryToolsPanel from "./RecoveryToolsPanel";

// The v0.1.5 CSS pass hid the old Security placement before the production
// component move existed. This bridge mounts the real RecoveryToolsPanel into
// the real Recovery section, and can disappear once DashboardSections owns the
// panel directly in a later structural cleanup.
//
// It keys off the presence of the #recovery section rather than the URL hash.
// Two things made the hash unusable: DashboardApp navigates with
// history.pushState, which does not emit `hashchange`, so an in-app click never
// woke this component; and on a direct load of /#recovery the hash is already
// correct while #recovery is still unmounted behind the initial
// "Loading secure router state…" state, so a one-shot lookup found nothing and
// never retried. Observing the DOM covers both, and any future navigation
// mechanism, without patching history.
export default function RecoveryRouteTools() {
  const [host, setHost] = useState<HTMLElement | null>(null);
  const [config, setConfig] = useState<RouterConfig | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    // GitHub Pages keeps the legacy demo portal for its mocked recovery
    // actions; mounting here as well would render the panel twice.
    if (isDemoMode) return;

    const root = document.getElementById("root") ?? document.body;
    let container: HTMLElement | null = null;

    const reconcile = () => {
      const section = document.querySelector<HTMLElement>("#recovery");
      if (!section) {
        if (container) {
          container.remove();
          container = null;
          setHost(null);
        }
        return;
      }
      if (container && container.isConnected && container.parentElement === section) return;
      container?.remove();
      container = document.createElement("div");
      container.className = "production-recovery-tools-host demo-015-recovery-slot demo-015-recovery-moved";
      section.appendChild(container);
      setHost(container);
    };

    const observer = new MutationObserver(reconcile);
    observer.observe(root, { childList: true, subtree: true });
    reconcile();

    return () => {
      observer.disconnect();
      container?.remove();
      container = null;
    };
  }, []);

  useEffect(() => {
    if (!host) return;
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
  }, [host]);

  if (!host) return null;
  return createPortal(
    <>
      {error && <div className="dashboard-alert is-error" role="alert">{error}</div>}
      {config && <RecoveryToolsPanel config={config} onError={setError} />}
    </>,
    host,
  );
}
