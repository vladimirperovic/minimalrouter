import {
  type FormEvent,
  useCallback,
  useEffect,
  useRef,
  useState,
} from "react";
import type { SystemStatus } from "../api-types";
import { apiFetch, responseError } from "../lib/api";
import { useVisiblePolling } from "../lib/useVisiblePolling";
import type { FirewallProps, FirewallStatus } from "../lib/firewall";
import FirewallActivityPanel from "./FirewallActivityPanel";
import FirewallRulesEditor from "./FirewallRulesEditor";
import FirewallPresets from "./FirewallPresets";
import PortForwardsPanel from "./PortForwardsPanel";
import "./FirewallPage.css";

type Explanation = {
  revision: number;
  verdict: string;
  reason: string;
  basis: string;
  source_zone: string;
  destination_zone: string;
  limitations: string[];
  checked_at: string;
  rule_id?: string;
};
type AuditEvent = {
  id: string;
  timestamp: string;
  event_type: string;
  actor: string;
  details?: Record<string, string> | null;
};

function FlowChecker({ revision }: { revision: number }) {
  const [source, setSource] = useState("");
  const [destination, setDestination] = useState("");
  const [protocol, setProtocol] = useState("tcp");
  const [port, setPort] = useState("");
  const [result, setResult] = useState<Explanation | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const request = useRef<AbortController | null>(null);
  const identity = `${source}|${destination}|${protocol}|${port}|${revision}`;
  const [resultKey, setResultKey] = useState("");
  useEffect(() => {
    request.current?.abort();
    setLoading(false);
    setError("");
    return () => request.current?.abort();
  }, [identity]);
  useEffect(() => {
    const hide = () => {
      if (document.hidden) {
        request.current?.abort();
        setLoading(false);
      }
    };
    document.addEventListener("visibilitychange", hide);
    return () => document.removeEventListener("visibilitychange", hide);
  }, []);
  async function check(event: FormEvent) {
    event.preventDefault();
    request.current?.abort();
    const controller = new AbortController();
    request.current = controller;
    setLoading(true);
    setError("");
    setResult(null);
    try {
      const q = new URLSearchParams({
        source_ip: source.trim(),
        destination_ip: destination.trim(),
        protocol,
        port: protocol === "icmp" ? "0" : port,
      });
      const response = await apiFetch(`/api/v1/firewall/explain?${q}`, {
        signal: controller.signal,
      });
      if (!response.ok)
        throw Error(
          await responseError(response, "Unable to explain this connection."),
        );
      const body = (await response.json()) as Explanation;
      if (!body.basis || !Array.isArray(body.limitations))
        throw Error(
          "Connection explanations are unavailable on this firmware.",
        );
      if (!controller.signal.aborted) {
        setResult(body);
        setResultKey(identity);
      }
    } catch (e) {
      if (!controller.signal.aborted)
        setError(
          e instanceof Error ? e.message : "Connection check unavailable.",
        );
    } finally {
      if (!controller.signal.aborted) setLoading(false);
    }
  }
  const shown =
    resultKey === identity && result?.revision === revision ? result : null;
  return (
    <section className="fw-panel fw-checker" aria-labelledby="fw-check-title">
      <header className="fw-panel-head">
        <div>
          <p className="fw-eyebrow">Understand a decision</p>
          <h3 id="fw-check-title">Check a connection</h3>
          <p>Explain how configured policy handles a new IPv4 connection.</p>
        </div>
        <span className="fw-icon" aria-hidden="true">
          ↗
        </span>
      </header>
      <form className="fw-check-form" onSubmit={check}>
        <div className="fw-form-grid">
          <label className="field">
            <span>Source IP</span>
            <input
              required
              placeholder="192.168.1.50"
              value={source}
              onChange={(e) => setSource(e.target.value)}
            />
          </label>
          <label className="field">
            <span>Destination IP</span>
            <input
              required
              placeholder="1.1.1.1"
              value={destination}
              onChange={(e) => setDestination(e.target.value)}
            />
          </label>
          <label className="field">
            <span>Protocol</span>
            <select
              value={protocol}
              onChange={(e) => setProtocol(e.target.value)}
            >
              {["tcp", "udp", "icmp"].map((p) => (
                <option value={p} key={p}>
                  {p.toUpperCase()}
                </option>
              ))}
            </select>
          </label>
          <label className="field">
            <span>Destination port</span>
            <input
              type="number"
              required={protocol !== "icmp"}
              min={1}
              max={65535}
              disabled={protocol === "icmp"}
              placeholder={protocol === "icmp" ? "Not applicable" : "443"}
              value={port}
              onChange={(e) => setPort(e.target.value)}
            />
          </label>
        </div>
        <div className="fw-form-footer">
          <span>No traffic is sent.</span>
          <button className="button secondary" disabled={loading}>
            {loading ? "Checking…" : "Explain connection"}
          </button>
        </div>
      </form>
      {error && (
        <p role="alert" className="fw-error">
          {error}
        </p>
      )}
      {shown && (
        <div className={`fw-flow-result is-${shown.verdict}`} role="status">
          <span className="fw-badge">
            {shown.verdict.replaceAll("_", " ")} ·{" "}
            {shown.basis === "demo" ? "demo preview" : "configured policy"}
          </span>
          <p>
            <strong>{shown.reason}</strong>
          </p>
          <code>
            {shown.source_zone} → {shown.destination_zone}
          </code>
          <small>
            Revision {shown.revision} ·{" "}
            {new Date(shown.checked_at).toLocaleTimeString()}
          </small>
          <ul>
            {shown.limitations.map((note) => (
              <li key={note}>{note}</li>
            ))}
          </ul>
        </div>
      )}
      <p className="fw-note fw-inset">
        An allowed policy does not prove reachability. Live routes, established
        connections, dynamic schedules and service access can change the
        outcome.
      </p>
    </section>
  );
}

