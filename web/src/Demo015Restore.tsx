import { useEffect, useState } from "react";
import { createPortal } from "react-dom";
import type { RouterConfig } from "./api-types";
import RecoveryToolsPanel from "./components/RecoveryToolsPanel";
import { apiFetch } from "./lib/api";
import { isDemoMode } from "./lib/demoApi";

const DEMO_WAN_ESTIMATE = { download_mbps: 600, upload_mbps: 400, measured_at: Date.now() };

if (isDemoMode && typeof window !== "undefined") {
  try {
    window.localStorage.setItem("minimalrouter:wan-speed-estimate", JSON.stringify(DEMO_WAN_ESTIMATE));
  } catch {
    // Preview still works when localStorage is disabled.
  }
}

function addSaveButton(fieldset: HTMLFieldSetElement, label: string, marker: string) {
  if (fieldset.querySelector(`.${marker}`)) return;
  const actions = document.createElement("div");
  actions.className = `form-actions demo-015-fieldset-actions ${marker}`;
  const button = document.createElement("button");
  button.className = "button primary";
  button.type = "submit";
  button.textContent = label;
  actions.appendChild(button);
  fieldset.appendChild(actions);
}

function patchNetworkPage() {
  const network = document.getElementById("network");
  const form = network?.querySelector<HTMLFormElement>("form.settings-form");
  if (!network || !form) return;

  const fieldsets = Array.from(form.children).filter((child): child is HTMLFieldSetElement => child instanceof HTMLFieldSetElement);
  const wan = fieldsets.find((fieldset) => fieldset.querySelector("legend")?.textContent?.includes("WAN / PPPoE"));
  const lan = fieldsets.find((fieldset) => fieldset.querySelector("legend")?.textContent?.includes("LAN and DHCP"));

  if (wan) {
    const wanToggle = wan.querySelector<HTMLInputElement>('input[type="checkbox"]')?.closest("label");
    wanToggle?.classList.add("demo-015-hidden-wan-toggle");
    const password = wan.querySelector<HTMLInputElement>('input[name="pppoe_password"]');
    if (password) password.placeholder = "Enter a new PPPoE password (optional)";
    addSaveButton(wan, "Save WAN", "demo-015-save-wan");
  }

  if (lan) addSaveButton(lan, "Save LAN & DHCP", "demo-015-save-lan");
  const directActions = Array.from(form.children).filter((child) => child.classList.contains("form-actions"));
  directActions.at(-1)?.classList.add("demo-015-original-network-save");
}

function patchOverview() {
  const session = Array.from(document.querySelectorAll<HTMLElement>(".overview-wan-main > div")).find((element) =>
    element.querySelector("strong")?.textContent?.trim() === "PPPoE",
  );
  if (!session) return;
  let estimate = session.querySelector<HTMLElement>(".demo-015-session-estimate");
  if (!estimate) {
    estimate = document.createElement("small");
    estimate.className = "demo-015-session-estimate";
    session.appendChild(estimate);
  }
  estimate.textContent = "~600 ↓ / 400 ↑ Mbps";
}

function patchRecoveryCopy() {
  const recovery = document.getElementById("recovery");
  if (recovery) {
    const title = recovery.querySelector<HTMLElement>(".dashboard-section-heading h2");
    if (title) title.textContent = "Recovery, backup and migration";
    const copy = recovery.querySelector<HTMLElement>(".dashboard-section-heading .section-copy");
    if (copy) copy.textContent = "Snapshots, encrypted backups, diagnostics and pfSense migration in one recovery workspace.";
  }

  document.querySelectorAll<HTMLElement>(".security-recovery-card").forEach((card) => {
    const paragraph = card.querySelector<HTMLElement>(".card-title-row p");
    if (paragraph) paragraph.textContent = "Export/import encrypted .mrbak backups, migrate a pfSense config.xml, and download redacted diagnostics.";
    const summary = card.querySelector<HTMLElement>("details summary");
    if (summary && summary.textContent === "Encrypted backup export") summary.textContent = "Encrypted Minimal Router backup (.mrbak)";
  });
}

function patchPreviewBadge() {
  const brand = document.querySelector<HTMLElement>(".dashboard-brand");
  if (!brand || brand.querySelector(".demo-015-beta-badge")) return;
  const badge = document.createElement("span");
  badge.className = "demo-015-beta-badge";
  badge.textContent = "v0.1.5 beta preview";
  brand.appendChild(badge);
}

export default function Demo015Restore() {
  const [recoveryTarget, setRecoveryTarget] = useState<HTMLElement | null>(null);
  const [config, setConfig] = useState<RouterConfig | null>(null);
  const [recoveryError, setRecoveryError] = useState("");

  useEffect(() => {
    if (!isDemoMode) return;
    document.documentElement.classList.add("demo-015-preview");

    const sync = () => {
      patchNetworkPage();
      patchOverview();
      patchRecoveryCopy();
      patchPreviewBadge();

      const recovery = document.getElementById("recovery");
      let slot = recovery?.querySelector<HTMLElement>(".demo-015-recovery-slot") ?? null;
      if (recovery && !slot) {
        slot = document.createElement("div");
        slot.className = "demo-015-recovery-slot";
        recovery.appendChild(slot);
      }
      setRecoveryTarget(slot);
    };

    sync();
    const observer = new MutationObserver(sync);
    observer.observe(document.getElementById("root") ?? document.body, { childList: true, subtree: true });
    window.addEventListener("hashchange", sync);
    window.addEventListener("minimalrouter:wan-speed-estimate", sync);
    return () => {
      observer.disconnect();
      window.removeEventListener("hashchange", sync);
      window.removeEventListener("minimalrouter:wan-speed-estimate", sync);
      document.documentElement.classList.remove("demo-015-preview");
    };
  }, []);

  useEffect(() => {
    if (!isDemoMode || !recoveryTarget) return;
    let cancelled = false;
    void apiFetch("/api/v1/config")
      .then((response) => response.ok ? response.json() : Promise.reject(new Error("Configuration unavailable")))
      .then((body: RouterConfig) => { if (!cancelled) setConfig(body); })
      .catch((error) => { if (!cancelled) setRecoveryError(error instanceof Error ? error.message : "Recovery tools unavailable"); });
    return () => { cancelled = true; };
  }, [recoveryTarget]);

  if (!isDemoMode || !recoveryTarget || !config) return null;
  return createPortal(
    <div className="demo-015-recovery-moved">
      {recoveryError && <div className="dashboard-alert is-error" role="alert">{recoveryError}</div>}
      <RecoveryToolsPanel config={config} onError={setRecoveryError} />
    </div>,
    recoveryTarget,
  );
}
