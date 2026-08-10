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
    <section className="classic-live-card classic-security-card classic-port-forwards" aria-labelledby="tunnel-forwards-title">
      <header className="port-forward-heading">
        <div className="port-forward-title">
          <span aria-hidden="true"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round"><path d="M4 7h10M10 3l4 4-4 4M20 17H10M14 13l-4 4 4 4" /></svg></span>
          <div><p className="eyebrow">WireGuard-only routing</p><h3 id="tunnel-forwards-title">Tunnel port forwards</h3><p>Send traffic arriving on the router&apos;s tunnel address to a service on the LAN. These rules are never exposed to WAN.</p></div>
        </div>
        <span className="port-forward-count">{rules.filter((rule) => rule.enabled).length} active</span>
      </header>

      {!loaded ? (
        <div className="port-forward-empty">Loading tunnel rules…</div>
      ) : (
        <>
          <form className="port-forward-composer" onSubmit={add}>
            <div className="port-forward-fields">
              <label className="field port-forward-name"><span>Rule name</span><input onChange={(event) => setName(event.target.value)} placeholder="OpenCode" value={name} /></label>
              <label className="field port-forward-protocol"><span>Protocol</span><select onChange={(event) => setProtocol(event.target.value)} value={protocol}><option value="tcp">TCP</option><option value="udp">UDP</option><option value="both">TCP + UDP</option></select></label>
              <label className="field"><span>Tunnel port</span><input inputMode="numeric" onChange={(event) => setExternalPort(event.target.value)} placeholder="4080" value={externalPort} /></label>
              <span className="port-forward-arrow" aria-hidden="true"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round"><path d="M5 12h14M14 7l5 5-5 5" /></svg></span>
              <label className="field port-forward-address"><span>LAN destination</span><input onChange={(event) => setInternalIP(event.target.value)} placeholder="192.168.1.50" value={internalIP} /></label>
              <label className="field"><span>Service port</span><input inputMode="numeric" onChange={(event) => setInternalPort(event.target.value)} placeholder="4080" value={internalPort} /></label>
            </div>
            <div className="port-forward-submit"><p><strong>Route preview</strong><span>10.8.0.1:{externalPort || "port"} → {internalIP || "LAN device"}:{internalPort || "port"}</span></p><button className="modal-action-button" disabled={saving} type="submit"><svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round"><path d="M12 5v14M5 12h14" /></svg>{saving ? "Applying…" : "Add tunnel rule"}</button></div>
          </form>

          {rules.length === 0 ? (
            <div className="port-forward-empty"><span aria-hidden="true"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round"><rect x="4" y="5" width="16" height="14" rx="3" /><path d="M8 12h8" /></svg></span><div><strong>No tunnel rules configured</strong><p>Remote WireGuard devices can reach only the services already allowed by the firewall.</p></div></div>
          ) : (
            <div className="port-forward-rules" aria-label="Configured tunnel port forwards">
              {rules.map((rule) => (
                <article className={rule.enabled ? "is-enabled" : ""} key={rule.id}>
                  <div className="port-forward-rule-name"><i aria-hidden="true" /><span><strong>{rule.name}</strong><small>{rule.enabled ? "Forwarding active" : "Rule paused"}</small></span></div>
                  <code>{rule.protocol.toUpperCase()}</code>
                  <div className="port-forward-route"><span>10.8.0.1:{rule.external_port}</span><svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round"><path d="M5 12h14M14 7l5 5-5 5" /></svg><strong>{rule.internal_ip}:{rule.internal_port}</strong></div>
                  <div className="port-forward-actions"><button disabled={saving} onClick={() => toggle(rule.id)} type="button">{rule.enabled ? "Pause" : "Enable"}</button><button className="is-remove" disabled={saving} onClick={() => remove(rule.id)} type="button" aria-label={`Remove ${rule.name}`} title={`Remove ${rule.name}`}><svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round"><path d="M4 7h16M9 7V4h6v3M6.5 7l.7 13h9.6l.7-13M10 11v5M14 11v5" /></svg></button></div>
                </article>
              ))}
            </div>
          )}
        </>
      )}
    </section>
  );
}
