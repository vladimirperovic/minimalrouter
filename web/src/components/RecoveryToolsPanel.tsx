import { FormEvent, useState } from "react";
import type { RouterConfig } from "../api-types";
import { apiFetch } from "../lib/api";

type Props = {
  config: RouterConfig;
  onError: (message: string) => void;
};

type PfSenseReport = {
  source_version?: string;
  warnings?: string[];
  unsupported_sections?: string[];
  imported?: Record<string, number>;
  config?: RouterConfig;
};

type PfSensePreview = {
  import_id: string;
  expires_in_seconds: number;
  report: PfSenseReport;
};

type BackupPreview = {
  import_id: string;
  expires_in_seconds: number;
  candidate: RouterConfig;
};

async function responseError(response: Response, fallback: string): Promise<string> {
  const clone = response.clone();
  try {
    const body = await response.json() as { error?: string };
    if (body?.error) return body.error;
  } catch {
    // Some hardened endpoints intentionally return plain text errors.
  }
  try {
    const text = (await clone.text()).trim();
    if (text) return text;
  } catch {
    // Fall through.
  }
  return fallback;
}

function downloadBlob(blob: Blob, filename: string) {
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = filename;
  document.body.appendChild(anchor);
  anchor.click();
  anchor.remove();
  window.setTimeout(() => URL.revokeObjectURL(url), 1000);
}

