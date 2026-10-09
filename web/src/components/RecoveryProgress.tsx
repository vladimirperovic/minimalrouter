import { useEffect, useState } from "react";
import { operationLabel, recoveryDate, type RecoveryPreview, type RecoveryStatus } from "../lib/recovery";
import type { RecoveryActions } from "../lib/useRecoveryActions";

const steps = ["Validate", "Review changes", "Apply", "Confirm access", "Restore DNS", "Result"];

export function RecoveryProgress({ status, fresh, preview, actions }: { status: RecoveryStatus | null; fresh: boolean; preview: RecoveryPreview | null; actions: RecoveryActions }) {
  const op = status?.operation;
  const state = op?.state;
  const activeStep = actions.busy.endsWith("preview") ? 0 : actions.busy === "apply" ? 2 : preview ? 1 : !op ? 0 : state === "applying" ? 2 : state === "awaiting_confirmation" ? 3 : state?.startsWith("dns_") ? 4 : 5;
  const failed = ["failed", "rolled_back", "needs_review", "dns_failed"].includes(state ?? "");
  const canDNS = fresh && !status?.pending && (state === "dns_pending" || state === "dns_failed");
  return <section className={`recovery-progress ${failed ? "has-error" : ""}`} aria-label="Restore progress">
    <div className="recovery-progress-heading"><strong>Restore, step by step</strong><span>{actions.busy.endsWith("preview") ? "Validating the restore candidate…" : actions.busy === "apply" ? "Applying configuration…" : preview ? "Review before applying" : operationLabel(state)}</span></div>
    <ol>{steps.map((label, index) => {
      const done = index < activeStep && (preview || !["failed", "rolled_back", "needs_review", "dismissed"].includes(state ?? ""));
      const skipped = !preview && op && ((index === 4 && !op.dns_policy) || (index === 3 && activeStep > 3));
      return <li key={label} aria-current={index === activeStep ? "step" : undefined} className={`${done ? "is-done" : ""} ${index === activeStep ? "is-current" : ""}`}><span>{done ? "✓" : String(index + 1).padStart(2, "0")}</span>{label}{skipped && <small>{index === 4 ? "Not included" : "Cleared / not required"}</small>}</li>;
    })}</ol>
    {op && !preview && !actions.busy.endsWith("preview") && actions.busy !== "apply" && <div className="recovery-operation" aria-live="polite">
      <div><strong>{operationLabel(state)}</strong><p>{state === "awaiting_confirmation" ? `Use the dashboard confirmation banner to verify the new management path before ${recoveryDate(op.confirmation_deadline)}. DNS restoration remains locked until access is confirmed.`
        : state === "dns_pending" ? `Router configuration is active. Restore ${Object.values(op.dns_policy?.categories ?? {}).filter(Boolean).length} DNS categories and ${(op.dns_policy?.exceptions ?? []).length} exceptions when ready. Lists will be downloaded again as needed.`
        : state === "dns_running" ? "DNS restoration was accepted. Waiting for the service to verify the policy and report a healthy result. You can leave this page and return."
        : state === "completed" ? "The restored configuration is active. All included restore steps have completed."
        : state === "dismissed" ? "The DNS continuation was dismissed. The current DNS policy was kept."
        : state === "applying" ? "The appliance is processing this restore. Check the result before starting another change." : op.error || "Inspect the router state before validating another restore."}</p>
        {op.error && ["dns_pending", "dns_running"].includes(state ?? "") && <p className="recovery-inline-error">{op.error}</p>}
        <small>{op.source === "backup" ? "Encrypted backup" : op.source === "snapshot" ? "Snapshot" : "pfSense migration"} · Started {recoveryDate(op.started_at)}</small>
      </div>
      {(!!op.dns_policy && ["awaiting_confirmation", "dns_pending", "dns_running", "dns_failed"].includes(state ?? "")) && <div className="recovery-operation-actions"><button className="button primary" disabled={!canDNS || actions.busy !== ""} type="button" onClick={() => void actions.continueDNS(op.id)}>{state === "dns_running" ? "Restoring DNS…" : state === "dns_failed" ? "Retry DNS restore" : "Restore DNS protection"}</button>{canDNS && <button className="button secondary small" disabled={actions.busy !== ""} type="button" onClick={() => void actions.dismiss(op.id)}>Keep current DNS policy</button>}</div>}
    </div>}
  </section>;
}

