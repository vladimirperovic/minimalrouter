import { useEffect, useRef } from "react";
import type { FormEvent } from "react";
import {
  isTerminal,
  UPDATE_BLOCK_EXPLANATION,
  UPDATE_PHASE_LABEL,
  type UpdatesController,
} from "../lib/updates";
import "./UpdateDialog.css";

type Props = {
  controller: UpdatesController;
  onClose: () => void;
  readOnly?: boolean;
};

function formatWhen(value?: string) {
  if (!value) return "";
  const parsed = new Date(value);
  if (Number.isNaN(parsed.getTime())) return "";
  return parsed.toLocaleString([], { dateStyle: "medium", timeStyle: "short" });
}

/**
 * UpdateDialog is the single update surface: the sidebar entry and the profile
 * menu both open this component, so there is one state and one flow rather
 * than two that can disagree.
 */
export default function UpdateDialog({ controller, onClose, readOnly = false }: Props) {
  const { status, active, busy, error, reconnecting, checkNow, install, setChannel, uploadSignedBuild } = controller;
  const dialogRef = useRef<HTMLDivElement>(null);
  const closeRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    closeRef.current?.focus();
  }, []);

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      // Escape closes the dialog, but never cancels work the appliance has
      // already accepted: closing is a view change, not an abort.
      if (event.key === "Escape") onClose();
      if (event.key !== "Tab" || !dialogRef.current) return;
      const focusable = dialogRef.current.querySelectorAll<HTMLElement>(
        'button:not([disabled]), input:not([disabled]), select:not([disabled]), a[href]',
      );
      if (focusable.length === 0) return;
      const first = focusable[0];
      const last = focusable[focusable.length - 1];
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first.focus();
      }
    };
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  }, [onClose]);

  const operation = status?.operation ?? null;
  const phase = operation ? UPDATE_PHASE_LABEL[operation.state] : "";
  const blocked = status?.blocked_reason ?? "";
  const blockedText = blocked && blocked !== "already_current" ? UPDATE_BLOCK_EXPLANATION[blocked] ?? "" : "";
  const installable = Boolean(status?.can_install && status?.update_available) && !readOnly && !active;
  // "Up to date" may only be claimed on the strength of a check that actually
  // succeeded. No status, a reported error, a stale answer or a check that has
  // never succeeded all mean the same thing: unknown.
  const checkSucceeded = Boolean(status?.last_successful_check_at) && !status?.stale && !status?.check_error;
  const checkFailed = !checkSucceeded;

  const submitUpload = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    void uploadSignedBuild(new FormData(event.currentTarget));
  };

  return (
    <div className="update-dialog-backdrop" role="presentation">
      <div
        aria-labelledby="update-dialog-title"
        aria-modal="true"
        className="update-dialog"
        ref={dialogRef}
        role="dialog"
      >
        <header className="update-dialog-header">
          <h2 id="update-dialog-title">Software update</h2>
          <button aria-label="Close" className="update-dialog-close" onClick={onClose} ref={closeRef} type="button">×</button>
        </header>

        <dl className="update-dialog-versions">
          <div><dt>Installed</dt><dd>{status?.current_version || "Unknown"}</dd></div>
          <div><dt>Running now</dt><dd>{status?.running_version || "Unknown"}</dd></div>
          <div>
            <dt>Available</dt>
            <dd>{status?.target_version ? `v${status.target_version}${status.prerelease ? " (Beta)" : ""}` : "—"}</dd>
          </div>
        </dl>

        {status?.last_successful_check_at && (
          <p className="update-dialog-checked">Last successful check: {formatWhen(status.last_successful_check_at)}</p>
        )}

        {/* A failed or stale check is reported as unknown, never as up to date. */}
        {checkFailed && (
          <p className="update-dialog-note is-warning" role="status">
            Update check unavailable{status?.rate_limited ? " — the release service asked us to wait." : "."}
            {" "}The appliance cannot confirm whether a newer release exists.
          </p>
        )}

        {checkSucceeded && !status?.update_available && !operation && (
          <p className="update-dialog-note is-current">This appliance is on the newest published release.</p>
        )}

        {status?.pending_version && (
          <p className="update-dialog-note is-warning">Verified release {status.pending_version} is already waiting to be activated.</p>
        )}

        {blockedText && <p className="update-dialog-note is-warning">{blockedText}</p>}

        {status?.update_available && status.release_notes && (
          <details className="update-dialog-notes">
            <summary>What changed in v{status.target_version}</summary>
            {/* Release notes are author-controlled text and are rendered as
                text: never as markup. */}
            <pre>{status.release_notes}</pre>
            {status.release_url && (
              <a href={status.release_url} rel="noreferrer noopener" target="_blank">Open the release page</a>
            )}
          </details>
        )}

        {status?.update_available && installable && (
          <p className="update-dialog-note">
            The appliance downloads and signature-verifies v{status.target_version}, switches A/B slots and restarts its
            services. Management and routing are briefly interrupted. If the new version does not become healthy, the
            previous one is restored automatically.
          </p>
        )}

        {operation && (
          <div className="update-dialog-progress" role="status" aria-live="polite">
            <strong>{reconnecting && !isTerminal(operation.state) ? "Reconnecting…" : phase}</strong>
            <span>
              v{operation.from_version} → v{operation.target_version}
            </span>
            {operation.state === "rolled_back" && (
              <p className="update-dialog-note is-warning">
                The previous version v{operation.from_version} is running again.{operation.error ? ` ${operation.error}` : ""}
              </p>
            )}
            {operation.state === "failed" && (
              <p className="update-dialog-note is-warning">{operation.error || "The update did not complete."}</p>
            )}
            {operation.state === "recovery_required" && (
              <p className="update-dialog-note is-warning">{operation.error || "This appliance needs attention before another update."}</p>
            )}
            {operation.state === "succeeded" && (
              <p className="update-dialog-note is-current">v{operation.target_version} is running. Reloading the dashboard…</p>
            )}
          </div>
        )}

        {error && <p className="update-dialog-note is-warning" role="alert">{error}</p>}

        <div className="update-dialog-actions">
          <button className="button secondary small" disabled={busy || readOnly} onClick={() => void checkNow()} type="button">
            {busy ? "Checking…" : "Check now"}
          </button>
          <div className="update-dialog-spacer" />
          <button className="button secondary small" onClick={onClose} type="button">Close</button>
          <button
            className="button primary small"
            disabled={!installable || busy}
            onClick={() => void install()}
            type="button"
          >
            {active ? "Updating…" : status?.target_version ? `Update to v${status.target_version}` : "Update"}
          </button>
        </div>

        {!readOnly && (
          <fieldset className="update-dialog-channel" disabled={busy || active}>
            <legend>Release channel</legend>
            <label>
              <input
                aria-label="Stable releases only"
                checked={status?.channel === "stable"}
                name="update-channel"
                onChange={() => void setChannel("stable")}
                type="radio"
              />
              <span>Stable only</span>
            </label>
            <label>
              <input
                aria-label="Include Beta releases"
                checked={status?.channel === "beta"}
                name="update-channel"
                onChange={() => void setChannel("beta")}
                type="radio"
              />
              <span>Include Beta releases</span>
            </label>
          </fieldset>
        )}

        {!readOnly && (
          <details className="update-dialog-upload">
            <summary>Install a signed build from a file</summary>
            <form onSubmit={submitUpload}>
              <label><span>Signed manifest</span><input accept=".json,application/json" disabled={busy || active} name="manifest" required type="file" /></label>
              <label><span>Release archive</span><input accept=".tar.gz,.tgz,application/gzip" disabled={busy || active} name="archive" required type="file" /></label>
              <p>
                Use the archive for this appliance's architecture together with its signed <code>.manifest.json</code>.
                Unsigned, altered, wrong-architecture, same-version or older builds are rejected by the updater.
              </p>
              <button className="button secondary small" disabled={busy || active} type="submit">Upload signed build</button>
            </form>
          </details>
        )}
      </div>
    </div>
  );
}
