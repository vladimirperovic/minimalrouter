import { useCallback, useEffect, useState } from "react";
import { apiFetch } from "../lib/api";
import { useVisiblePolling } from "../lib/useVisiblePolling";
import { bootStatus, type Boot } from "../lib/logs";
import type { StorageStatus, SystemStatus } from "../api-types";
import StartupResourceChart from "./StartupResourceChart";

export type StartupDiagnostics = {
  summaries: Boot[]; selected_boot: Boot | null; storage_now: StorageStatus | null;
  collected_at: string | null; errors: string[];
  selected_boot_id: string | null; samples_state: "none" | "loading" | "ready" | "unavailable";
};
type Props = { refreshKey?: number; paused?: boolean; onSnapshot?: (value: StartupDiagnostics) => void };
const MILESTONES = [
  { key: "management_seconds", kind: "management", label: "Management", detail: "management listener bound", tone: "tl-azure" },
  { key: "pppoe_seconds", kind: "pppoe", label: "PPPoE", detail: "ppp0 interface detected", tone: "tl-green" },
  { key: "dns_seconds", kind: "dns", label: "DNS", detail: "system resolver lookup succeeded", tone: "tl-amber" },
  { key: "internet_seconds", kind: "internet", label: "Internet", detail: "TCP port 443 reachable (not an HTTPS test)", tone: "tl-emerald" },
  { key: "wireguard_seconds", kind: "wireguard", label: "WireGuard", detail: "interface detected (not a peer handshake)", tone: "tl-violet" },
] as const;
const STATUS_LABELS: Record<string, string> = {
  ready: "Expected services ready", capturing: "Startup capture in progress", timeout: "Capture timed out — readiness incomplete",
  interrupted: "Capture interrupted", unknown: "Capture ended — outcome not recorded",
};

