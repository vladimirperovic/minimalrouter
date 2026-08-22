import { type FormEvent, useState } from "react";
import type { FirewallCustomRule, RouterConfig } from "../api-types";

// internal/services/nftables.go has always emitted custom rules into all four
// input/forward x allow/deny positions. Until now the only way to create one was
// a hand-written PUT to /api/v1/config, so the capability was effectively
// invisible. The generator's constraints are mirrored in the help text below so
// the form cannot promise something nftables will not do.

type Props = {
  config: RouterConfig;
  busy: boolean;
  applyConfig: (mutate: (next: RouterConfig) => void, success: string) => void;
};

const DIRECTIONS = [
  { value: "input", label: "To the router (input)" },
  { value: "forward", label: "Through the router (forward)" },
];

const ACTIONS = [
  { value: "allow", label: "Allow" },
  { value: "deny", label: "Deny" },
];

const PROTOCOLS = ["tcp", "udp", "any"];

function isValidSource(value: string): boolean {
  if (value === "") return true;
  const [address, prefix] = value.split("/");
  const parts = address.split(".");
  if (parts.length !== 4) return false;
  if (!parts.every((part) => /^\d{1,3}$/.test(part) && Number(part) <= 255)) return false;
  if (prefix === undefined) return true;
  return /^\d{1,2}$/.test(prefix) && Number(prefix) <= 32;
}