export function RecoveryReview({ preview, revision, blocked, actions }: { preview: RecoveryPreview; revision: number; blocked: boolean; actions: RecoveryActions }) {
  const [now, setNow] = useState(Date.now);
  useEffect(() => { const timer = window.setInterval(() => setNow(Date.now()), 1000); return () => window.clearInterval(timer); }, []);
  const { assessment } = preview;
  const seconds = preview.expires_at ? Math.max(0, Math.floor((Date.parse(preview.expires_at) - now) / 1000)) : null;
  const stale = preview.base_revision !== revision;
  const expired = seconds === 0;
  const source = preview.source;
  const title = source === "backup" ? "Validated restore candidate" : source === "snapshot" ? "Snapshot restore preview" : "pfSense migration preview";
  return <>
    <div className="recovery-card-head"><div><p className="eyebrow">Step 02 · Review changes</p><h3>{title}</h3></div><span className={`recovery-kind ${assessment.can_apply ? "is-manual" : ""}`}>{assessment.can_apply ? `${assessment.risk} risk` : "Restore blocked"}</span></div>
    <dl className="recovery-candidate"><div><dt>Hostname</dt><dd>{preview.candidate.system.hostname}</dd></div><div><dt>LAN</dt><dd>{preview.candidate.lan.ip_address} · {preview.candidate.lan.interface}</dd></div><div><dt>WAN</dt><dd>{preview.candidate.wan.interface}</dd></div></dl>
    <div className="recovery-review-details"><div><strong>Changed sections</strong>{assessment.changes.length ? <ul>{assessment.changes.map(change => <li key={change}>{change}</li>)}</ul> : <p>{assessment.can_apply ? "No effective router configuration changes." : "Resolve the blockers below to assess this transition."}</p>}</div><div><strong>{assessment.requires_confirmation ? `Access confirmation · ${assessment.rollback_seconds}s` : "No access confirmation required"}</strong><p>{assessment.expected_interruption}</p><p>{preview.dns_filter ? "DNS protection is restored in a separate step, after network confirmation." : "This restore does not include a DNS category policy. The current policy is kept."}</p></div></div>
    {preview.report && <div className="recovery-migration-report"><p>Imported: {Object.entries(preview.report.imported ?? {}).map(([key, count]) => `${key.replaceAll("_", " ")}: ${count}`).join(" · ") || "No countable sections"}</p>{(preview.report.warnings ?? []).length > 0 && <><strong>Warnings</strong><ul>{preview.report.warnings!.map(item => <li key={item}>{item}</li>)}</ul></>}{(preview.report.unsupported_sections ?? []).length > 0 && <><strong>Unsupported sections</strong><ul>{preview.report.unsupported_sections!.map(item => <li key={item}>{item}</li>)}</ul></>}<p>Imported WAN NAT rules remain disabled. Remote services are accessible through WireGuard.</p></div>}
    {!!assessment.blockers.length && <div className="recovery-alert" role="alert"><div><strong>Resolve before restoring</strong><ul>{assessment.blockers.map(item => <li key={item}>{item}</li>)}</ul></div></div>}
    {(stale || expired) && <p className="recovery-inline-error" role="alert">{stale ? "Configuration changed after preview. Validate again before applying." : "This preview has expired. Validate the file again."}</p>}
    {blocked && <p className="recovery-inline-error">Wait for current status and finish any pending network or DNS restore before applying another configuration.</p>}
    <div className="recovery-review-footer"><span>{seconds === null ? `Reviewed against revision ${preview.base_revision}` : `Preview ${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, "0")} remaining · revision ${preview.base_revision}`}</span><div><button className="button secondary" type="button" disabled={actions.busy !== ""} onClick={actions.clearPreview}>Discard preview</button><button className="button danger" disabled={blocked || stale || expired || !assessment.can_apply || actions.busy !== ""} type="button" onClick={() => void actions.applyPreview()}>{source === "backup" ? "Apply validated backup" : source === "snapshot" ? "Restore snapshot" : "Apply pfSense migration"}</button></div></div>
  </>;
}
