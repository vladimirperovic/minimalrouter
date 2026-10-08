import { createContext, useCallback, useContext, useState, type ReactNode } from "react";
import { apiFetch, responseError } from "./api";
import { useVisiblePolling } from "./useVisiblePolling";
import type { DNSRiskSummary } from "../api-types";

type State = { summary: DNSRiskSummary | null; error: string; refresh: () => void; revision: number };
const Context = createContext<State>({ summary: null, error: "", refresh: () => {}, revision: 0 });

// One metadata-only poll shared by the bell and the visible panels.
export function DNSRiskProvider({ children }: { children: ReactNode }) {
  const [summary, setSummary] = useState<DNSRiskSummary | null>(null);
  const [error, setError] = useState("");
  const [revision, setRevision] = useState(0);
  const refresh = useCallback(() => setRevision(value => value + 1), []);
  const load = useCallback(async (signal: AbortSignal) => {
    try {
      const response = await apiFetch("/api/v1/dns-activity/alerts/summary", { signal });
      if (!response.ok) throw new Error(await responseError(response, "DNS risk status unavailable"));
      const body = await response.json() as DNSRiskSummary;
      if (typeof body.available !== "boolean" || typeof body.enabled !== "boolean" || !Number.isFinite(body.new_count) || !Number.isFinite(body.total) || !Array.isArray(body.sources) || !body.collection || typeof body.collection.state !== "string") throw new Error("DNS risk monitoring is not available on this firmware");
      if (signal.aborted) return;
      setSummary(body); setError("");
    } catch (e) {
      if (signal.aborted) return;
      setSummary(null); setError(e instanceof Error ? e.message : "DNS risk status unavailable");
    }
  }, []);
  useVisiblePolling(load, 60000, true, String(revision));
  return <Context value={{ summary, error, refresh, revision }}>{children}</Context>;
}

export function useDNSRisk() { return useContext(Context); }

export function riskCoverage(summary: DNSRiskSummary | null): string {
  if (!summary?.available) return "Unavailable";
  if (!summary.enabled) return "Recording off";
  if (summary.error || summary.collection.state !== "active" || summary.dropped_lookups > 0 || (summary.collection.dropped_lookups ?? 0) > 0 || summary.sources.length !== 5 || summary.sources.some(source => source.stale || source.entries === 0 || source.error)) return "Incomplete coverage";
  return "Monitoring";
}
