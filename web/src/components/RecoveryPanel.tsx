import { useCallback, useEffect, useRef, useState } from "react";
import type { RouterConfig, Snapshot } from "../api-types";
import { apiFetch, responseError } from "../lib/api";
import { operationActive, operationLabel, recoveryDate, type RecoveryStatus } from "../lib/recovery";
import { useRecoveryActions } from "../lib/useRecoveryActions";
import { useVisiblePolling } from "../lib/useVisiblePolling";
import RecoveryToolsPanel from "./RecoveryToolsPanel";
import { RecoveryProgress, RecoveryReview } from "./RecoveryProgress";

export default function RecoveryPanel({ config }: { config: RouterConfig }) {
  const [status, setStatus] = useState<RecoveryStatus | null>(null);
  const [statusError, setStatusError] = useState("");
  const [snapshots, setSnapshots] = useState<Snapshot[] | null>(null);
  const [snapshotError, setSnapshotError] = useState("");
  const [actionError, setActionError] = useState("");
  const [observedOperation, setObservedOperation] = useState<string | null>(null);
  const [refreshKey, setRefreshKey] = useState(0);
  const refresh = useCallback(() => setRefreshKey(key => key + 1), []);
  const actions = useRecoveryActions(refresh, setActionError);
  const reviewRef = useRef<HTMLElement>(null);

  const loadStatus = useCallback(async (signal: AbortSignal) => {
    try {
      const response = await apiFetch("/api/v1/recovery/status", { signal, cache: "no-store" });
      if (!response.ok) throw new Error(await responseError(response, "Recovery status unavailable"));
      const body = await response.json() as RecoveryStatus;
      if (!Number.isSafeInteger(body.revision) || !body.generated_at || !body.retention) throw new Error("Recovery status unavailable");
      if (!signal.aborted) { setStatus(body); setStatusError(""); }
    } catch (error) { if (!signal.aborted) setStatusError((error as Error).message); }
  }, []);
  const loadSnapshots = useCallback(async (signal: AbortSignal) => {
    try {
      const response = await apiFetch("/api/v1/snapshots", { signal, cache: "no-store" });
      if (!response.ok) throw new Error(await responseError(response, "Snapshot history unavailable"));
      const body = await response.json();
      const items = Array.isArray(body) ? body : body.snapshots;
      if (!Array.isArray(items) || items.some(item => typeof item.id !== "string" || typeof item.checksum !== "string")) throw new Error("Snapshot history unavailable");
      if (!signal.aborted) { setSnapshots(items); setSnapshotError(""); }
    } catch (error) { if (!signal.aborted) setSnapshotError((error as Error).message); }
  }, []);

  // This component only mounts on Recovery. Network-confirmation monitoring
  // stays in DashboardApp and continues on every route.
  useVisiblePolling(loadStatus, 5_000, true, String(refreshKey));
  useVisiblePolling(loadSnapshots, 30_000, true, String(refreshKey));
  useEffect(() => {
    window.addEventListener("minimalrouter:config-applied", refresh);
    return () => window.removeEventListener("minimalrouter:config-applied", refresh);
  }, [refresh]);
  useEffect(() => {
    if (actions.preview) { reviewRef.current?.focus({ preventScroll: true }); reviewRef.current?.scrollIntoView({ block: "nearest", behavior: "smooth" }); }
  }, [actions.preview]);
  useEffect(() => {
    if (status?.operation && !["completed", "dismissed"].includes(status.operation.state)) setObservedOperation(status.operation.id);
  }, [status?.operation]);

  const fresh = !!status && !statusError;
  const pending = status?.pending;
  const operation = status?.operation;
  const dnsRemaining = !!operation?.dns_policy && operationActive(operation);
  const blocked = !fresh || !!pending || operationActive(operation);
  const snapshotCount = snapshots?.length ?? status?.snapshot_count;
  const disabled = actions.busy !== "";

  return <section className="dashboard-section recovery-page" id="recovery">
    <header className="recovery-heading">
      <div><p className="eyebrow">Keep a way back</p><h2>Recovery</h2><p>Save your configuration. Review every change. Recover with confidence.</p></div>
      <span className="recovery-revision"><span aria-hidden="true">↺</span> Configuration <b>r{config.revision}</b></span>
    </header>

    <section className="recovery-overview" aria-label="Recovery overview">
      <div><span className="recovery-metric-icon" aria-hidden="true">↗</span><span className="recovery-metric-label">Last backup export</span><strong>{status ? recoveryDate(status.last_backup_export_at) : statusError ? "Unavailable" : "Loading…"}</strong><small>{statusError ? "Status unavailable · last known value" : "Export recorded; keep the file off this appliance"}</small></div>
      <div><span className="recovery-metric-icon" aria-hidden="true">▤</span><span className="recovery-metric-label">Local snapshots</span><strong>{snapshotCount === undefined ? "Loading…" : `${snapshotCount} available`}</strong><small>{snapshotError ? "History unavailable · last known count" : "20 manual + 20 automatic retained"}</small></div>
      <div data-attention={!!pending}><span className="recovery-metric-icon" aria-hidden="true">⇄</span><span className="recovery-metric-label">Network confirmation</span><strong>{!status ? statusError ? "Unavailable" : "Loading…" : pending ? "Action required" : "No pending change"}</strong><small>{pending?.confirmation_deadline ? `Deadline ${recoveryDate(pending.confirmation_deadline)}` : "Critical changes roll back unless confirmed"}</small></div>
      <div data-attention={dnsRemaining}><span className="recovery-metric-icon" aria-hidden="true">◇</span><span className="recovery-metric-label">DNS restoration</span><strong>{!status ? statusError ? "Unavailable" : "Loading…" : dnsRemaining ? operationLabel(operation?.state) : "No unfinished restore"}</strong><small>{dnsRemaining ? "Progress is saved on the appliance" : "Encrypted backups can include the DNS policy"}</small></div>
    </section>

    {statusError && <div className="recovery-alert" role="alert"><div><strong>Recovery status unavailable</strong><p>{statusError}. Displayed information may be stale. Restore actions are paused until status returns.</p></div><button className="button secondary small" type="button" onClick={refresh}>Retry status</button></div>}
    {actionError && <div className="recovery-alert" role="alert">{actionError}</div>}
    {actions.notice && <div className="recovery-notice" role="status">{actions.notice}</div>}
    {(actions.flowStarted || (operation && (observedOperation === operation.id || !["completed", "dismissed"].includes(operation.state)))) && <RecoveryProgress status={status} fresh={fresh} preview={actions.preview} actions={actions} />}
    {actions.preview && <section ref={reviewRef} tabIndex={-1} className="recovery-review card" aria-label="Review restore changes"><RecoveryReview preview={actions.preview} revision={status?.revision ?? config.revision} blocked={blocked} actions={actions} /></section>}

    <RecoveryToolsPanel config={config} actions={actions} />

    <article className="recovery-snapshots card" aria-labelledby="recovery-snapshots-title">
      <div className="recovery-card-head"><div><p className="eyebrow">A local safety net</p><h3 id="recovery-snapshots-title">Configuration snapshots</h3><p>Quick restore points on this disk. Router configuration only; export a backup to include DNS policy and protect against disk failure.</p></div><button className="button secondary small" type="button" onClick={refresh}>Refresh snapshots</button></div>
      <form className="recovery-snapshot-create" onSubmit={actions.createSnapshot}>
        <label className="field"><span>Snapshot name <small>(optional)</small></span><input name="label" maxLength={80} placeholder="e.g. Before changing the firewall" /></label>
        <button className="button primary" disabled={disabled || !fresh || !!pending} type="submit">{actions.busy === "snapshot-create" ? "Saving…" : "Create snapshot"}</button>
      </form>
      <div className="recovery-retention"><span>Manual <b>{snapshots?.filter(item => item.kind === "manual").length ?? "—"} / 20</b></span><span>Automatic <b>{snapshots?.filter(item => item.kind === "automatic").length ?? "—"} / 20</b></span><small>Oldest snapshots of each kind are removed first.</small></div>
      {snapshotError && <div className="recovery-alert" role="alert"><div><strong>Snapshot history unavailable</strong><p>{snapshotError}{snapshots ? " · Showing the last loaded list." : " · Retry to load your restore points."}</p></div><button className="button secondary small" onClick={refresh} type="button">Retry snapshots</button></div>}
      {!snapshots && !snapshotError && <p className="recovery-empty" role="status">Loading snapshots…</p>}
      {snapshots?.length === 0 && !snapshotError && <div className="recovery-empty"><strong>No snapshots yet.</strong><p>Create a named restore point before your next change.</p></div>}
      {!!snapshots?.length && <ul className="recovery-snapshot-list">{snapshots.map(snapshot => <li key={snapshot.id}>
        <span className={`recovery-kind ${snapshot.kind === "manual" ? "is-manual" : ""}`}>{snapshot.kind === "manual" ? "Manual" : snapshot.kind === "automatic" ? "Automatic" : "Unknown kind"}</span>
        <div className="recovery-snapshot-info"><strong>{snapshot.label || (snapshot.kind === "automatic" ? "Before configuration change" : "Configuration snapshot")}</strong><span>{recoveryDate(snapshot.created_at)} <span aria-hidden="true">·</span> Revision {snapshot.revision}</span><code title={snapshot.checksum}>SHA-256 {snapshot.checksum.slice(0, 16)}…</code></div>
        <div className="recovery-snapshot-actions"><button className="button secondary small" disabled={disabled || !!snapshotError} type="button" onClick={() => void actions.previewSnapshot(snapshot.id)}>Preview restore</button><button className="button secondary small danger" disabled={disabled || !!snapshotError} type="button" onClick={() => void actions.deleteSnapshot(snapshot)}>Delete</button></div>
      </li>)}</ul>}
    </article>
    <aside className="recovery-console"><span className="recovery-console-icon" aria-hidden="true">⌘</span><div><strong>Lost access to the dashboard?</strong><p>Use <code>router-recovery</code> on the local console to reset password/TOTP, repair LAN access or perform a factory reset. Console access is required.</p></div></aside>
  </section>;
}