export default function FirewallRulesEditor({ config, busy, applyConfig }: Props) {
  const rules: FirewallCustomRule[] = config.firewall.custom_rules || [];
  const [name, setName] = useState("");
  const [direction, setDirection] = useState("input");
  const [action, setAction] = useState("allow");
  const [protocol, setProtocol] = useState("tcp");
  const [srcIP, setSrcIP] = useState("");
  const [dstPort, setDstPort] = useState("");
  const [editingId, setEditingId] = useState<string | null>(null);
  const [error, setError] = useState("");

  const needsPort = protocol === "tcp" || protocol === "udp";

  const startEdit = (rule: FirewallCustomRule) => {
    setEditingId(rule.id);
    setName(rule.name);
    setDirection(rule.direction);
    setAction(rule.action);
    setProtocol(rule.protocol);
    setSrcIP(rule.src_ip || "");
    setDstPort(rule.dst_port ? String(rule.dst_port) : "");
    setError("");
  };

  const cancelEdit = () => {
    setEditingId(null);
    setName("");
    setDirection("input");
    setAction("allow");
    setProtocol("tcp");
    setSrcIP("");
    setDstPort("");
    setError("");
  };

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setError("");
    if (name.trim() === "") {
      setError("Give the rule a name so it is recognisable in the ruleset.");
      return;
    }
    if (!isValidSource(srcIP.trim())) {
      setError("Source must be an IPv4 address or CIDR, or left empty for any LAN host.");
      return;
    }
    const port = Number(dstPort);
    if (needsPort && (!Number.isInteger(port) || port < 1 || port > 65535)) {
      setError("Destination port must be between 1 and 65535 for TCP and UDP rules.");
      return;
    }
    if (editingId) {
      applyConfig((next) => {
        next.firewall = {
          ...next.firewall,
          custom_rules: (next.firewall.custom_rules || []).map((rule) => (
            rule.id === editingId
              ? { ...rule, name: name.trim(), action, direction, protocol, src_ip: srcIP.trim(), dst_port: needsPort ? port : 0 }
              : rule
          )),
        };
      }, "Firewall rule updated.");
      cancelEdit();
      return;
    }
    applyConfig((next) => {
      next.firewall = {
        ...next.firewall,
        custom_rules: [
          ...(next.firewall.custom_rules || []),
          {
            id: `rule-${Date.now().toString(36)}`,
            name: name.trim(),
            action,
            direction,
            protocol,
            src_ip: srcIP.trim(),
            dst_port: needsPort ? port : 0,
            enabled: true,
          },
        ],
      };
    }, "Firewall rule saved.");
    setName("");
    setSrcIP("");
    setDstPort("");
  };

  const toggle = (id: string) => {
    applyConfig((next) => {
      next.firewall = {
        ...next.firewall,
        custom_rules: (next.firewall.custom_rules || []).map((rule) =>
          rule.id === id ? { ...rule, enabled: !rule.enabled } : rule,
        ),
      };
    }, "Firewall rule updated.");
  };

  const remove = (id: string) => {
    applyConfig((next) => {
      next.firewall = {
        ...next.firewall,
        custom_rules: (next.firewall.custom_rules || []).filter((rule) => rule.id !== id),
      };
    }, "Firewall rule removed.");
  };

  return (
    <article className="card table-card firewall-rules">
      <div className="card-title-row">
        <div>
          <h3>Custom rules</h3>
          <p>Extra allow/deny rules evaluated alongside the default-deny policy.</p>
        </div>
        <span className="quiet-meta">{rules.filter((rule) => rule.enabled).length} active</span>
      </div>

      <div className="table-scroll">
        <table>
          <thead>
            <tr><th>Name</th><th>Direction</th><th>Action</th><th>Match</th><th>State</th><th>Actions</th></tr>
          </thead>
          <tbody>
            {rules.length === 0 ? (
              <tr><td className="empty-state" colSpan={6}>No custom rules. The default-deny policy applies on its own.</td></tr>
            ) : (
              rules.map((rule) => (
                <tr className={rule.enabled ? "" : "is-paused"} key={rule.id}>
                  <td>{rule.name}</td>
                  <td>{rule.direction === "forward" ? "Through router" : "To router"}</td>
                  <td className={rule.action === "deny" ? "is-bad" : "is-good"}>{rule.action}</td>
                  <td>
                    <code>
                      {rule.src_ip || "any LAN host"}
                      {rule.protocol !== "any" ? ` · ${rule.protocol}` : ""}
                      {rule.dst_port ? `/${rule.dst_port}` : ""}
                    </code>
                  </td>
                  <td>{rule.enabled ? "Enabled" : "Paused"}</td>
                  <td className="firewall-rule-actions">
                    <button className="button secondary small" disabled={busy} onClick={() => (editingId === rule.id ? cancelEdit() : startEdit(rule))} type="button">
                      {editingId === rule.id ? "Cancel" : "Edit"}
                    </button>
                    <button className="button secondary small" disabled={busy} onClick={() => toggle(rule.id)} type="button">
                      {rule.enabled ? "Pause" : "Enable"}
                    </button>
                    <button className="button secondary small" disabled={busy} onClick={() => remove(rule.id)} type="button">
                      Remove
                    </button>
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>

      <form className="settings-form" onSubmit={submit}>
        <div className="form-grid three">
          <label className="field">
            <span>Rule name</span>
            <input onChange={(event) => setName(event.target.value)} placeholder="Allow printer" required value={name} />
          </label>
          <label className="field">
            <span>Direction</span>
            <select onChange={(event) => setDirection(event.target.value)} value={direction}>
              {DIRECTIONS.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}
            </select>
          </label>
          <label className="field">
            <span>Action</span>
            <select onChange={(event) => setAction(event.target.value)} value={action}>
              {ACTIONS.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}
            </select>
          </label>
          <label className="field">
            <span>Source (blank = any LAN host)</span>
            <input onChange={(event) => setSrcIP(event.target.value)} placeholder="192.168.1.50" value={srcIP} />
          </label>
          <label className="field">
            <span>Protocol</span>
            <select onChange={(event) => setProtocol(event.target.value)} value={protocol}>
              {PROTOCOLS.map((item) => <option key={item} value={item}>{item.toUpperCase()}</option>)}
            </select>
          </label>
          <label className="field">
            <span>Destination port</span>
            <input
              disabled={!needsPort}
              onChange={(event) => setDstPort(event.target.value)}
              placeholder="631"
              type="number"
              value={dstPort}
            />
          </label>
        </div>
        <p className="form-note">
          Rules match traffic arriving on the LAN interface. A forward <strong>allow</strong> is restricted to LAN&nbsp;→&nbsp;WAN
          egress by the generator: it can never open a path toward another LAN segment or a tunnel.
        </p>
        {error && <p className="form-note is-error" role="alert">{error}</p>}
        <div className="form-actions">
          {editingId && <button className="button secondary" disabled={busy} onClick={cancelEdit} type="button">Cancel edit</button>}
          <button className="button primary" disabled={busy} type="submit">{editingId ? "Save changes" : "Add rule"}</button>
        </div>
      </form>
    </article>
  );
}
