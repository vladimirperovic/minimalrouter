import { useCallback, useEffect, useRef, useState } from "react";
import { apiFetch } from "./api";
import { isDemoMode } from "./demoApi";

export type UpdateOperationState =
  | "queued"
  | "downloading"
  | "verifying"
  | "staging"
  | "activating"
  | "checking_health"
  | "succeeded"
  | "failed"
  | "rolling_back"
  | "rolled_back"
  | "recovery_required";

export type UpdateOperation = {
  id: string;
  state: UpdateOperationState;
  from_version: string;
  target_version: string;
  candidate_id?: string;
  source?: string;
  started_at?: string;
  completed_at?: string;
  error_code?: string;
  error?: string;
};

export type FirmwareStatus = {
  enabled?: boolean;
  current_version?: string;
  running_version?: string;
  previous_version?: string;
  pending_version?: string;
  channel?: string;
  latest_version?: string;
  target_version?: string;
  candidate_id?: string;
  update_available?: boolean;
  can_install?: boolean;
  blocked_reason?: string;
  prerelease?: boolean;
  published_at?: string;
  release_url?: string;
  release_notes?: string;
  checked_at?: string;
  last_successful_check_at?: string;
  stale?: boolean;
  check_error?: string;
  rate_limited?: boolean;
  operation?: UpdateOperation | null;
};

const TERMINAL_STATES: UpdateOperationState[] = ["succeeded", "failed", "rolled_back", "recovery_required"];

export function isTerminal(state?: UpdateOperationState) {
  return Boolean(state && TERMINAL_STATES.includes(state));
}

// Phase labels describe what the appliance is actually doing. There is no
// synthetic percentage: the server reports phases, not bytes, and inventing a
// progress bar would be describing work nobody measured.
export const UPDATE_PHASE_LABEL: Record<UpdateOperationState, string> = {
  queued: "Preparing",
  downloading: "Downloading the signed release",
  verifying: "Verifying the signature",
  staging: "Staging the new slot",
  activating: "Activating and restarting services",
  checking_health: "Checking the new version",
  succeeded: "Update complete",
  failed: "Update failed",
  rolling_back: "Restoring the previous version",
  rolled_back: "Previous version restored",
  recovery_required: "Needs attention",
};

// Explanations for every blocked_reason the server can return. The dashboard
// never parses prose to decide what to show.
export const UPDATE_BLOCK_EXPLANATION: Record<string, string> = {
  missing_trust_key: "Updates are disabled because no trusted signing key is installed on this appliance.",
  missing_update_helper: "The privileged update helper is not installed. Run the full signed distribution installer once to enable dashboard updates.",
  missing_baseline: "No rollback baseline is registered yet. Run the full signed distribution installer before using web updates.",
  pending_activation: "A verified release is already waiting to be activated.",
  unsupported_architecture: "This architecture has no published update payload.",
  insufficient_space: "There is not enough free space to download, unpack and stage a new release safely.",
  configuration_pending: "A configuration change is being applied or is waiting for confirmation. Finish it first.",
  recovery_required: "The appliance needs canonical recovery before it can install an update.",
  update_in_progress: "An update is already running.",
  check_unavailable: "The release check has not succeeded recently, so no update can be offered right now.",
  no_candidate: "No installable release was found for this appliance.",
  already_current: "This appliance is already on the newest published release.",
  local_state_unavailable: "The local update state could not be read.",
  candidate_superseded: "The confirmed release is no longer the current candidate. Review the new one and confirm again.",
  read_only_session: "This session is read-only.",
};

type UpdatesState = {
  status: FirmwareStatus | null;
  loading: boolean;
  /** The appliance could not be reached — during activation this is expected. */
  reconnecting: boolean;
  error: string;
  busy: boolean;
};

const ACTIVE_POLL_MS = 3000;
const IDLE_POLL_MS = 60000;

/**
 * useUpdates owns all update state for the whole dashboard, so the sidebar
 * badge, the profile menu and the dialog always agree. Nothing about an
 * update's outcome is inferred locally: the appliance is asked, every time.
 */