export default function StartupTimelinePanel({ refreshKey = 0, paused = false, onSnapshot }: Props) {
  const [boots, setBoots] = useState<Boot[]>([]);
  const [selectedID, setSelectedID] = useState("");
  const [detail, setDetail] = useState<Boot | null>(null);
  const [error, setError] = useState("");
  const [detailError, setDetailError] = useState("");
  const [storageError, setStorageError] = useState("");
  const [storage, setStorage] = useState<StorageStatus | null>(null);
  const [collectedAt, setCollectedAt] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [refresh, setRefresh] = useState(0);
  const boot = boots.find(item => item.id === selectedID);
  const status = boot ? bootStatus(boot) : "unknown";
  const refreshToken = `${refreshKey}:${refresh}`;

  const load = useCallback(async (signal: AbortSignal) => {
    setLoading(true);
    const [history, system] = await Promise.allSettled([
      apiFetch("/api/v1/startup/boots", { signal, cache: "no-store" }).then(async response => {
        if (!response.ok) throw new Error(`Startup timeline unavailable (${response.status})`);
        const body = await response.json() as { boots?: Boot[] };
        if (!Array.isArray(body.boots)) throw new Error("Invalid startup response");
        return body.boots;
      }),
      apiFetch("/api/v1/system", { signal, cache: "no-store" }).then(async response => {
        if (!response.ok) throw new Error("Current storage telemetry unavailable");
        return (await response.json() as SystemStatus).runtime?.storage ?? null;
      }),
    ]);
    if (signal.aborted) return;
    setLoading(false);
    if (history.status === "fulfilled") {
      setBoots(history.value);
      setSelectedID(current => history.value.some(item => item.id === current) ? current : history.value[0]?.id ?? "");
      setError("");
      setCollectedAt(new Date().toISOString());
    } else { setError(history.reason instanceof Error ? history.reason.message : "Startup timeline unavailable"); }
    setStorage(system.status === "fulfilled" ? system.value : null);
    setStorageError(system.status === "rejected" ? "Current storage telemetry unavailable" : "");
  }, []);
  useVisiblePolling(load, paused ? 0 : 15_000, true, refreshToken);

  const loadDetail = useCallback(async (signal: AbortSignal) => {
    setDetailError("");
    try {
      const response = await apiFetch(`/api/v1/startup/boots/${encodeURIComponent(selectedID)}`, { signal, cache: "no-store" });
      if (!response.ok) throw new Error(`Selected boot samples unavailable (${response.status})`);
      const body = await response.json() as { boot?: Boot; status?: string };
      if (!body.boot || body.boot.id !== selectedID) throw new Error("Invalid startup detail response");
      if (!signal.aborted) setDetail({ ...body.boot, status: body.status });
    } catch (cause) {
      if (!signal.aborted) { setDetail(null); setDetailError(cause instanceof Error ? cause.message : "Startup samples unavailable"); }
    }
  }, [selectedID]);
  useVisiblePolling(loadDetail, !paused && status === "capturing" ? 15_000 : 0, !!selectedID, refreshToken);
  const selectedDetail = detail?.id === selectedID ? detail : null;

  useEffect(() => {
    onSnapshot?.({ summaries: boots, selected_boot: selectedDetail, selected_boot_id: selectedID || null,
      samples_state: !selectedID ? "none" : detailError ? "unavailable" : selectedDetail ? "ready" : "loading",
      storage_now: storage, collected_at: collectedAt, errors: [error, detailError, storageError].filter(Boolean) });
  }, [boots, selectedDetail, selectedID, storage, collectedAt, error, detailError, storageError, onSnapshot]);

  const items: { offset: number | null; label: string; detail: string; tone: string }[] = [];
  if (boot) {
    items.push({ offset: 0, tone: "tl-ink", label: "System started", detail: new Date(boot.started_at).toLocaleString() });
    for (const milestone of MILESTONES) {
      const seconds = boot.readiness?.[milestone.key];
      if (seconds !== undefined) items.push({ offset: seconds, tone: milestone.tone, label: milestone.label, detail: milestone.detail });
      else if (boot.expected?.includes(milestone.kind)) items.push({ offset: null, tone: "tl-event", label: milestone.label, detail: status === "capturing" ? "Waiting for readiness" : "Not reached during capture" });
    }
    for (const event of boot.events ?? []) items.push({ offset: event.offset_seconds, tone: "tl-event", label: event.kind, detail: event.message });
    items.sort((a, b) => (a.offset ?? Infinity) - (b.offset ?? Infinity) || a.label.localeCompare(b.label));
  }
  const samples = selectedDetail?.samples ?? boot?.samples ?? [];
  const lastSample = boot?.last_sample ?? samples[samples.length - 1];
  const observations = items.filter(item => item.offset !== null).length;
  const peakCPU = samples.length ? Math.max(...samples.map(sample => sample.cpu_percent)) : null;

  return <article className="card table-card startup-timeline">
    <div className="card-title-row"><div><h3>Startup Timeline</h3><p>Readiness and resource usage for each system boot.</p></div>
      <button className="button secondary small" disabled={loading} onClick={() => setRefresh(value => value + 1)} type="button"><svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" aria-hidden="true"><path d="M20 7v5h-5M4 17v-5h5" /><path d="M6.1 6.1a8.3 8.3 0 0 1 13.7 4.4M4.2 13.5a8.3 8.3 0 0 0 13.7 4.4" /></svg>{loading ? "Refreshing…" : "Refresh"}</button>
    </div>
    {error && <div className="dashboard-alert is-error" role="alert">{error}{boots.length ? ". Showing the last loaded capture." : ""}</div>}
    {!boots.length ? <div className="empty-state">{loading ? "Loading startup captures…" : error ? "Retry with Refresh." : "No startup captures yet."}</div> : <>
      <div className="tl-boots">{boots.map((item, index) => <button className={item.id === selectedID ? "tl-boot is-active" : "tl-boot"}
        aria-pressed={item.id === selectedID} key={item.id} onClick={() => setSelectedID(item.id)} type="button">
        {index === 0 ? "Latest boot" : new Date(item.started_at).toLocaleString()}</button>)}</div>
      {boot && <div className="tl-wrap">
        <div className={`startup-overview is-${status}`}>
          <div className="startup-overview-main"><span className="startup-state-icon" aria-hidden="true"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">{status === "ready" ? <path d="m6 12 4 4 8-8" /> : status === "capturing" ? <><circle cx="12" cy="12" r="7" /><path d="M12 8v4l2 2" /></> : <><circle cx="12" cy="12" r="8" /><path d="M12 7v6m0 3h.01" /></>}</svg></span>
            <div><p className="logs-capture-state" role="status">{STATUS_LABELS[status]}</p><p className="startup-capture-meta"><time dateTime={boot.started_at}>{new Date(boot.started_at).toLocaleString()}</time><span aria-hidden="true"> · </span>{observations} observations</p></div>
          </div>
          {boot.finished_seconds !== undefined && <div className="startup-duration"><strong>{boot.finished_seconds}<span>s</span></strong><span>Capture duration</span></div>}
        </div>
        <ol className="tl" aria-label="Startup readiness observations">{items.map((item, index) => <li key={`${item.offset}-${item.label}-${index}`} className={`tl-item ${item.tone}${item.offset === null ? " is-pending" : ""}`}>
          <span className="tl-dot" aria-hidden="true" /><span className="tl-time">{item.offset === null ? "—" : `+${item.offset}s`}</span>
          <span className="tl-body"><b>{item.label}</b><small>{item.detail}</small></span>
        </li>)}</ol>
        <p className="startup-observation-note">First readiness observations. Disabled services are not required.</p>
        {detailError && <p role="alert" className="logs-retention">{detailError}. Refresh to retry.</p>}
        <StartupResourceChart key={selectedID} samples={samples} loading={!selectedDetail && !detailError && !samples.length} />
        <div className="tl-summary">
          <div className="startup-metric"><span>Peak CPU</span><strong>{peakCPU === null ? "—" : <>{peakCPU.toFixed(1)}<small>%</small></>}</strong><span>During this capture</span></div>
          <div className="startup-metric"><span>Captured memory</span><strong>{lastSample && lastSample.memory_total_mb > 0 ? <>{lastSample.memory_used_mb.toFixed(0)}<small> / {lastSample.memory_total_mb.toFixed(0)} MB</small></> : "—"}</strong><span>{status === "capturing" ? "Latest captured sample" : "Last captured sample"}</span></div>
          <div className="startup-metric"><span>Disk now</span><strong>{storage?.available ? <>{storage.usage_percent.toFixed(1)}<small>%</small></> : "—"}</strong><span>{storage?.available ? "Current device storage" : "Telemetry unavailable"}</span></div>
        </div>
      </div>}
    </>}
  </article>;
}
