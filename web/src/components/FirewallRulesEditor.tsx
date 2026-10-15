import { type FormEvent, useState } from "react";
import type { FirewallCustomRule } from "../api-types";
import {
  ruleScope,
  scopeLabel,
  validateRule,
  type FirewallProps,
  type FirewallStatus,
} from "../lib/firewall";

const blankRule = (): FirewallCustomRule => ({
  id: "",
  name: "",
  direction: "forward",
  action: "deny",
  protocol: "tcp",
  src_ip: "",
  dst_port: 0,
  scope: "wan",
  enabled: true,
});

// The editor authors one destination port and no destination addresses.
// Opening a preset's multi-port or address-scoped rule in it would save a
// different rule, so those stay read-only here.
const editableRule = (rule: FirewallCustomRule) =>
  !rule.dst_ips?.length && !rule.dst_ports?.length;
const ruleDestinationPorts = (rule: FirewallCustomRule) =>
  rule.dst_ports?.length
    ? ` / ${rule.dst_ports.join(", ")}`
    : rule.dst_port
      ? ` / ${rule.dst_port}`
      : "";

function RuleIcon({ allow }: { allow: boolean }) {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.5"
      aria-hidden="true"
    >
      <path d="m12 3 8 3v6c0 5-5 8-8 10-3-2-8-5-8-10V6l8-3Z" />
      {allow ? <path d="m8 12 3 3 5-6" /> : <path d="M8 12h8" />}
    </svg>
  );
}

function RuleRow({
  rule,
  counter,
  busy,
  onEdit,
  onToggle,
  onRemove,
}: {
  rule: FirewallCustomRule;
  counter?: FirewallStatus["rules"][number];
  busy: boolean;
  onEdit: (rule: FirewallCustomRule) => void;
  onToggle: (id: string) => void;
  onRemove: (id: string) => void;
}) {
  const destination = {
    router: "Router",
    wan: "Internet / WAN",
    all: "All routed networks",
  }[ruleScope(rule)];
  return (
    <article
      className={`fw-rule ${rule.enabled ? "" : "is-paused"}`}
      aria-label={rule.name}
    >
      <div className="fw-rule-main">
        <span
          className={`fw-rule-verdict ${rule.action === "deny" ? "is-deny" : "is-allow"}`}
        >
          <RuleIcon allow={rule.action === "allow"} />
        </span>
        <div className="fw-rule-identity">
          <h4>{rule.name}</h4>
          <div className="fw-rule-tags">
            <span
              className={`fw-rule-state ${rule.enabled ? "is-enabled" : ""}`}
            >
              <i />
              {rule.enabled ? "Enabled" : "Paused"}
            </span>
            <span>
              {rule.action === "deny" ? "Deny traffic" : "Allow traffic"}
            </span>
            {rule.preset_id && <span>Preset</span>}
          </div>
        </div>
      </div>
      <div className="fw-rule-flow" aria-label={scopeLabel(rule)}>
        <div>
          <small>From LAN</small>
          <code>{rule.src_ip || "All devices"}</code>
        </div>
        <span className="fw-flow-arrow" aria-hidden="true">
          →
        </span>
        <div>
          <small>To</small>
          <strong>
            {rule.dst_ips?.length
              ? `${rule.dst_ips.length} listed destinations · ${destination}`
              : destination}
          </strong>
        </div>
        <span className="fw-rule-protocol">
          {rule.protocol.toUpperCase()}
          {ruleDestinationPorts(rule)}
        </span>
      </div>
      <div className="fw-rule-count">
        <small>Packets since apply</small>
        <strong>
          {counter?.packets == null
            ? "—"
            : new Intl.NumberFormat().format(counter.packets)}
        </strong>
        <span>
          {counter?.last_increase_observed_at
            ? `Increase seen ${new Date(counter.last_increase_observed_at).toLocaleTimeString()}`
            : "No match time observed"}
        </span>
      </div>
      <div className="fw-row-actions">
        <button
          disabled={busy || !editableRule(rule)}
          onClick={() => onEdit(rule)}
          aria-label={`Edit ${rule.name}`}
          title={
            editableRule(rule)
              ? undefined
              : "Address lists and port sets come from Suggested rules. Pause, remove or restore the preset there."
          }
        >
          <svg
            viewBox="0 0 20 20"
            fill="none"
            stroke="currentColor"
            strokeWidth="1.4"
            aria-hidden="true"
          >
            <path d="m4 12 9-9 4 4-9 9-5 1 1-5ZM11 5l4 4" />
          </svg>
          Edit rule
        </button>
        <button
          disabled={busy}
          onClick={() => onToggle(rule.id)}
          aria-label={`${rule.enabled ? "Pause" : "Enable"} ${rule.name}`}
        >
          {rule.enabled ? "Pause" : "Enable"}
        </button>
        <button
          disabled={busy}
          onClick={() => onRemove(rule.id)}
          className="is-remove"
          aria-label={`Remove ${rule.name}`}
          title={`Remove ${rule.name}`}
        >
          <svg
            viewBox="0 0 20 20"
            fill="none"
            stroke="currentColor"
            strokeWidth="1.4"
            aria-hidden="true"
          >
            <path d="M3 5h14M7 5V3h6v2M5 5l1 12h8l1-12M8 8v6M12 8v6" />
          </svg>
          <span className="sr-only">Remove</span>
        </button>
      </div>
    </article>
  );
}

