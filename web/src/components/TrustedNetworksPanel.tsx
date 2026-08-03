import { FormEvent, useEffect, useState } from "react";
import { apiFetch } from "../lib/api";
import type { RouterConfig } from "../api-types";

type Props = {
  onError: (message: string) => void;
};

const IPV4_RE = /^(\d{1,3}\.){3}\d{1,3}\/\d{1,2}$/;
const IPV6_RE = /^[0-9a-fA-F:]{2,39}\/\d{1,3}$/;

function isValidCidr(value: string): boolean {
  if (IPV4_RE.test(value)) {
    const [octets, prefix] = [value.split("/")[0].split("."), Number(value.split("/")[1])];
    return prefix <= 32 && octets.every((octet) => Number(octet) <= 255);
  }
  if (IPV6_RE.test(value)) {
    const [address, prefix] = [value.split("/")[0], Number(value.split("/")[1])];
    if (prefix > 128) return false;
    if (address.includes("::")) return /^([0-9a-fA-F]{1,4}:)*::([0-9a-fA-F]{1,4}:)*[0-9a-fA-F]{0,4}$/.test(address) || address === "::";
    return address.split(":").length === 8 && address.split(":").every((group) => /^[0-9a-fA-F]{1,4}$/.test(group));
  }
  return false;
}

export default function TrustedNetworksPanel({ onError }: Props) {
  const [networks, setNetworks] = useState<string[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [saving, setSaving] = useState(false);
  const [input, setInput] = useState("");

  useEffect(() => {
    void load();
  }, []);

  const load = async () => {
    try {
      const response = await apiFetch("/api/v1/config");
      if (!response.ok) throw new Error(`Configuration load failed (${response.status})`);
      const config = (await response.json()) as RouterConfig;
      setNetworks(Array.isArray(config.trusted_networks) ? config.trusted_networks : []);
      setLoaded(true);
    } catch (error) {
      onError(error instanceof Error ? error.message : "Trusted networks configuration unavailable");
    }
  };

  const persist = async (next: string[]) => {
    setSaving(true);
    try {
      const currentResponse = await apiFetch("/api/v1/config");
      if (!currentResponse.ok) throw new Error(`Configuration load failed (${currentResponse.status})`);
      const config = (await currentResponse.json()) as RouterConfig;
      config.trusted_networks = next;
      const response = await apiFetch("/api/v1/config", {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(config),
      });
      const body = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(body.error || `Trusted networks apply failed (${response.status})`);
      setNetworks(next);
      onError("");
    } catch (error) {
      onError(error instanceof Error ? error.message : "Trusted networks update failed");
    } finally {
      setSaving(false);
    }
  };

  const add = async (event: FormEvent) => {
    event.preventDefault();
    const value = input.trim();
    if (!isValidCidr(value)) {
      onError(`"${value}" is not a valid CIDR network (e.g. 192.168.1.0/24).`);
      return;
    }
    if (networks.includes(value)) {
      onError(`${value} is already on the trusted list.`);
      return;
    }
    await persist([...networks, value]);
    setInput("");
  };

  const remove = async (network: string) => {
    await persist(networks.filter((item) => item !== network));
  };

  return (
    <div className="classic-live-card classic-security-card">
      <h3>Trusted networks</h3>
      <p className="classic-security-intro">
        Only devices connecting from these networks can access the MinimalRouter administration interface.
        This does not replace the router password — both layers apply.
      </p>

      {!loaded ? (
        <p className="classic-security-empty">Loading…</p>
      ) : (
        <form className="form-grid" onSubmit={add}>
          <label className="field">
            <span>Network (CIDR)</span>
            <input
              onChange={(event) => setInput(event.target.value)}
              placeholder="192.168.1.0/24"
              value={input}
            />
          </label>
          <div className="modal-actions">
            <button className="button secondary" disabled={saving} type="submit">Add network</button>
          </div>
        </form>
      )}

      <table className="classic-security-table">
        <thead><tr><th>Trusted network</th><th>Action</th></tr></thead>
        <tbody>
          {!loaded && <tr><td colSpan={2} className="empty-state">Loading…</td></tr>}
          {loaded && networks.length === 0 && <tr><td colSpan={2} className="empty-state">No trusted networks configured — administration is only reachable from localhost.</td></tr>}
          {loaded && networks.map((network) => (
            <tr key={network}>
              <td><code>{network}</code></td>
              <td><button className="icon-danger" disabled={saving} onClick={() => remove(network)} title="Remove network" type="button">✕</button></td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
