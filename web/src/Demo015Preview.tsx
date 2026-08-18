import { useEffect, useState } from "react";
import { createPortal } from "react-dom";
import type { RouterConfig } from "./api-types";
import RecoveryToolsPanel from "./components/RecoveryToolsPanel";
import { apiFetch } from "./lib/api";
import { isDemoMode } from "./lib/demoApi";

const DEMO_WAN_ESTIMATE = { download_mbps: 600, upload_mbps: 400, measured_at: Date.now() };
const DEMO_QR = "data:image/png;base64,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";

const STATUS_VARIANTS: Record<string, "good" | "warning" | "bad" | "muted"> = {
  Allow: "good",
  Connected: "good",
  Enabled: "good",
  Healthy: "good",
  Online: "good",
  Protected: "good",
  Recommended: "good",
  Checking: "warning",
  Degraded: "warning",
  Warning: "warning",
  Deny: "bad",
  Disconnected: "bad",
  Error: "bad",
  Disabled: "muted",
  Paused: "muted",
};

const BUTTON_ICONS: Array<[RegExp, string]> = [
  [/^save\b/i, "✓"],
  [/^add\b/i, "+"],
  [/^reserve\b/i, "+"],
  [/^download\b/i, "↓"],
  [/^export\b/i, "⇩"],
  [/^restore\b/i, "↻"],
  [/^validate\b/i, "✓"],
  [/^preview\b/i, "⌕"],
  [/^generate\b/i, "◇"],
  [/^wake\b/i, "↗"],
  [/^pause\b/i, "Ⅱ"],
  [/^enable\b/i, "✓"],
  [/^(remove|delete)\b/i, "×"],
];

if (isDemoMode && typeof window !== "undefined") {
  try {
    window.localStorage.setItem("minimalrouter:wan-speed-estimate", JSON.stringify(DEMO_WAN_ESTIMATE));
  } catch {
    // Demo preview still works when localStorage is disabled.
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
    if (password && password.placeholder !== "Enter a new PPPoE password (optional)") {
      password.placeholder = "Enter a new PPPoE password (optional)";
    }
    addSaveButton(wan, "Save WAN", "demo-015-save-wan");
  }

  if (lan) addSaveButton(lan, "Save LAN & DHCP", "demo-015-save-lan");

  const directActions = Array.from(form.children).filter((child) => child.classList.contains("form-actions"));
  directActions.at(-1)?.classList.add("demo-015-original-network-save");
}

function makeQuickStatusCard(label: string, value: string, meta: string, tone: string) {
  const card = document.createElement("article");
  card.className = `demo-015-quick-card is-${tone}`;
  const top = document.createElement("div");
  top.className = "demo-015-quick-card-top";
  const title = document.createElement("span");
  title.textContent = label;
  const dot = document.createElement("i");
  dot.setAttribute("aria-hidden", "true");
  top.append(title, dot);
  const strong = document.createElement("strong");
  strong.textContent = value;
  const small = document.createElement("small");
  small.textContent = meta;
  card.append(top, strong, small);
  return card;
}

function patchOverview() {
  const session = Array.from(document.querySelectorAll<HTMLElement>(".overview-wan-main > div")).find((element) =>
    element.querySelector("strong")?.textContent?.trim() === "PPPoE",
  );
  if (session) {
    let estimate = session.querySelector<HTMLElement>(".demo-015-session-estimate");
    if (!estimate) {
      estimate = document.createElement("small");
      estimate.className = "demo-015-session-estimate";
      session.appendChild(estimate);
    }
    if (estimate.textContent !== "~ 600 Mb / 400 Mb") estimate.textContent = "~ 600 Mb / 400 Mb";
  }

  const wan = document.querySelector<HTMLElement>(".classic-dashboard-overview .overview-wan-card");
  if (!wan || document.querySelector(".demo-015-quick-status-grid")) return;
  const grid = document.createElement("div");
  grid.className = "demo-015-quick-status-grid";
  [
    ["Internet", "Online", "PPPoE uplink", "good"],
    ["LAN clients", "4 active", "Current leases", "good"],
    ["WireGuard", "2 peers", "Remote access ready", "good"],
    ["DNS", "Protected", "Filtering active", "good"],
    ["Firewall", "Default deny", "Stateful policy", "good"],
    ["System", "Healthy", "No recovery needed", "good"],
  ].forEach(([label, value, meta, tone]) => grid.appendChild(makeQuickStatusCard(label, value, meta, tone)));
  wan.insertAdjacentElement("afterend", grid);
}

function patchWireGuardSuccess() {
  const callout = document.querySelector<HTMLElement>(".wg-callout");
  if (!callout || callout.querySelector(".wg-qr")) return;
  const wrapper = document.createElement("div");
  wrapper.className = "wg-qr demo-015-qr";
  const image = document.createElement("img");
  image.src = DEMO_QR;
  image.alt = "Demo WireGuard QR code";
  wrapper.appendChild(image);
  callout.insertBefore(wrapper, callout.firstChild);
}

