import { type FormEvent, useEffect, useState } from "react";
import { apiFetch } from "../lib/api";
import type { PortForwardRule, RouterConfig } from "../api-types";

interface Props {
  onError: (message: string) => void;
}

const isValidPort = (value: string): boolean => {
  const port = Number(value);
  return Number.isInteger(port) && port >= 1 && port <= 65535;
};

const isValidIPv4 = (value: string): boolean => {
  const parts = value.split(".");
  if (parts.length !== 4) return false;
  return parts.every((part) => {
    const octet = Number(part);
    return /^\d{1,3}$/.test(part) && octet >= 0 && octet <= 255;
  });
};

export default function PortForwardsPanel({ onError }: Props) {
  const [rules, setRules] = useState<PortForwardRule[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [saving, setSaving] = useState(false);
  const [name, setName] = useState("");
  const [protocol, setProtocol] = useState("tcp");
  const [externalPort, setExternalPort] = useState("");
  const [internalIP, setInternalIP] = useState("");
  const [internalPort, setInternalPort] = useState("");

  useEffect(() => {
    void load();
  }, []);

  const load = async () => {
    try {
      const response = await apiFetch("/api/v1/config");
      if (!response.ok) throw new Error(`Configuration load failed (${response.status})`);
      const config = (await response.json()) as RouterConfig;
      setRules(Array.isArray(config.firewall?.port_forwards) ? config.firewall.port_forwards : []);
      setLoaded(true);
    } catch (error) {
      onError(error instanceof Error ? error.message : "Port forwards configuration unavailable");
    }
  };

  const persist = async (next: PortForwardRule[]) => {
    setSaving(true);
    try {
      const currentResponse = await apiFetch("/api/v1/config");
      if (!currentResponse.ok) throw new Error(`Configuration load failed (${currentResponse.status})`);
      const config = (await currentResponse.json()) as RouterConfig;
      config.firewall = config.firewall ?? ({} as RouterConfig["firewall"]);
      config.firewall.port_forwards = next;
      const response = await apiFetch("/api/v1/config", {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(config),
      });
      const body = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(body.error || `Port forwards apply failed (${response.status})`);
      setRules(next);
      onError("");
    } catch (error) {
      onError(error instanceof Error ? error.message : "Port forwards update failed");
    } finally {
      setSaving(false);
    }
  };

  const add = async (event: FormEvent) => {
    event.preventDefault();
    if (!name.trim() || !/^[a-zA-Z0-9 _-]{1,64}$/.test(name.trim())) {
      onError("Name must be 1-64 characters (letters, digits, space, dash, underscore).");
      return;
    }
    if (!isValidPort(externalPort)) {
      onError("External port must be between 1 and 65535.");
      return;
    }
    if (!isValidPort(internalPort)) {
      onError("Internal port must be between 1 and 65535.");
      return;
    }
    if (!isValidIPv4(internalIP)) {
      onError("Internal IP must be a valid IPv4 address (e.g. 192.168.1.50).");
      return;
    }
    const rule: PortForwardRule = {
      id: `pf-${Date.now().toString(36)}`,
      name: name.trim(),
      protocol,
      external_port: Number(externalPort),
      internal_ip: internalIP,
      internal_port: Number(internalPort),
      enabled: true,
    };
    await persist([...rules, rule]);
    setName("");
    setExternalPort("");
    setInternalIP("");
    setInternalPort("");
  };

  const toggle = async (id: string) => {
    await persist(rules.map((rule) => (rule.id === id ? { ...rule, enabled: !rule.enabled } : rule)));
  };

  const remove = async (id: string) => {
    await persist(rules.filter((rule) => rule.id !== id));
  };

  return (
    <div className="classic-live-card classic-security-card">
      <h3>Tunnel port forwards</h3>
      <p className="classic-security-intro">
        Reachable only over the WireGuard tunnel (e.g. 10.6.0.1:&lt;port&gt;) — never from the WAN.
        Traffic arriving at the router&apos;s WireGuard address on the external port is forwarded to the internal host.
      </p>

      {!loaded ? (
        <p className="classic-security-empty">Loading…</p>
      ) : (
        <>
          <form className="form-grid" onSubmit={add}>
            <label className="field">
              <span>Name</span>
              <input onChange={(event) => setName(event.target.value)} placeholder="opencode" value={name} />
            </label>
            <label className="field">
              <span>Protocol</span>
              <select onChange={(event) => setProtocol(event.target.value)} value={protocol}>
                <option value="tcp">TCP</option>
                <option value="udp">UDP</option>
                <option value="both">TCP + UDP</option>
              </select>
            </label>
            <label className="field">
              <span>External port</span>
              <input onChange={(event) => setExternalPort(event.target.value)} placeholder="4080" value={externalPort} />
            </label>
            <label className="field">
              <span>Internal IP</span>
              <input onChange={(event) => setInternalIP(event.target.value)} placeholder="192.168.1.50" value={internalIP} />
            </label>
            <label className="field">
              <span>Internal port</span>
              <input onChange={(event) => setInternalPort(event.target.value)} placeholder="4080" value={internalPort} />
            </label>
            <div className="modal-actions">
              <button className="modal-action-button" disabled={saving} type="submit">
                {saving ? "Applying…" : "Add forward"}
              </button>
            </div>
          </form>

          {rules.length === 0 ? (
            <p className="classic-security-empty">No port forwards configured. WireGuard remains the only external entry point.</p>
          ) : (
            <table className="classic-security-table">
              <thead>
                <tr>
                  <th>Name</th>
                  <th>Protocol</th>
                  <th>External port</th>
                  <th>Forwards to</th>
                  <th>State</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {rules.map((rule) => (
                  <tr key={rule.id}>
                    <td><strong>{rule.name}</strong></td>
                    <td><code>{rule.protocol}</code></td>
                    <td><code>{rule.external_port}</code></td>
                    <td><code>{rule.internal_ip}:{rule.internal_port}</code></td>
                    <td>{rule.enabled ? "enabled" : "disabled"}</td>
                    <td className="table-actions">
                      <button disabled={saving} onClick={() => toggle(rule.id)} type="button">
                        {rule.enabled ? "Disable" : "Enable"}
                      </button>
                      <button disabled={saving} onClick={() => remove(rule.id)} type="button">
                        Remove
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </>
      )}
    </div>
  );
}
