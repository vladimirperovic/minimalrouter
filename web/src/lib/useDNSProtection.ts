import { useCallback, useState, useSyncExternalStore } from "react";
import type { DNSFilterPolicy } from "../api-types";
import { apiFetch, responseError } from "./api";
import { parseDNSStatus, sameDNSPolicy, type DNSOperation, type DNSStatus } from "./dnsProtection";
import { useVisiblePolling } from "./useVisiblePolling";

type Pending = { id?: string; policy: DNSFilterPolicy; kind: "policy" | "refresh" };
type State = { status: DNSStatus | null; draft: DNSFilterPolicy | null; pending: Pending | null; saving: boolean; error: string; statusError: string; notice: string; observedAt: number };
const initial = (): State => ({ status: null, draft: null, pending: null, saving: false, error: "", statusError: "", notice: "", observedAt: 0 });
let state = initial();
let epoch = 0;
let session = 0;
const listeners = new Set<() => void>();
const subscribe = (listener: () => void) => { listeners.add(listener); return () => { listeners.delete(listener); }; };
function publish(next: Partial<State>) { state = { ...state, ...next }; listeners.forEach(listener => listener()); }
// Keep drafts and accepted jobs across route changes, but never across logout.
function reset() { epoch++; session++; state = initial(); listeners.forEach(listener => listener()); }
window.addEventListener("minimalrouter:unauthorized", reset);
window.addEventListener("minimalrouter:configuration-cleared", reset);
window.addEventListener("beforeunload", event => {
  if (state.draft || state.pending) { event.preventDefault(); event.returnValue = ""; }
});

async function load(signal: AbortSignal) {
  const generation = epoch;
  const owns = () => !signal.aborted && generation === epoch;
  try {
    const response = await apiFetch("/api/v1/dns-filter", { signal, cache: "no-store" });
    if (!response.ok) throw new Error(await responseError(response, "DNS filter status unavailable"));
    const next = parseDNSStatus(await response.json());
    if (!owns()) return;
    let operation = next.operation;
    const pending = state.pending;
    if (pending?.id && operation?.id !== pending.id) {
      const history = await apiFetch("/api/v1/dns-filter/operations", { signal, cache: "no-store" });
      if (history.ok) { const result = await history.json() as { operations: DNSOperation[] }; operation = result.operations?.find(op => op.id === pending.id); }
      if (!owns()) return;
    }
    const update: Partial<State> = { status: next, statusError: "", observedAt: Date.now() };
    if (pending) {
      const matchingJob = pending.id && operation?.id === pending.id;
      const verified = matchingJob ? operation?.state === "completed"
        : !next.updating && next.healthy && next.policy.revision === pending.policy.revision + 1 && sameDNSPolicy(next.policy, pending.policy);
      if (verified) {
        update.pending = null;
        if (state.draft && sameDNSPolicy(state.draft, pending.policy)) update.draft = null;
        update.notice = `DNS ${pending.kind === "refresh" ? "lists refreshed" : "protection applied"}. Revision ${operation?.applied_revision ?? next.policy.revision} verified.`;
        update.error = "";
      } else if ((matchingJob && ["failed", "rolled_back", "needs_review"].includes(operation?.state ?? "")) || (!next.updating && next.error)) {
        update.pending = null;
        update.error = operation?.error || next.error || "DNS change needs review. Inspect the applied policy before retrying.";
        update.notice = "Your changes are retained. Review the error and retry when ready.";
      } else if (!next.updating && next.policy.revision > pending.policy.revision + 1 && !operation) {
        update.pending = null;
        update.error = "A newer policy is now applied. The previous request's outcome is unknown; review operation history and your retained changes.";
      }
    }
    publish(update);
  } catch (error) {
    if (owns()) publish({ statusError: error instanceof Error ? error.message : "DNS status unavailable" });
  }
}

export function useDNSProtection(enabled: boolean) {
  const snapshot = useSyncExternalStore(subscribe, () => state);
  const [refreshKey, setRefreshKey] = useState(0);
  const refresh = useCallback(() => setRefreshKey(value => value + 1), []);
  useVisiblePolling(load, snapshot.pending || snapshot.status?.updating ? 3000 : 30000, enabled, String(refreshKey));
  const edit = (policy: DNSFilterPolicy) => publish({ draft: state.status && sameDNSPolicy(policy, state.status.policy) ? null : structuredClone(policy), error: "", notice: "" });
  const discard = () => { if (!state.saving && !state.pending) publish({ draft: null, error: "", notice: "" }); };
  async function apply(kind: "policy" | "refresh") {
    if (state.saving || state.pending || state.status?.updating || !state.status || state.statusError || !enabled || state.status.blockers.length || (state.draft && state.draft.revision !== state.status.policy.revision)) return;
    const policy = structuredClone(kind === "policy" ? state.draft ?? state.status.policy : state.status.policy);
    const requestSession = session;
    epoch++; // A status read issued before this write cannot replace its state.
    publish({ saving: true, error: "", notice: "" });
    let receivedResponse = false;
    let accepted = false;
    try {
      const response = await apiFetch(`/api/v1/dns-filter${kind === "refresh" ? "/refresh" : ""}`, { method: kind === "refresh" ? "POST" : "PUT", ...(kind === "policy" ? { body: JSON.stringify(policy) } : {}) });
      receivedResponse = true;
      if (!response.ok) throw new Error(await responseError(response, "DNS change failed"));
      accepted = true;
      const body = await response.json() as { operation?: DNSOperation };
      if (requestSession !== session) return;
      publish({ pending: { id: body.operation?.id, policy, kind }, notice: "Request accepted. Existing protection remains active until the new policy is verified." });
    } catch (error) {
      if (requestSession === session) publish(accepted
        ? { pending: { policy, kind }, error: "The router accepted the request, but its receipt could not be read. Checking the applied policy; your changes are retained." }
        : !receivedResponse
          ? { statusError: "Checking router after connection failure", error: "Connection lost while sending the change. Its outcome is unknown; review fresh status before retrying. Your changes are retained." }
          : { error: error instanceof Error ? error.message : "DNS change failed" });
    } finally {
      if (requestSession === session) { publish({ saving: false }); refresh(); }
    }
  }

  const busy = snapshot.saving || Boolean(snapshot.pending) || Boolean(snapshot.status?.updating) || !enabled || !snapshot.status || Boolean(snapshot.statusError);
  const conflict = Boolean(snapshot.draft && snapshot.status && snapshot.draft.revision !== snapshot.status.policy.revision);
  return { ...snapshot, policy: snapshot.draft ?? snapshot.status?.policy, busy, conflict, edit, discard, apply, refresh };
}
export type DNSProtectionControl = ReturnType<typeof useDNSProtection>;
