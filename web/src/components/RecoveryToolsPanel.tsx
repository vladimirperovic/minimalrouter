import { useState } from "react";
import type { RouterConfig } from "../api-types";
import type { RecoveryActions } from "../lib/useRecoveryActions";

export default function RecoveryToolsPanel({ config, actions }: { config: RouterConfig; actions: RecoveryActions }) {
  const [olderPassword, setOlderPassword] = useState(false);
  const { busy } = actions;
  return <article className="recovery-tools" aria-label="Recovery tools">
      <div className="recovery-workflows">
      <section className="recovery-workflow recovery-transfer">
        <header className="recovery-workflow-head"><span className="recovery-workflow-icon is-export" aria-hidden="true">&#8599;</span><span className="demo-recovery-label">Export encrypted backup<small>Encrypted Minimal Router backup (.mrbak)</small></span></header>
        <form className="settings-form" onSubmit={actions.exportBackup}>
          <p className="form-note">Your current dashboard password also encrypts this file. Keep the password used for each exported file.</p>
          <label className="field"><span>Current administrator password</span><input autoComplete="current-password" minLength={12} name="current_password" required type="password" /></label>
          <p className="form-note">Save the downloaded .mrbak file somewhere safe. If you change your dashboard password, older files still need the password used when they were created.</p>
          <div className="recovery-contents"><strong>Inside your backup</strong><ul><li>Router configuration</li><li>Network DNS policy, when available</li></ul><p>Lists and activity history are excluded. Restore on v0.2.0 or later.</p></div>
          <div className="form-actions"><button className="button primary" disabled={busy !== ""} type="submit">{busy === "backup-export" ? "Encrypting…" : "Export encrypted backup"}</button></div>
        </form>
      </section>

      <section className="recovery-workflow recovery-transfer">
        <header className="recovery-workflow-head"><span className="recovery-workflow-icon is-restore" aria-hidden="true">&#8601;</span><span className="demo-recovery-label">Restore encrypted backup<small>Validate first. Apply when you are ready.</small></span></header>
        <form className="settings-form" onSubmit={actions.previewBackup} onChange={actions.clearPreview}>
          <div className="form-grid two">
            <label className="field form-span"><span>Backup file</span><input accept=".mrbak,application/json" name="backup" required type="file" /></label>
            <label className="field"><span>Current administrator password</span><input autoComplete="current-password" name="restore_current_password" required type="password" /></label>
            <label className="field form-span recovery-password-option"><span><input checked={olderPassword} onChange={event => setOlderPassword(event.target.checked)} type="checkbox" /> This backup uses an older or separate password</span></label>
            {olderPassword && <label className="field form-span"><span>Password used to create this backup</span><input autoComplete="off" name="restore_backup_passphrase" required type="password" /></label>}
          </div>
          <p className="form-note">We first check the file and show the settings to restore. Nothing changes until you apply the validated backup.</p>
          <div className="form-actions"><button className="button secondary" disabled={busy !== ""} type="submit">{busy === "backup-preview" ? "Validating…" : "Validate backup"}</button></div>
        </form>
      </section>

      <details className="recovery-workflow recovery-migration">
        <summary><span className="recovery-workflow-icon is-migration">&#8644;</span><span className="demo-recovery-label">Migrate from pfSense config.xml</span><span className="demo-recovery-badge is-migration">Migration only</span><span className="demo-recovery-chevron" aria-hidden="true">›</span></summary>
        <form className="settings-form" onSubmit={actions.previewPfSense} onChange={actions.clearPreview}>
          <div className="form-grid two">
            <label className="field form-span"><span>pfSense config.xml</span><input accept=".xml,application/xml,text/xml" name="pfsense_xml" required type="file" /></label>
            <label className="field"><span>Target WAN interface</span><input defaultValue={config.wan.interface} name="target_wan" required /></label>
            <label className="field"><span>Target LAN interface</span><input defaultValue={config.lan.interface} name="target_lan" required /></label>
          </div>
          <p className="form-note">FreeBSD interface names are never trusted. You must map the imported configuration onto explicit Linux WAN/LAN interfaces before previewing it.</p>
          <div className="form-actions"><button className="button secondary" disabled={busy !== ""} type="submit">{busy === "pfsense-preview" ? "Parsing…" : "Preview pfSense migration"}</button></div>
        </form>
      </details>
      </div>
    <section className="recovery-diagnostics card"><div><span className="eyebrow">Troubleshoot with context</span><h3>Diagnostics for troubleshooting</h3><p>Health, resource usage and recent recovery events in one redacted report. Private network details may remain; review before sharing.</p></div><button className="button secondary" disabled={busy !== ""} onClick={() => void actions.downloadDiagnostics()} type="button">{busy === "diagnostics" ? "Building…" : "Download diagnostics"}</button></section>
  </article>;
}