export default function FirewallRulesEditor({
  config,
  busy,
  applyConfig,
  status,
}: FirewallProps & { status: FirewallStatus | null }) {
  const [draft, setDraft] = useState(blankRule);
  const [original, setOriginal] = useState<FirewallCustomRule | null>(null);
  const [open, setOpen] = useState(false);
  const [error, setError] = useState("");
  const [query, setQuery] = useState("");
  const [filter, setFilter] = useState("all");
  const rules = config.firewall.custom_rules || [];
  const visible = rules.filter(
    (rule) =>
      (filter === "all" ||
        (filter === "paused"
          ? !rule.enabled
          : rule.enabled && rule.action === filter)) &&
      `${rule.name} ${rule.src_ip} ${rule.protocol} ${rule.dst_port} ${(rule.dst_ports || []).join(" ")} ${scopeLabel(rule)}`
        .toLowerCase()
        .includes(query.toLowerCase()),
  );
  const needsPort = draft.protocol === "tcp" || draft.protocol === "udp";
  const set = (value: Partial<FirewallCustomRule>) =>
    setDraft((current) => ({ ...current, ...value }));
  function close() {
    setOpen(false);
    setOriginal(null);
    setDraft(blankRule());
    setError("");
  }
  function edit(rule: FirewallCustomRule) {
    setOriginal(structuredClone(rule));
    setDraft({ ...rule, scope: ruleScope(rule) === "wan" ? "wan" : "all" });
    setOpen(true);
    setError("");
  }
  async function submit(event: FormEvent) {
    event.preventDefault();
    const candidate = {
      ...draft,
      name: draft.name.trim(),
      src_ip: draft.src_ip?.trim(),
      dst_port: needsPort ? draft.dst_port : 0,
      scope:
        draft.direction === "input"
          ? undefined
          : draft.action === "allow"
            ? ("wan" as const)
            : draft.scope,
    };
    const message = validateRule(candidate, config);
    setError(message);
    if (message) return;
    const saved = await applyConfig(
      (next) => {
        const latest = next.firewall.custom_rules || [];
        if (original) {
          const current = latest.find((r) => r.id === original.id);
          if (!current || JSON.stringify(current) !== JSON.stringify(original))
            throw Error(
              "This rule changed in another session. Cancel and reopen it before saving.",
            );
          next.firewall.custom_rules = latest.map((r) =>
            r.id === original.id ? candidate : r,
          );
        } else
          next.firewall.custom_rules = [
            ...latest,
            { ...candidate, id: `rule-${crypto.randomUUID()}` },
          ];
      },
      original ? "Firewall rule updated." : "Firewall rule added.",
    );
    if (saved) close();
  }
  function toggle(id: string) {
    void applyConfig((next) => {
      next.firewall.custom_rules = (next.firewall.custom_rules || []).map(
        (r) => (r.id === id ? { ...r, enabled: !r.enabled } : r),
      );
    }, "Firewall rule updated.");
  }
  function remove(id: string) {
    void applyConfig((next) => {
      next.firewall.custom_rules = (next.firewall.custom_rules || []).filter(
        (r) => r.id !== id,
      );
    }, "Firewall rule removed.");
  }

  return (
    <section className="fw-panel" aria-labelledby="fw-rules-title">
      <header className="fw-panel-head">
        <div>
          <p className="fw-eyebrow">Your policy</p>
          <h3 id="fw-rules-title">Custom rules</h3>
          <p>
            LAN rules with an explicit destination scope. Denies run before
            allows.
          </p>
        </div>
        <button
          className="button primary"
          disabled={busy}
          onClick={() => {
            close();
            setOpen(true);
          }}
        >
          + Add rule
        </button>
      </header>
      <div className="fw-toolbar">
        <label className="fw-search">
          <span className="sr-only">Search firewall rules</span>
          <input
            type="search"
            placeholder="Search name, source, protocol or port"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
          />
        </label>
        <label>
          <span className="sr-only">Filter firewall rules</span>
          <select value={filter} onChange={(e) => setFilter(e.target.value)}>
            <option value="all">All rules</option>
            <option value="deny">Active denies</option>
            <option value="allow">Active allows</option>
            <option value="paused">Paused</option>
          </select>
        </label>
        <span className="fw-meta">
          {visible.length} / {rules.length} rules
        </span>
      </div>
      <div className="fw-rule-list">
        {visible.length === 0 ? (
          <p className="fw-empty">
            {rules.length
              ? "No rules match your filters."
              : "No custom rules. The built-in policy remains in place."}
          </p>
        ) : (
          visible.map((rule) => (
            <RuleRow
              key={rule.id}
              rule={rule}
              counter={
                status?.revision === config.revision
                  ? status.rules.find((r) => r.id === rule.id)
                  : undefined
              }
              busy={busy}
              onEdit={edit}
              onToggle={toggle}
              onRemove={remove}
            />
          ))
        )}
      </div>
      {open && (
        <form
          className="fw-editor"
          onSubmit={submit}
          aria-label={original ? "Edit firewall rule" : "New firewall rule"}
        >
          <div className="fw-editor-title">
            <div>
              <p className="fw-eyebrow">
                {original ? "Update rule" : "Create rule"}
              </p>
              <h4>{original?.name || "Define the traffic to match"}</h4>
            </div>
            <button
              className="button secondary small"
              type="button"
              onClick={close}
            >
              Cancel
            </button>
          </div>
          <div className="fw-form-grid">
            <label className="field">
              <span>Rule name</span>
              <input
                required
                maxLength={64}
                value={draft.name}
                onChange={(e) => set({ name: e.target.value })}
                placeholder="Block outbound service"
              />
            </label>
            <label className="field">
              <span>Direction</span>
              <select
                value={draft.direction}
                onChange={(e) =>
                  set({
                    direction: e.target.value,
                    action: e.target.value === "input" ? "allow" : draft.action,
                    scope: "wan",
                  })
                }
              >
                <option value="forward">Through router</option>
                <option
                  value="input"
                  disabled={
                    config.system.management_access === "wireguard_only"
                  }
                >
                  To router
                </option>
              </select>
            </label>
            <label className="field">
              <span>Action</span>
              <select
                value={draft.action}
                onChange={(e) => set({ action: e.target.value })}
              >
                <option value="deny" disabled={draft.direction === "input"}>
                  Deny
                </option>
                <option value="allow">Allow</option>
              </select>
            </label>
            <label className="field">
              <span>Source device or subnet</span>
              <input
                value={draft.src_ip || ""}
                onChange={(e) => set({ src_ip: e.target.value })}
                placeholder={`All LAN · ${config.lan.cidr}`}
              />
            </label>
            <label className="field">
              <span>Protocol</span>
              <select
                value={draft.protocol}
                onChange={(e) => set({ protocol: e.target.value })}
              >
                {["tcp", "udp", "icmp", "any"].map((p) => (
                  <option key={p} value={p}>
                    {p.toUpperCase()}
                  </option>
                ))}
              </select>
            </label>
            <label className="field">
              <span>Destination port</span>
              <input
                type="number"
                min={1}
                max={65535}
                required={needsPort}
                disabled={!needsPort}
                value={needsPort ? draft.dst_port || "" : ""}
                onChange={(e) => set({ dst_port: Number(e.target.value) })}
                placeholder={needsPort ? "1–65535" : "Not applicable"}
              />
            </label>
            <label className="field">
              <span>Destination scope</span>
              <select
                disabled={
                  draft.direction === "input" || draft.action === "allow"
                }
                value={
                  draft.direction === "input" || draft.action === "allow"
                    ? "wan"
                    : draft.scope || "all"
                }
                onChange={(e) =>
                  set({ scope: e.target.value as "wan" | "all" })
                }
              >
                <option value="wan">
                  {draft.direction === "input"
                    ? "Router only"
                    : "Internet / WAN only"}
                </option>
                <option value="all">
                  All routed networks, including tunnels
                </option>
              </select>
            </label>
          </div>
          <p className="fw-note">
            Router-input denies are disabled to protect access. Forward allows
            are WAN-only and cannot override earlier denies, device schedules or
            segment isolation.
          </p>
          {error && (
            <p role="alert" className="fw-error">
              {error}
            </p>
          )}
          <div className="fw-form-footer">
            <span>Changes are validated and reviewed before applying.</span>
            <button className="button primary" disabled={busy}>
              {busy ? "Reviewing…" : "Review rule"}
            </button>
          </div>
        </form>
      )}
    </section>
  );
}