function FirewallHistory({ revision }: { revision: number }) {
  const [events, setEvents] = useState<AuditEvent[]>([]),
    [error, setError] = useState("");
  const load = useCallback(async (signal: AbortSignal) => {
    try {
      const response = await apiFetch(
        "/api/v1/audit/events?event_type=firewall.transaction&limit=20",
        { signal },
      );
      if (!response.ok) throw Error("Firewall change history is unavailable.");
      const body = await response.json();
      if (!Array.isArray(body.events))
        throw Error("Change history is unavailable on this firmware.");
      if (!signal.aborted) {
        setEvents(body.events.filter((event: AuditEvent | null) => event?.event_type === "firewall.transaction"));
        setError("");
      }
    } catch (e) {
      if (!signal.aborted)
        setError(e instanceof Error ? e.message : "History unavailable.");
    }
  }, []);
  useVisiblePolling(load, 30000, true, String(revision));
  return (
    <details className="fw-panel fw-history">
      <summary>
        <div>
          <span className="fw-eyebrow">Recorded outcomes</span>
          <h3>Recent Firewall changes</h3>
        </div>
        <span className="fw-meta">Last 20 · ↘</span>
      </summary>
      <div className="fw-history-body">
        {error && (
          <p role="alert" className="fw-error">
            {error} Stored results may be stale.
          </p>
        )}
        {!events.length && !error && (
          <p className="fw-empty">
            No Firewall outcomes recorded yet. Dedicated history begins with
            this version.
          </p>
        )}
        {events.map((event) => (
          <article key={event.id}>
            <span
              className={`fw-badge ${event.details?.state === "Committed" ? "is-good" : ""}`}
            >
              {event.details?.state || "Recorded"}
            </span>
            <div>
              <strong>
                Revision {event.details?.previous_revision || "Unknown"} →{" "}
                {event.details?.candidate_revision || "Unknown"}
              </strong>
              <small>
                {event.details?.phase?.replaceAll("_", " ")} ·{" "}
                {new Date(event.timestamp).toLocaleString()}
              </small>
              <code>{event.details?.transaction_id}</code>
            </div>
            <a href="#logs">View audit</a>
          </article>
        ))}
        <p className="fw-note">
          Transaction IDs connect these outcomes to initiating requests and
          actors in Logs. A pending or accepted change is not a committed
          result.
        </p>
      </div>
    </details>
  );
}

