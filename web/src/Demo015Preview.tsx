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
  // The "~ 600 Mb / 400 Mb" line pass 2 wrote under the PPPoE session is gone:
  // the Line estimate tile in the same card already carries that number, and
  // the seeded DEMO_WAN_ESTIMATE still feeds it.

  // Anchor the strip to the hero as a whole, not to the WAN card. The WAN card
  // sits inside `.overview-hero-command`, which is itself a fixed-track CSS
  // grid, so inserting after it made the strip occupy one ~400px track and
  // squeezed six cards into ~39px each while displacing `.overview-assurance`.
  const hero = document.querySelector<HTMLElement>(".classic-dashboard-overview .overview-status-hero");
  if (!hero) return;

  const existing = document.querySelector<HTMLElement>(".demo-015-quick-status-grid");
  if (existing) {
    // Re-seat a strip left in the hero grid by an earlier pass.
    if (existing.previousElementSibling !== hero) hero.insertAdjacentElement("afterend", existing);
    return;
  }

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
  hero.insertAdjacentElement("afterend", grid);
}

function trimmed(node: Element | null | undefined) {
  return node?.textContent?.trim() ?? "";
}

// Every number on the Overview hero is jargon to someone. Keyed by the tile's
// own label so the map stays readable next to the UI it annotates.
const METRIC_TIPS: Record<string, string> = {
  "Latency": "Round-trip time to the probe targets. Under about 30 ms feels instant; sustained spikes point at a congested or failing line.",
  "Jitter": "How much the latency varies between samples. Calls and games suffer more from high jitter than from steady high latency.",
  "Line estimate": "Throughput measured by the last speed test — not the rate your ISP advertises.",
  "Probe targets": "How many external hosts are pinged to judge link quality. More targets make the verdict less dependent on any one host.",
  "Conntrack": "Connections the firewall is currently tracking, against the kernel's table limit. Nearing the limit drops new connections.",
  "Update trust": "Whether package signatures are checked before an update is allowed to install.",
  "Last admin access": "The most recent sign-in to this dashboard, with the address it came from. An address you do not recognise is worth investigating.",
  "Time synchronization": "Clock sync state. Certificate validation and log timestamps are only trustworthy while the clock is accurate.",
};

// Tooltips are real elements rather than a CSS `::after`, so assistive
// technology can reach the text, and they reveal on focus as well as hover so
// the explanation is not mouse-only.
function attachMetricTip(host: HTMLElement, label: string) {
  const tip = METRIC_TIPS[label];
  if (!tip) return;
  let bubble = host.querySelector<HTMLElement>(":scope > .demo-metric-tip");
  if (!bubble) {
    bubble = document.createElement("span");
    bubble.className = "demo-metric-tip";
    bubble.setAttribute("role", "note");
    host.appendChild(bubble);
    host.classList.add("demo-has-tip");
    if (!host.hasAttribute("tabindex")) host.setAttribute("tabindex", "0");
  }
  if (bubble.textContent !== tip) bubble.textContent = tip;
}

function patchMetricTips() {
  document.querySelectorAll<HTMLElement>(".overview-wan-quality > span").forEach((tile) => {
    attachMetricTip(tile, trimmed(tile.querySelector("small")));
  });
  document.querySelectorAll<HTMLElement>(".overview-assurance > div").forEach((card) => {
    attachMetricTip(card, trimmed(card.querySelector("small")));
  });
}

// Splits `<strong>Synchronized<em>02:49 PM</em></strong>` into its headline and
// its trailing meta line.
function readDiagnostic(item: Element | null) {
  const strong = item?.querySelector("strong");
  const meta = trimmed(strong?.querySelector("em"));
  const value = strong ? trimmed(strong).slice(0, trimmed(strong).length - meta.length).trim() : "";
  return { value, meta, positive: strong?.classList.contains("is-positive") ?? false };
}

