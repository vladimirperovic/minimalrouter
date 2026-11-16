export type DDNSRuntime = { running: boolean; hostname?: string; last_update_epoch?: number; last_ip?: string; resolved_ip?: string; in_sync?: boolean };

export type DDNSState = "disabled" | "unknown" | "stopped" | "running" | "in-sync" | "mismatch";

/**
 * What the dashboard may claim about Dynamic DNS. inadyn only compares the WAN
 * address with its own cache, so "In sync" needs the router's lookup of the
 * public name to agree with the last address sent. A lookup that has not
 * answered yet is unknown, never a mismatch.
 */
export function ddnsState(enabled: boolean, ddns: DDNSRuntime | undefined): DDNSState {
  if (!enabled) return "disabled";
  if (!ddns) return "unknown";
  if (!ddns.running) return "stopped";
  if (ddns.in_sync) return "in-sync";
  if (ddns.resolved_ip) return "mismatch";
  return "running";
}

export const ddnsStateLabel: Record<DDNSState, string> = {
  disabled: "Disabled",
  unknown: "Unknown",
  stopped: "Stopped",
  running: "Service running",
  "in-sync": "In sync",
  mismatch: "Public DNS differs",
};

/** Overview chip tone: a published mismatch or a stopped service is a warning. */
export function ddnsChipTone(state: DDNSState): string {
  if (state === "disabled") return "is-off";
  if (state === "mismatch" || state === "stopped") return "is-warning";
  return state === "unknown" ? "is-info" : "is-good";
}

/** DDNS page status chip tone. */
export function ddnsHeroTone(state: DDNSState): string {
  if (state === "disabled") return "is-off";
  if (state === "mismatch" || state === "stopped") return "is-warning";
  return state === "unknown" ? "is-info" : "";
}

/** The note under the registered address: what public DNS answers for it. */
export function ddnsPublicNote(ddns: DDNSRuntime | undefined): string {
  if (!ddns?.resolved_ip) return "last sent · public DNS not verified yet";
  return ddns.in_sync ? "last sent · public DNS matches" : `last sent · public DNS shows ${ddns.resolved_ip}`;
}
