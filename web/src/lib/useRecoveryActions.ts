import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import type { PendingTransaction, Snapshot } from "../api-types";
import { apiFetch, responseError } from "./api";
import { publishAppliedTransaction } from "./configuration";
import { checkedPreview, downloadRecoveryFile, type RecoveryPreview } from "./recovery";

export function useRecoveryActions(refresh: () => void, onError: (message: string) => void) {
  const [busy, setBusy] = useState("");
  const [notice, setNotice] = useState("");
  const [preview, setPreview] = useState<RecoveryPreview | null>(null);
  const [flowStarted, setFlowStarted] = useState(false);
  const request = useRef<AbortController | null>(null);
  const mounted = useRef(true);
  const writing = useRef(false);

  const clearPreview = useCallback(() => {
    request.current?.abort(); request.current = null;
    setPreview(null);
    setFlowStarted(false);
    setBusy(value => value.endsWith("preview") || value === "diagnostics" ? "" : value);
  }, []);

  useEffect(() => {
    mounted.current = true;
    const hide = () => { if (document.hidden) { request.current?.abort(); request.current = null; setBusy(value => value.endsWith("preview") || value === "diagnostics" ? "" : value); } };
    document.addEventListener("visibilitychange", hide);
    return () => { mounted.current = false; request.current?.abort(); request.current = null; document.removeEventListener("visibilitychange", hide); };
  }, []);

  // A cancelled fetch may still resolve (including caches and test transports).
  // Check ownership after every await before publishing or downloading anything.
  async function read(kind: string, path: string, init: RequestInit, source?: RecoveryPreview["source"]) {
    if (writing.current) return;
    request.current?.abort();
    const controller = new AbortController(); request.current = controller;
    const current = () => mounted.current && request.current === controller && !controller.signal.aborted;
    setBusy(kind); setNotice(""); onError("");
    if (source) { setPreview(null); setFlowStarted(true); }
    try {
      const response = await apiFetch(path, { ...init, cache: "no-store", signal: controller.signal });
      if (!response.ok) throw new Error(await responseError(response, "Recovery request failed"));
      if (source) {
        const body = await response.json() as RecoveryPreview;
        if (current()) setPreview(checkedPreview(body, source));
      } else {
        const blob = await response.blob();
        if (current()) { downloadRecoveryFile(blob, "minimalrouter-diagnostics.json"); setNotice("Diagnostic report downloaded. Review its private network details before sharing."); }
      }
    } catch (error) { if (current()) { if (source) setFlowStarted(false); onError(error instanceof Error ? error.message : "Recovery request failed"); } }
    finally { if (current()) { request.current = null; setBusy(""); } }
  }

  async function write(kind: string, task: () => Promise<void>) {
    if (writing.current) return;
    writing.current = true; request.current?.abort(); request.current = null;
    setBusy(kind); setNotice(""); onError("");
    try { await task(); }
    catch (error) { if (mounted.current) onError(error instanceof Error ? error.message : "Recovery operation failed"); }
    finally { writing.current = false; if (mounted.current) { setBusy(""); refresh(); } }
  }

  async function post(path: string, body?: unknown) {
    const response = await apiFetch(path, { method: "POST", ...(body === undefined ? {} : { headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) }) });
    if (!response.ok) throw new Error(await responseError(response, "Recovery operation failed"));
    return response;
  }

  function selectedFile(form: FormData, key: string, maxMiB: number): File {
    const file = form.get(key);
    if (!(file instanceof File) || file.size === 0) throw new Error("Choose a backup file first.");
    if (file.size > maxMiB * 1024 * 1024) throw new Error(`File exceeds the ${maxMiB} MiB safety limit.`);
    return file;
  }

  async function previewBackup(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const element = event.currentTarget;
    const form = new FormData(element);
    try {
      const upload = new FormData();
      upload.set("backup", selectedFile(form, "backup", 16));
      upload.set("current_password", String(form.get("restore_current_password") ?? ""));
      upload.set("backup_passphrase", String(form.get("restore_backup_passphrase") ?? ""));
      // Erase secrets immediately; an older request must never clear a newer input.
      element.querySelectorAll<HTMLInputElement>('input[type="password"]').forEach(input => { input.value = ""; });
      await read("backup-preview", "/api/v1/backup/import/preview", { method: "POST", body: upload }, "backup");
    } catch (error) { clearPreview(); onError((error as Error).message); }
  }

  async function previewPfSense(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    try {
      const file = selectedFile(form, "pfsense_xml", 8);
      const wan = String(form.get("target_wan") ?? "").trim(), lan = String(form.get("target_lan") ?? "").trim();
      if (!wan || !lan || wan === lan) throw new Error("Choose distinct target WAN and LAN interfaces.");
      // Passing the File directly keeps the complete operation abortable.
      await read("pfsense-preview", `/api/v1/import/pfsense/preview?${new URLSearchParams({ wan, lan })}`, { method: "POST", headers: { "Content-Type": "application/xml" }, body: file }, "pfsense");
    } catch (error) { clearPreview(); onError((error as Error).message); }
  }

  async function exportBackup(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const element = event.currentTarget, form = new FormData(element);
    const password = String(form.get("current_password") ?? "");
    element.reset();
    await write("backup-export", async () => {
      const response = await post("/api/v1/backup/export", { current_password: password });
      const blob = await response.blob();
      if (mounted.current) { downloadRecoveryFile(blob, "minimalrouter-backup.mrbak"); setNotice("Encrypted backup downloaded. Keep this file and the password used to create it somewhere safe."); }
    });
  }

  async function applyPreview() {
    if (!preview || !preview.assessment.can_apply || (preview.expires_at && Date.parse(preview.expires_at) <= Date.now())) return;
    if (!window.confirm("Apply the reviewed configuration? Confirm any network change before the rollback deadline. DNS restoration is a separate step.")) return;
    const chosen = preview;
    setPreview(null); // Import tokens are single-use, including rejected applies.
    await write("apply", async () => {
      const response = chosen.source === "snapshot"
        ? await post(`/api/v1/snapshots/${encodeURIComponent(chosen.snapshot_id!)}/restore`, { expected_revision: chosen.base_revision })
        : await post(`/api/v1/import/${chosen.source}/${encodeURIComponent(chosen.import_id!)}/apply`);
      const transaction = await response.json() as PendingTransaction;
      if (!transaction.id || !["Committed", "AwaitingConfirmation"].includes(transaction.state)) throw new Error("Restore response is incomplete. Check Recovery status before retrying.");
      const error = await publishAppliedTransaction(transaction);
      if (mounted.current) {
        if (error) onError(error);
        setNotice(transaction.state === "AwaitingConfirmation" ? "Configuration is provisional. Confirm access using the dashboard banner before the deadline." : "Configuration restored. Check the recovery progress for any remaining DNS step.");
      }
    });
  }

  async function createSnapshot(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); const element = event.currentTarget, label = String(new FormData(element).get("label") ?? "").trim();
    await write("snapshot-create", async () => { await post("/api/v1/snapshots", { label }); if (mounted.current) { element.reset(); setNotice("Configuration snapshot created."); } });
  }

  async function deleteSnapshot(snapshot: Snapshot) {
    if (!window.confirm(`Delete snapshot “${snapshot.label || snapshot.id}”? This restore point cannot be recovered.`)) return;
    if (preview?.snapshot_id === snapshot.id) setPreview(null);
    await write("snapshot-delete", async () => {
      const response = await apiFetch(`/api/v1/snapshots/${encodeURIComponent(snapshot.id)}`, { method: "DELETE" });
      if (!response.ok) throw new Error(await responseError(response, "Snapshot deletion failed"));
      if (mounted.current) setNotice("Snapshot deleted.");
    });
  }

  async function continueDNS(id: string) {
    await write("dns", async () => { await post(`/api/v1/recovery/operations/${encodeURIComponent(id)}/dns`); if (mounted.current) setNotice("DNS policy restoration requested. Waiting for the verified result…"); });
  }

  async function dismiss(id: string) {
    if (!window.confirm("Dismiss the remaining DNS restoration? The current DNS policy will remain in place.")) return;
    await write("dismiss", async () => { await post(`/api/v1/recovery/operations/${encodeURIComponent(id)}/dismiss`); });
  }

  return { busy, notice, preview, flowStarted, clearPreview, previewBackup, previewPfSense, exportBackup, applyPreview, createSnapshot, deleteSnapshot, continueDNS, dismiss,
    previewSnapshot: (id: string) => read("snapshot-preview", `/api/v1/snapshots/${encodeURIComponent(id)}/preview`, {}, "snapshot"),
    downloadDiagnostics: () => read("diagnostics", "/api/v1/system/diagnostics", {}),
  };
}

export type RecoveryActions = ReturnType<typeof useRecoveryActions>;