export function useUpdates(enabled: boolean) {
  const [state, setState] = useState<UpdatesState>({
    status: null,
    loading: false,
    reconnecting: false,
    error: "",
    busy: false,
  });
  const reloadedRef = useRef(false);
  const mountedRef = useRef(true);

  useEffect(() => () => { mountedRef.current = false; }, []);

  const refresh = useCallback(async () => {
    if (!enabled || isDemoMode) return;
    try {
      const response = await apiFetch("/api/v1/firmware/status");
      const body = (await response.json().catch(() => ({}))) as FirmwareStatus;
      if (!mountedRef.current) return;
      if (!response.ok) {
        setState((previous) => ({ ...previous, reconnecting: false, error: "Update status unavailable." }));
        return;
      }
      setState((previous) => ({ ...previous, status: body, reconnecting: false, error: "" }));
    } catch {
      // routerd is unreachable. During activation both daemons restart, so
      // this is a reconnect, not a failure, and must never be shown as one.
      if (mountedRef.current) setState((previous) => ({ ...previous, reconnecting: true }));
    }
  }, [enabled]);

  const operationState = state.status?.operation?.state;
  const active = Boolean(operationState && !isTerminal(operationState));

  useEffect(() => {
    if (!enabled || isDemoMode) return;
    void refresh();
    const interval = window.setInterval(() => void refresh(), active || state.reconnecting ? ACTIVE_POLL_MS : IDLE_POLL_MS);
    return () => window.clearInterval(interval);
  }, [enabled, refresh, active, state.reconnecting]);

  // A completed update is confirmed by the appliance reporting the new version
  // as running, never by a local timer. The reload happens once.
  useEffect(() => {
    const operation = state.status?.operation;
    if (!operation || operation.state !== "succeeded" || reloadedRef.current) return;
    if (state.status?.running_version && operation.target_version &&
        state.status.running_version !== operation.target_version) return;
    reloadedRef.current = true;
    const timer = window.setTimeout(() => window.location.reload(), 900);
    return () => window.clearTimeout(timer);
  }, [state.status]);

  const checkNow = useCallback(async () => {
    if (!enabled || isDemoMode) return;
    setState((previous) => ({ ...previous, busy: true, error: "" }));
    try {
      const response = await apiFetch("/api/v1/firmware/check", { method: "POST" });
      const body = (await response.json().catch(() => ({}))) as FirmwareStatus & { error?: string };
      if (!mountedRef.current) return;
      if (response.status === 429) {
        setState((previous) => ({ ...previous, status: body, busy: false, error: "A release check ran a moment ago. Try again shortly." }));
        return;
      }
      if (!response.ok) {
        setState((previous) => ({ ...previous, busy: false, error: body.error || "The release check could not run." }));
        return;
      }
      setState((previous) => ({ ...previous, status: body, busy: false, error: "" }));
    } catch {
      if (mountedRef.current) setState((previous) => ({ ...previous, busy: false, error: "The appliance could not be reached." }));
    }
  }, [enabled]);

  const setChannel = useCallback(async (channel: "stable" | "beta") => {
    setState((previous) => ({ ...previous, busy: true, error: "" }));
    try {
      const response = await apiFetch("/api/v1/firmware/channel", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ channel }),
      });
      const body = (await response.json().catch(() => ({}))) as FirmwareStatus & { error?: string };
      if (!mountedRef.current) return;
      if (!response.ok) {
        setState((previous) => ({ ...previous, busy: false, error: body.error || "The channel could not be changed." }));
        return;
      }
      setState((previous) => ({ ...previous, status: body, busy: false }));
      await checkNow();
    } catch {
      if (mountedRef.current) setState((previous) => ({ ...previous, busy: false, error: "The appliance could not be reached." }));
    }
  }, [checkNow]);

  /**
   * install confirms exactly the candidate the operator was shown. The
   * idempotency key makes a retried request after a lost response return the
   * same operation instead of installing twice.
   */
  const install = useCallback(async () => {
    const status = state.status;
    if (!status?.candidate_id || !status.target_version) return;
    setState((previous) => ({ ...previous, busy: true, error: "" }));
    try {
      const response = await apiFetch("/api/v1/firmware/update", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          candidate_id: status.candidate_id,
          target_version: status.target_version,
          idempotency_key: `${status.candidate_id}:${status.current_version ?? ""}`,
        }),
      });
      const body = (await response.json().catch(() => ({}))) as {
        error?: string;
        blocked_reason?: string;
        status?: FirmwareStatus;
        operation?: UpdateOperation;
      };
      if (!mountedRef.current) return;
      if (!response.ok) {
        const explanation = body.blocked_reason ? UPDATE_BLOCK_EXPLANATION[body.blocked_reason] : "";
        setState((previous) => ({
          ...previous,
          status: body.status ?? previous.status,
          busy: false,
          error: explanation || body.error || "The update could not be started.",
        }));
        return;
      }
      setState((previous) => ({
        ...previous,
        busy: false,
        status: body.status ? { ...body.status, operation: body.operation ?? body.status.operation } : previous.status,
      }));
      // From here the appliance owns the work: closing this dialog, or the
      // tab, no longer changes whether the update completes.
      void refresh();
    } catch {
      if (mountedRef.current) {
        setState((previous) => ({ ...previous, busy: false, reconnecting: true }));
      }
    }
  }, [refresh, state.status]);

  const uploadSignedBuild = useCallback(async (data: FormData) => {
    setState((previous) => ({ ...previous, busy: true, error: "" }));
    try {
      const response = await apiFetch("/api/v1/firmware/upload", { method: "POST", body: data });
      const body = (await response.json().catch(() => ({}))) as { error?: string };
      if (!mountedRef.current) return;
      if (!response.ok) {
        setState((previous) => ({ ...previous, busy: false, error: body.error || "The signed build was not installed." }));
        return;
      }
      setState((previous) => ({ ...previous, busy: false }));
      void refresh();
    } catch {
      if (mountedRef.current) setState((previous) => ({ ...previous, busy: false, reconnecting: true }));
    }
  }, [refresh]);

  return { ...state, active, refresh, checkNow, setChannel, install, uploadSignedBuild };
}

export type UpdatesController = ReturnType<typeof useUpdates>;

/** Short label for the sidebar badge. */
export function updateBadgeLabel(status: FirmwareStatus | null): string | null {
  if (!status?.update_available || !status.target_version) return null;
  return `New version · v${status.target_version}`;
}