export default function FirewallPage(
  props: FirewallProps & { runtime: NonNullable<SystemStatus["runtime"]> },
) {
  const { config, runtime } = props;
  const [status, setStatus] = useState<FirewallStatus | null>(null),
    [error, setError] = useState("");
  const [actionError, setActionError] = useState("");
  const load = useCallback(async (signal: AbortSignal) => {
    try {
      const response = await apiFetch("/api/v1/firewall/status", { signal });
      if (!response.ok) throw Error("Runtime Firewall status is unavailable.");
      const body = (await response.json()) as FirewallStatus;
      if (!body.runtime || !Array.isArray(body.rules))
        throw Error("Runtime observation is unavailable on this firmware.");
      if (!signal.aborted) {
        setStatus(body);
        setError("");
      }
    } catch (e) {
      if (!signal.aborted) {
        setStatus(null);
        setError(e instanceof Error ? e.message : "Status unavailable.");
      }
    }
  }, []);
  useVisiblePolling(load, 30000, true, String(config.revision));
  const current = status?.revision === config.revision ? status : null;
  const observed = current?.runtime.available;
  const blocked =
    props.busy ||
    Boolean(
      current?.pending_transaction ||
        current?.engine.recovery_required ||
        current?.engine.apply_in_progress,
    );
  const policy = current?.demo
    ? "Demo observation · default deny"
    : !observed
      ? "Not observed"
      : current.runtime.default_deny
        ? "Default deny observed"
        : "Policy needs attention";
  return (
    <section className="dashboard-section fw-workspace" id="firewall">
      <section className="fw-hero">
        <div className="fw-hero-top">
          <div>
            <p className="fw-eyebrow">Network protection</p>
            <h2>Firewall policy</h2>
            <p>
              Know what is allowed, understand what is blocked, and review every
              change.
            </p>
          </div>
          <span className="fw-shield" aria-hidden="true">
            <svg viewBox="0 0 32 36" fill="none">
              <path
                d="M16 2 29 7v10c0 8-8 14-13 17C11 31 3 25 3 17V7L16 2Z"
                stroke="currentColor"
                strokeWidth="1.5"
              />
              <path
                d="m10 17 4 4 8-9"
                stroke="currentColor"
                strokeWidth="1.7"
              />
            </svg>
          </span>
        </div>
        <div className="fw-observation">
          <span
            className={`fw-badge ${observed && current.runtime.default_deny ? "is-good" : "is-warning"}`}
          >
            {policy}
          </span>
          <span>
            {current?.runtime.observed_at
              ? `Last observed ${new Date(current.runtime.observed_at).toLocaleTimeString()}`
              : "Waiting for appliance telemetry"}
          </span>
        </div>
        <dl className="fw-hero-facts">
          <div>
            <dt>Configured WAN policy</dt>
            <dd>Default deny</dd>
            <small>
              WireGuard endpoint is the only permitted new WAN entry
            </small>
          </div>
          <div>
            <dt>Remote tunnel</dt>
            <dd>{config.wireguard.enabled ? "Enabled" : "Disabled"}</dd>
            <small>
              Configured state · <a href="#wireguard">Manage WireGuard ↗</a>
            </small>
          </div>
          <div>
            <dt>Configuration</dt>
            <dd>Revision {config.revision}</dd>
            <small>
              {observed && current.runtime.revision_matches
                ? "Matching revision marker observed"
                : "Current revision not confirmed by observation"}
            </small>
          </div>
          <div>
            <dt>Custom rules</dt>
            <dd>
              {
                (config.firewall.custom_rules || []).filter((r) => r.enabled)
                  .length
              }
              <span> active</span>
            </dd>
            <small>
              {
                (config.firewall.port_forwards || []).filter((r) => r.enabled)
                  .length
              }{" "}
              enabled tunnel forwards
            </small>
          </div>
        </dl>
        <p className="fw-note">
          Observed base policies and revision markers do not verify every live
          rule or end-to-end access.
        </p>
      </section>
      {error && (
        <p role="status" className="fw-caution">
          {error} The configured rules remain visible.
        </p>
      )}
      {actionError && (
        <p role="alert" className="fw-error">
          {actionError}
        </p>
      )}
      {blocked && (
        <p role="status" className="fw-caution">
          Configuration changes are temporarily unavailable. Finish the pending
          operation or resolve recovery first.
        </p>
      )}
      <FirewallActivityPanel
        connections={
          runtime.available !== false &&
          typeof runtime.conntrack_count === "number"
            ? runtime.conntrack_count
            : null
        }
        revision={config.revision}
      />
      <div className="fw-tools-grid">
        <FlowChecker revision={config.revision} />
        <aside className="fw-panel fw-policy-guide">
          <p className="fw-eyebrow">How decisions are made</p>
          <h3>Order matters.</h3>
          <ol>
            <li>
              <strong>Validate the source</strong>
              <span>Anti-spoof checks and invalid-packet rejection.</span>
            </li>
            <li>
              <strong>Apply restrictions</strong>
              <span>
                Device schedules, custom denies and proxy restrictions.
              </span>
            </li>
            <li>
              <strong>Allow known paths</strong>
              <span>
                Established flows, scoped allows, segment ACLs and trusted
                egress.
              </span>
            </li>
            <li>
              <strong>Deny everything else</strong>
              <span>Unmatched input and forwarded traffic is dropped.</span>
            </li>
          </ol>
          <a href="#dns-filter">Review device schedules ↗</a>
          <a href="#security">Manage trusted access ↗</a>
        </aside>
      </div>
      {current?.warnings.length ? (
        <details className="fw-panel fw-warnings">
          <summary>{current.warnings.length} policy notes to review</summary>
          <ul>
            {current.warnings.map((warning, i) => (
              <li key={i}>{warning}</li>
            ))}
          </ul>
        </details>
      ) : null}
      <FirewallRulesEditor {...props} busy={blocked} status={current} />
      <FirewallPresets {...props} busy={blocked} />
      <details className="fw-panel fw-forwards">
        <summary>
          <div>
            <span className="fw-eyebrow">Private remote access</span>
            <h3>Tunnel port forwards</h3>
          </div>
          <span className="fw-badge">
            {(config.firewall.port_forwards || []).length} configured
          </span>
        </summary>
        <PortForwardsPanel onError={setActionError} blocked={blocked} />
      </details>
      <FirewallHistory revision={config.revision} />
    </section>
  );
}