// Both diagnostics read better inside the hero than in a separate strip below
// it: time sync is an assurance statement like the two cards above it, and
// conntrack is a link-quality number like the ones already in the WAN card.
//
// These are React-rendered nodes carrying live values, so they are mirrored
// into demo-owned elements on every pass rather than reparented — moving a node
// out from under React risks it removing a child it no longer owns. The markup
// deliberately matches each destination, whose CSS selects on structure
// (`.overview-assurance > div`, `.overview-wan-quality span`), so the copies
// inherit the surrounding style with no extra rules.
function patchOverviewDiagnostics() {
  const strip = document.querySelector<HTMLElement>(".overview-diagnostic-strip");
  if (!strip) return;

  const source = (label: string) =>
    Array.from(strip.children).find((child) => trimmed(child.querySelector("small")) === label) ?? null;

  const assurance = document.querySelector<HTMLElement>(".overview-assurance");
  const timeSource = source("Time synchronization");
  if (assurance && timeSource) {
    let card = assurance.querySelector<HTMLElement>(".demo-015-timesync-card");
    if (!card) {
      card = document.createElement("div");
      card.className = "demo-015-timesync-card";
      const icon = document.createElement("span");
      const glyph = timeSource.querySelector("svg");
      if (glyph) icon.appendChild(glyph.cloneNode(true));
      const body = document.createElement("p");
      body.append(document.createElement("small"), document.createElement("strong"), document.createElement("em"));
      card.append(icon, body);
      assurance.appendChild(card);
    }
    const { value, meta, positive } = readDiagnostic(timeSource);
    const label = card.querySelector("small")!;
    const headline = card.querySelector("strong")!;
    const detail = card.querySelector("em")!;
    if (label.textContent !== "Time synchronization") label.textContent = "Time synchronization";
    if (headline.textContent !== value) headline.textContent = value;
    if (detail.textContent !== meta) detail.textContent = meta;
    headline.classList.toggle("is-positive", positive);
  }

  const quality = document.querySelector<HTMLElement>(".overview-wan-quality");
  const conntrackSource = source("Conntrack");
  if (quality && conntrackSource) {
    let tile = quality.querySelector<HTMLElement>(".demo-015-conntrack-tile");
    if (!tile) {
      tile = document.createElement("span");
      tile.className = "demo-015-conntrack-tile";
      tile.append(document.createElement("small"), document.createElement("strong"));
      quality.appendChild(tile);
    }
    const { value } = readDiagnostic(conntrackSource);
    const label = tile.querySelector("small")!;
    const headline = tile.querySelector("strong")!;
    if (label.textContent !== "Conntrack") label.textContent = "Conntrack";
    if (headline.textContent !== value) headline.textContent = value;
  }
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
  badge.textContent = "v0.1.5 beta · final dashboard design";
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
    // The badge this function injects is itself a <span>, so a descendant
    // selector such as `.wg-client-toggle span` matches it on the next observer
    // pass. Without this guard the badge is re-wrapped forever and the
    // MutationObserver never settles.
    if (element.closest(".demo-status-badge")) return;
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
      patchOverviewDiagnostics();
      patchMetricTips();
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

    const root = document.getElementById("root") ?? document.body;
    const observer = new MutationObserver(() => schedule());
    const observe = () => observer.observe(root, { childList: true, subtree: true });

    // This overlay reacts to DOM changes by making DOM changes. Running the
    // patches straight from the observer callback feeds every edit back in as a
    // fresh record, so a single non-idempotent patch locks the main thread.
    // Detaching around the patch pass, and coalescing bursts into one frame,
    // keeps the overlay bounded by React's render rate instead.
    // A timer rather than requestAnimationFrame: rAF is suspended in a hidden
    // tab, which would leave the overlay unapplied until the tab is focused.
    let scheduled = 0;
    const schedule = () => {
      if (scheduled) return;
      scheduled = window.setTimeout(() => {
        scheduled = 0;
        observer.disconnect();
        try {
          sync();
        } finally {
          observer.takeRecords();
          observe();
        }
      }, 0);
    };

    sync();
    observe();
    window.addEventListener("hashchange", schedule);
    window.addEventListener("minimalrouter:wan-speed-estimate", schedule);
    return () => {
      observer.disconnect();
      window.clearTimeout(scheduled);
      window.removeEventListener("hashchange", schedule);
      window.removeEventListener("minimalrouter:wan-speed-estimate", schedule);
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