export default function RecoveryToolsPanel({ config, onError }: Props) {
  const [busy, setBusy] = useState("");
  const [notice, setNotice] = useState("");
  const [pfPreview, setPfPreview] = useState<PfSensePreview | null>(null);
  const [backupPreview, setBackupPreview] = useState<BackupPreview | null>(null);

  const clearMessages = () => {
    setNotice("");
    onError("");
  };

  const downloadDiagnostics = async () => {
    setBusy("diagnostics");
    clearMessages();
    try {
      const response = await apiFetch("/api/v1/system/diagnostics");
      if (!response.ok) throw new Error(await responseError(response, `Diagnostics failed (${response.status})`));
      downloadBlob(await response.blob(), "minimalrouter-diagnostics.json");
      setNotice("Redacted diagnostic bundle downloaded.");
    } catch (error) {
      onError(error instanceof Error ? error.message : "Diagnostics failed");
    } finally {
      setBusy("");
    }
  };

  const exportBackup = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const formElement = event.currentTarget;
    const form = new FormData(formElement);
    const currentPassword = String(form.get("current_password") ?? "");
    const passphrase = String(form.get("backup_passphrase") ?? "");
    const confirmPassphrase = String(form.get("confirm_backup_passphrase") ?? "");
    if (passphrase.length < 15) {
      onError("Backup passphrase must contain at least 15 characters.");
      return;
    }
    if (passphrase !== confirmPassphrase) {
      onError("Backup passphrases do not match.");
      return;
    }
    setBusy("backup-export");
    clearMessages();
    try {
      const response = await apiFetch("/api/v1/backup/export", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ current_password: currentPassword, backup_passphrase: passphrase }),
      });
      if (!response.ok) throw new Error(await responseError(response, `Backup export failed (${response.status})`));
      downloadBlob(await response.blob(), "minimalrouter-backup.mrbak");
      setNotice("Encrypted backup downloaded. Keep the passphrase separately; it is never stored by the router.");
      formElement.reset();
    } catch (error) {
      onError(error instanceof Error ? error.message : "Backup export failed");
    } finally {
      setBusy("");
    }
  };

  const previewBackup = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    const file = form.get("backup");
    if (!(file instanceof File) || file.size === 0) {
      onError("Choose a .mrbak backup file first.");
      return;
    }
    if (file.size > 16 * 1024 * 1024) {
      onError("Encrypted backup is larger than the 16 MiB safety limit.");
      return;
    }
    const currentPassword = String(form.get("restore_current_password") ?? "");
    const passphrase = String(form.get("restore_backup_passphrase") ?? "");
    const upload = new FormData();
    upload.set("current_password", currentPassword);
    upload.set("backup_passphrase", passphrase);
    upload.set("backup", file);

    setBusy("backup-preview");
    clearMessages();
    setBackupPreview(null);
    try {
      const response = await apiFetch("/api/v1/backup/import/preview", {
        method: "POST",
        body: upload,
      });
      if (!response.ok) throw new Error(await responseError(response, `Backup validation failed (${response.status})`));
      const body = await response.json() as BackupPreview;
      if (!body.import_id || !body.candidate) throw new Error("Backup preview response was incomplete");
      setBackupPreview(body);
      setNotice("Backup authenticated and validated. Review the candidate below before applying it.");
    } catch (error) {
      onError(error instanceof Error ? error.message : "Backup validation failed");
    } finally {
      setBusy("");
    }
  };

  const applyBackup = async () => {
    if (!backupPreview?.import_id) return;
    if (!window.confirm("Apply this encrypted backup? Connectivity-critical changes may require confirmation and will roll back automatically if not confirmed.")) return;
    setBusy("backup-apply");
    clearMessages();
    try {
      const response = await apiFetch(`/api/v1/import/backup/${encodeURIComponent(backupPreview.import_id)}/apply`, { method: "POST" });
      if (!response.ok) throw new Error(await responseError(response, `Backup restore failed (${response.status})`));
      const body = await response.json().catch(() => ({})) as { state?: string };
      setBackupPreview(null);
      setNotice(body.state === "AwaitingConfirmation"
        ? "Backup is provisionally active. Use the dashboard confirmation banner to prove the new management path."
        : "Backup restored successfully.");
    } catch (error) {
      onError(error instanceof Error ? error.message : "Backup restore failed");
    } finally {
      setBusy("");
    }
  };

  const previewPfSense = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    const file = form.get("pfsense_xml");
    if (!(file instanceof File) || file.size === 0) {
      onError("Choose a pfSense config.xml file first.");
      return;
    }
    if (file.size > 8 * 1024 * 1024) {
      onError("pfSense configuration is larger than the 8 MiB safety limit.");
      return;
    }
    const wan = String(form.get("target_wan") ?? "").trim();
    const lan = String(form.get("target_lan") ?? "").trim();
    if (!wan || !lan || wan === lan) {
      onError("Choose distinct target WAN and LAN interfaces.");
      return;
    }
    setBusy("pfsense-preview");
    clearMessages();
    setPfPreview(null);
    try {
      const xml = await file.text();
      const query = new URLSearchParams({ wan, lan });
      const response = await apiFetch(`/api/v1/import/pfsense/preview?${query.toString()}`, {
        method: "POST",
        headers: { "Content-Type": "application/xml" },
        body: xml,
      });
      if (!response.ok) throw new Error(await responseError(response, `pfSense preview failed (${response.status})`));
      const body = await response.json() as PfSensePreview;
      if (!body.import_id || !body.report) throw new Error("pfSense preview response was incomplete");
      setPfPreview(body);
      setNotice("pfSense configuration parsed. Nothing has been applied yet; review warnings and unsupported sections below.");
    } catch (error) {
      onError(error instanceof Error ? error.message : "pfSense preview failed");
    } finally {
      setBusy("");
    }
  };

  const applyPfSense = async () => {
    if (!pfPreview?.import_id) return;
    if (!window.confirm("Apply this pfSense migration preview? Imported NAT rules remain disabled because WireGuard is the only permitted remote entry path.")) return;
    setBusy("pfsense-apply");
    clearMessages();
    try {
      const response = await apiFetch(`/api/v1/import/pfsense/${encodeURIComponent(pfPreview.import_id)}/apply`, { method: "POST" });
      if (!response.ok) throw new Error(await responseError(response, `pfSense import failed (${response.status})`));
      const body = await response.json().catch(() => ({})) as { state?: string };
      setPfPreview(null);
      setNotice(body.state === "AwaitingConfirmation"
        ? "pfSense migration is provisionally active. Confirm the management path from the dashboard banner before the rollback deadline."
        : "pfSense migration applied successfully.");
    } catch (error) {
      onError(error instanceof Error ? error.message : "pfSense import failed");
    } finally {
      setBusy("");
    }
  };

  const importedEntries = Object.entries(pfPreview?.report.imported ?? {});
  const warnings = pfPreview?.report.warnings ?? [];
  const unsupported = pfPreview?.report.unsupported_sections ?? [];

  return (
    <article className="card security-control-card security-recovery-card" aria-labelledby="recovery-tools-title">
      <div className="card-title-row">
        <div>
          <h3 id="recovery-tools-title">Administration and recovery tools</h3>
          <p>Export encrypted backups, validate restores, migrate pfSense settings, and download a redacted diagnostic bundle.</p>
        </div>
        <button className="button secondary" disabled={busy !== ""} onClick={() => void downloadDiagnostics()} type="button">{busy === "diagnostics" ? "Building…" : "Download diagnostics"}</button>
      </div>

      {notice && <div className="dashboard-callout" role="status"><strong>Operation status</strong><p>{notice}</p></div>}

      <details open>
        <summary>Encrypted backup export</summary>
        <form className="settings-form" onSubmit={exportBackup}>
          <div className="form-grid two">
            <label className="field"><span>Current administrator password</span><input autoComplete="current-password" name="current_password" required type="password" /></label>
            <label className="field"><span>Backup passphrase</span><input autoComplete="new-password" minLength={15} name="backup_passphrase" required type="password" /></label>
            <label className="field form-span"><span>Confirm backup passphrase</span><input autoComplete="new-password" minLength={15} name="confirm_backup_passphrase" required type="password" /></label>
          </div>
          <p className="form-note">The backup is authenticated and encrypted with AES-GCM using an Argon2id-derived key. The passphrase is never stored on the router.</p>
          <div className="form-actions"><button className="button primary" disabled={busy !== ""} type="submit">{busy === "backup-export" ? "Encrypting…" : "Export encrypted backup"}</button></div>
        </form>
      </details>

      <details>
        <summary>Restore encrypted backup</summary>
        <form className="settings-form" onSubmit={previewBackup}>
          <div className="form-grid two">
            <label className="field form-span"><span>Backup file</span><input accept=".mrbak,application/json" name="backup" required type="file" /></label>
            <label className="field"><span>Current administrator password</span><input autoComplete="current-password" name="restore_current_password" required type="password" /></label>
            <label className="field"><span>Backup passphrase</span><input autoComplete="current-password" name="restore_backup_passphrase" required type="password" /></label>
          </div>
          <div className="form-actions"><button className="button secondary" disabled={busy !== ""} type="submit">{busy === "backup-preview" ? "Validating…" : "Validate backup"}</button></div>
        </form>
        {backupPreview && (
          <div className="dashboard-callout">
            <strong>Validated restore candidate</strong>
            <p>Hostname <code>{backupPreview.candidate.system.hostname}</code> · LAN <code>{backupPreview.candidate.lan.ip_address}</code> on <code>{backupPreview.candidate.lan.interface}</code> · WAN <code>{backupPreview.candidate.wan.interface}</code>.</p>
            <p>This preview expires in {Math.round(backupPreview.expires_in_seconds / 60)} minutes and keeps secrets server-side until apply.</p>
            <div className="form-actions"><button className="button danger" disabled={busy !== ""} onClick={() => void applyBackup()} type="button">{busy === "backup-apply" ? "Applying…" : "Apply validated backup"}</button></div>
          </div>
        )}
      </details>

      <details>
        <summary>Migrate from pfSense config.xml</summary>
        <form className="settings-form" onSubmit={previewPfSense}>
          <div className="form-grid two">
            <label className="field form-span"><span>pfSense config.xml</span><input accept=".xml,application/xml,text/xml" name="pfsense_xml" required type="file" /></label>
            <label className="field"><span>Target WAN interface</span><input defaultValue={config.wan.interface} name="target_wan" required /></label>
            <label className="field"><span>Target LAN interface</span><input defaultValue={config.lan.interface} name="target_lan" required /></label>
          </div>
          <p className="form-note">FreeBSD interface names are never trusted. You must map the imported configuration onto explicit Linux WAN/LAN interfaces before previewing it.</p>
          <div className="form-actions"><button className="button secondary" disabled={busy !== ""} type="submit">{busy === "pfsense-preview" ? "Parsing…" : "Preview pfSense migration"}</button></div>
        </form>
        {pfPreview && (
          <div className="dashboard-callout">
            <strong>pfSense migration preview {pfPreview.report.source_version ? `(${pfPreview.report.source_version})` : ""}</strong>
            {importedEntries.length > 0 ? <p>Imported: {importedEntries.map(([name, count]) => `${name.replaceAll("_", " ")}: ${count}`).join(" · ")}</p> : <p>No countable sections were imported.</p>}
            {warnings.length > 0 && <div><strong>Warnings</strong><ul>{warnings.map((warning) => <li key={warning}>{warning}</li>)}</ul></div>}
            {unsupported.length > 0 && <div><strong>Unsupported sections</strong><ul>{unsupported.map((item) => <li key={item}>{item}</li>)}</ul></div>}
            {warnings.length === 0 && unsupported.length === 0 && <p>No migration warnings were reported.</p>}
            <p>Nothing changes until you press Apply. Imported pfSense WAN NAT rules remain disabled because Minimal Router exposes remote services only through WireGuard.</p>
            <div className="form-actions"><button className="button danger" disabled={busy !== ""} onClick={() => void applyPfSense()} type="button">{busy === "pfsense-apply" ? "Applying…" : "Apply pfSense migration"}</button></div>
          </div>
        )}
      </details>
    </article>
  );
}
