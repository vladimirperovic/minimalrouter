import { useEffect, useRef, useState, type FormEvent } from "react";
import { apiFetch, responseError } from "../lib/api";
import { dnsTime, dnsScheduleTime, type DNSDevice, type DNSDomainCheck, type DNSStatus } from "../lib/dnsProtection";
import DNSUtilityIcon from "./DNSUtilityIcon";

export default function DNSDomainChecker({ status, devices, configRevision, disabled, refresh }: { status: DNSStatus | null; devices: DNSDevice[]; configRevision?: number; disabled: boolean; refresh: () => void }) {
  const [domain, setDomain] = useState("");
  const [device, setDevice] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [result, setResult] = useState<{ key: string; data: DNSDomainCheck } | null>(null);
  const request = useRef<AbortController | null>(null);
  const key = `${domain.trim().toLowerCase()}|${device}|${status?.policy.revision}|${configRevision}`;
  const currentKey = useRef(key);
  useEffect(() => {
    currentKey.current = key;
    request.current?.abort(); request.current = null; setBusy(false);
  }, [key]);
  useEffect(() => {
    const hide = () => { if (document.hidden) { request.current?.abort(); request.current = null; setBusy(false); } };
    document.addEventListener("visibilitychange", hide);
    return () => { request.current?.abort(); request.current = null; document.removeEventListener("visibilitychange", hide); };
  }, []);
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); request.current?.abort();
    const controller = new AbortController(); request.current = controller;
    const owns = () => request.current === controller && !controller.signal.aborted && currentKey.current === key;
    setBusy(true); setResult(null); setError("");
    try {
      const params = new URLSearchParams({ domain: domain.trim() });
      if (device) params.set("device_ip", device);
      const response = await apiFetch(`/api/v1/dns-filter/check?${params}`, { signal: controller.signal, cache: "no-store" });
      if (!response.ok) throw new Error(await responseError(response, "Domain check failed"));
      const data = await response.json() as DNSDomainCheck;
      if (!owns()) return;
      if (!data || typeof data.action !== "string" || !Array.isArray(data.matches)) throw new Error("Domain check returned an invalid response.");
      if ((data.policy_revision !== undefined && data.policy_revision !== status?.policy.revision) || (data.config_revision !== undefined && data.config_revision !== configRevision)) { refresh(); throw new Error("The policy changed during this check. Review the updated status and check again."); }
      setResult({ key, data });
    } catch (error) { if (owns()) setError((error as Error).message); }
    finally { if (owns()) { request.current = null; setBusy(false); } }
  }
  const check = result?.key === key ? result.data : null;
  return <article className="dns-protection-card dns-utility-card dns-inspector-card" aria-label="Check a domain">
    <header className="dns-tool-header"><div className="dns-tool-topline"><span className="dns-tool-symbol"><DNSUtilityIcon kind="inspect"/></span><span className="dns-tool-badge"><span/>Local lookup</span></div><div><p className="eyebrow">Understand a decision</p><h3>Check a domain</h3><p>Find the rule behind a blocked site, for your network or a specific device.</p></div></header>
    <form className="dns-domain-form" onSubmit={submit}>
      <label className="field"><span>Domain name</span><span className="dns-tool-input"><DNSUtilityIcon kind="globe"/><input name="check_domain" value={domain} onChange={event => { setDomain(event.target.value); setError(""); }} placeholder="example.com" required maxLength={253}/></span></label>
      <label className="field"><span>Device context (optional)</span><span className="dns-tool-input"><DNSUtilityIcon kind="device"/><input list="dns-check-devices" value={device} onChange={event => { setDevice(event.target.value); setError(""); }} placeholder="All devices · optional IPv4" maxLength={15}/></span><datalist id="dns-check-devices">{devices.map(item => <option key={item.ip} value={item.ip}>{item.name || "Unnamed device"}{item.reserved ? " · reserved" : ""}</option>)}</datalist></label>
      <div className="dns-tool-action"><span>{status ? `Applied policy · rev ${status.policy.revision}` : "Waiting for router"}</span><button className="button secondary dns-tool-button" type="submit" disabled={disabled || busy}>{busy ? "Checking domain…" : "Check domain"}<DNSUtilityIcon kind="arrow"/></button></div>
    </form>
    {error && <p role="alert" className="dns-message is-error">{error}</p>}
    {!check && !error && <div className="dns-tool-empty"><span className={`dns-tool-empty-icon ${busy ? "is-checking" : ""}`}><DNSUtilityIcon kind="inspect"/></span><div><strong>{busy ? "Looking up matching rules" : "Ready to inspect"}</strong><p>{busy ? "Checking the applied policy on your router." : "Enter a domain to see matches, exceptions and the reason behind its policy."}</p></div></div>}
    {check && <div className="dns-domain-result" role="status"><span className="eyebrow">Configured result</span><strong>{check.action}: {check.domain}</strong>
      <p>{check.healthy ? "Resolver generation verified." : "Resolver enforcement is unverified."} This is a policy check, not proof of a blocked query or a visit.</p>
      <ul>{check.layers?.map((layer, index) => <li key={index}><span className="dns-neutral-chip">{layer.source.replaceAll("_", " ")}</span><span>{layer.reason}</span></li>) ?? check.matches.map((match, index) => <li key={index}>{match.category} · {match.domain} · {match.enabled ? "blocking enabled" : "category off"}</li>)}</ul>
      {check.profile && <p>Profile: {check.profile.name} · {check.profile.state.replaceAll("_", " ")}{check.profile.next_change_at ? ` · next change ${dnsScheduleTime(check.profile.next_change_at)}` : ""}. Runtime enforcement is unverified.</p>}
      <small>{dnsTime(check.checked_at)} · policy {check.policy_revision ?? status?.policy.revision} · configuration {check.config_revision ?? configRevision}</small>
    </div>}
    <div className="dns-tool-note"><DNSUtilityIcon kind="lock"/><div><strong>Private by design</strong><p>Checked on your router. No domain is sent to a third party. Results describe policy, including local DNS overrides.</p></div></div>
  </article>;
}