function patchRecoveryCopy() {
  const recovery = document.getElementById("recovery");
  if (recovery) {
    const title = recovery.querySelector<HTMLElement>(".dashboard-section-heading h2");
    if (title && title.textContent !== "Recovery") title.textContent = "Recovery";
    const copy = recovery.querySelector<HTMLElement>(".dashboard-section-heading .section-copy");
    const recoveryCopy = "Back up your Minimal Router configuration, restore from an encrypted backup, or migrate settings from pfSense.";
    if (copy && copy.textContent !== recoveryCopy) copy.textContent = recoveryCopy;
  }

  document.querySelectorAll<HTMLElement>(".security-recovery-card").forEach((card) => {
    const heading = card.querySelector<HTMLElement>(".card-title-row h3");
    if (heading && heading.textContent !== "Recovery tools") heading.textContent = "Recovery tools";
    const paragraph = card.querySelector<HTMLElement>(".card-title-row p");
    const toolsCopy = "Encrypted backups, restore validation, pfSense migration and redacted diagnostics.";
    if (paragraph && paragraph.textContent !== toolsCopy) paragraph.textContent = toolsCopy;

    const details = Array.from(card.querySelectorAll<HTMLDetailsElement>("details"));
    const labels = [
      "Encrypted Minimal Router backup (.mrbak)",
      "Restore encrypted backup",
      "Migrate from pfSense config.xml",
    ];
    details.forEach((detail, index) => {
      const summary = detail.querySelector<HTMLElement>("summary");
      if (!summary || summary.querySelector(".demo-recovery-label")) return;
      summary.textContent = "";
      const number = document.createElement("span");
      number.className = `demo-recovery-index is-${index + 1}`;
      number.textContent = String(index + 1);
      const label = document.createElement("span");
      label.className = "demo-recovery-label";
      label.textContent = labels[index] || `Recovery step ${index + 1}`;
      summary.append(number, label);
      if (index === 0 || index === 2) {
        const badge = document.createElement("span");
        badge.className = `demo-recovery-badge ${index === 0 ? "is-recommended" : "is-migration"}`;
        badge.textContent = index === 0 ? "Recommended" : "Migration only";
        summary.appendChild(badge);
      }
      const chevron = document.createElement("span");
      chevron.className = "demo-recovery-chevron";
      chevron.setAttribute("aria-hidden", "true");
      chevron.textContent = "›";
      summary.appendChild(chevron);
    });

    if (!card.querySelector(".demo-recovery-legend")) {
      const legend = document.createElement("div");
      legend.className = "demo-recovery-legend";
      legend.innerHTML = "<span>ⓘ</span><p><strong>.mrbak</strong> backups are for Minimal Router only.</p><i></i><p><strong>pfSense config.xml</strong> is for migration only and is not a Minimal Router backup.</p>";
      card.appendChild(legend);
    }
  });
}

function patchPreviewBadge() {
  const brand = document.querySelector<HTMLElement>(".dashboard-brand");
  if (!brand) return;
  let badge = brand.querySelector<HTMLElement>(".demo-015-beta-badge");
  if (!badge) {
    badge = document.createElement("span");
    badge.className = "demo-015-beta-badge";
    brand.appendChild(badge);
  }
  badge.textContent = "v0.1.5 beta · visual pass 2";
}

function decorateButtons() {
  document.querySelectorAll<HTMLElement>("button.button, a.button").forEach((button) => {
    const text = button.textContent?.trim() || "";
    if (!button.dataset.demoIcon) {
      const match = BUTTON_ICONS.find(([pattern]) => pattern.test(text));
      if (match) {
        button.dataset.demoIcon = match[1];
        button.classList.add("demo-icon-button");
      }
    }
    if (/^(remove|delete|apply validated|apply pfsense)/i.test(text)) button.classList.add("demo-destructive-action");
  });
}

function decorateStatuses() {
  const selector = "td, .wg-client-toggle span, .dashboard-callout strong, .overview-wan-card header b";
  document.querySelectorAll<HTMLElement>(selector).forEach((element) => {
    if (element.dataset.demoStatusWrapped === "true" || element.children.length > 0) return;
    const text = element.textContent?.trim() || "";
    const variant = STATUS_VARIANTS[text];
    if (!variant) return;
    const badge = document.createElement("span");
    badge.className = `demo-status-badge is-${variant}`;
    badge.textContent = text;
    element.textContent = "";
    element.appendChild(badge);
    element.dataset.demoStatusWrapped = "true";
  });
}

function decorateEmptyStates() {
  document.querySelectorAll<HTMLElement>(".empty-state, .elegant-empty, .dns-records-empty").forEach((element) => {
    element.classList.add("demo-empty-state");
  });
}

function markVisualSystem() {
  document.querySelectorAll<HTMLElement>(".dashboard-section-heading").forEach((heading) => heading.classList.add("demo-page-heading"));
  document.querySelectorAll<HTMLElement>(".table-card, .modern-device-section").forEach((card) => card.classList.add("demo-unified-table-card"));
  document.querySelectorAll<HTMLElement>(".settings-form").forEach((form) => form.classList.add("demo-unified-form"));
}

export default function Demo015Preview() {
  const [recoveryTarget, setRecoveryTarget] = useState<HTMLElement | null>(null);
  const [config, setConfig] = useState<RouterConfig | null>(null);
  const [recoveryError, setRecoveryError] = useState("");

  useEffect(() => {
    if (!isDemoMode) return;
    document.documentElement.classList.add("demo-015-preview");

    const sync = () => {
      patchNetworkPage();
      patchOverview();
      patchWireGuardSuccess();
      patchRecoveryCopy();
      patchPreviewBadge();
      decorateButtons();
      decorateStatuses();
      decorateEmptyStates();
      markVisualSystem();

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
