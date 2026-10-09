import type { DNSFilterPolicy, PendingTransaction, RouterConfig } from "../api-types";

export type RecoveryOperation = {
  id: string;
  source: string;
  state: string;
  transaction_id: string;
  target_revision: number;
  started_at: string;
  confirmation_deadline?: string;
  dns_policy?: DNSFilterPolicy | null;
  error?: string;
};

export type RecoveryStatus = {
  generated_at: string;
  revision: number;
  last_backup_export_at: string | null;
  snapshot_count: number;
  retention: { manual: number; automatic: number };
  pending: PendingTransaction | null;
  operation: RecoveryOperation | null;
};

export type RecoveryPreview = {
  source: "backup" | "pfsense" | "snapshot";
  import_id?: string;
  snapshot_id?: string;
  base_revision: number;
  expires_at?: string;
  expires_in_seconds?: number;
  candidate: RouterConfig;
  dns_filter?: DNSFilterPolicy | null;
  assessment: {
    can_apply: boolean;
    blockers: string[];
    changes: string[];
    risk: string;
    requires_confirmation: boolean;
    rollback_seconds?: number;
    expected_interruption: string;
  };
  report?: {
    source_version?: string;
    warnings?: string[];
    unsupported_sections?: string[];
    imported?: Record<string, number>;
  };
};

export function operationActive(op?: RecoveryOperation | null) {
  return !!op && !["completed", "failed", "rolled_back", "needs_review", "dismissed"].includes(op.state);
}

export function operationLabel(state?: string) {
  const labels: Record<string, string> = { applying: "Applying configuration", awaiting_confirmation: "Confirm network access", dns_pending: "DNS restore remaining", dns_running: "Restoring DNS protection", dns_failed: "DNS restore needs attention", completed: "Recovery completed", failed: "Restore failed", rolled_back: "Network change rolled back", needs_review: "Restore outcome needs review", dismissed: "DNS continuation dismissed" };
  return state ? labels[state] ?? "Unknown restore state" : "No restore in progress";
}

export function recoveryDate(value?: string | null) {
  if (!value) return "No export recorded";
  const date = new Date(value);
  return Number.isFinite(date.getTime()) ? date.toLocaleString(undefined, { month: "short", day: "numeric", year: "numeric", hour: "2-digit", minute: "2-digit" }) : "Date unavailable";
}

export function checkedPreview(value: RecoveryPreview, source: RecoveryPreview["source"]) {
  if (!value.candidate?.system || !value.candidate?.lan || !value.candidate?.wan || !Number.isSafeInteger(value.base_revision)
    || typeof value.assessment?.can_apply !== "boolean" || !Array.isArray(value.assessment.changes) || !Array.isArray(value.assessment.blockers)
    || (source === "snapshot" ? !value.snapshot_id : !value.import_id || !Number.isFinite(Date.parse(value.expires_at ?? "")))) {
    throw new Error("Recovery preview is incomplete. Refresh and validate again.");
  }
  return { ...value, source };
}

export function downloadRecoveryFile(blob: Blob, filename: string) {
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement("a");
  anchor.href = url; anchor.download = filename;
  document.body.appendChild(anchor); anchor.click(); anchor.remove();
  window.setTimeout(() => URL.revokeObjectURL(url), 1000);
}
