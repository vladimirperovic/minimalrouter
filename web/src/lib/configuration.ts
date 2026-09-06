import { useSyncExternalStore } from "react";
import type { PendingTransaction, RouterConfig } from "../api-types";
import { apiFetch, responseError } from "./api";
import { isDemoMode } from "./demoApi";

const REQUIRED_CONFIG_SECTIONS = [
  "wan", "lan", "dhcp", "firewall", "wireguard",
  "cloudflare", "squid_proxy", "adguard", "qos", "wifi",
] as const;

function isRenderableConfig(value: RouterConfig | null): value is RouterConfig {
  if (!value || typeof value !== "object") return false;
  return REQUIRED_CONFIG_SECTIONS.every((section) => {
    const item = (value as unknown as Record<string, unknown>)[section];
    return item !== null && typeof item === "object";
  });
}

let snapshot: RouterConfig | null = null;
let generation = 0;
const listeners = new Set<() => void>();
const subscribe = (listener: () => void) => { listeners.add(listener); return () => { listeners.delete(listener); }; };
export function useConfiguration() { return useSyncExternalStore(subscribe, () => snapshot); }
export function clearConfiguration() { generation++; snapshot = null; listeners.forEach(listener => listener()); }

export async function readConfiguration(init: RequestInit = {}): Promise<RouterConfig> {
  const requestGeneration = generation;
  const response = await apiFetch("/api/v1/config", init);
  if (!response.ok) throw new Error(await responseError(response, `Configuration unavailable (${response.status})`));
  const config = await response.json() as RouterConfig;
  if (!isRenderableConfig(config) || !Number.isSafeInteger(config.revision)) throw new Error("Configuration unavailable");
  if (init.signal?.aborted || requestGeneration !== generation) throw new DOMException("Configuration request aborted", "AbortError");
  if (!snapshot || config.revision >= snapshot.revision) {
    snapshot = config;
    listeners.forEach(listener => listener());
  }
  // Writers receive an independent candidate, never the shared canonical value.
  return structuredClone(snapshot!);
}

export type ConfigApplyResult = { cancelled: true } | { cancelled: false; transaction: PendingTransaction; refreshError?: string };

export async function previewAndApplyConfig(candidate: RouterConfig): Promise<ConfigApplyResult> {
  const body = JSON.stringify(candidate);
  if (!isDemoMode && !await confirmConfigChange(body)) return { cancelled: true };
  const response = await apiFetch("/api/v1/config", { method: "PUT", body });
  if (!response.ok) throw new Error(await responseError(response, `Configuration apply failed (${response.status})`));
  const transaction = await response.json() as PendingTransaction;
  // Never infer the canonical state from a provisional candidate.
  let refreshError: string | undefined;
  try { await readConfiguration({ cache: "reload" }); }
  catch { refreshError = "The configuration change was accepted, but its current state could not be refreshed. Refresh before making another change."; }
  window.dispatchEvent(new CustomEvent("minimalrouter:config-applied", { detail: { transaction, refreshError } }));
  return { cancelled: false, transaction, refreshError };
}

type ChangePreview = {
  changes?: string[];
  risk?: string;
  requires_confirmation?: boolean;
  rollback_seconds?: number;
  expected_interruption?: string;
  error?: string;
};

async function confirmConfigChange(body: string): Promise<boolean> {
  const response = await apiFetch("/api/v1/config/preview", {
    method: "POST",
    body,
  });
  const preview = (await response.json().catch(() => ({}))) as ChangePreview;
  if (!response.ok) {
    throw new Error(preview.error || `Change preview failed (${response.status})`);
  }
  const changes = Array.isArray(preview.changes) && preview.changes.length > 0
    ? preview.changes.map((item) => `• ${item}`).join("\n")
    : "• No effective configuration changes";
  const risk = String(preview.risk || "unknown").toUpperCase();
  const rollback = preview.requires_confirmation
    ? `Automatic rollback: ARMED (${preview.rollback_seconds || 90}s unless confirmed)`
    : "Automatic rollback: not required for this change";
  const message = [
    "Smart Change Preview",
    "",
    changes,
    "",
    `Risk: ${risk}`,
    preview.expected_interruption || "Affected services may restart briefly.",
    rollback,
    "",
    "Apply these changes?",
  ].join("\n");
  return window.confirm(message);
}

