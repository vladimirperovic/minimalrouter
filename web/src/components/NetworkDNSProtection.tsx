import { FormEvent, useCallback, useState } from "react";
import { apiFetch, responseError } from "../lib/api";
import { useVisiblePolling } from "../lib/useVisiblePolling";
import type { DNSFilterPolicy } from "../api-types";

type List = { category: string; label: string; url: string; entries: number; updated_at: number; error?: string };
type Status = { policy: DNSFilterPolicy; domains: number; applied_at: number; healthy: boolean; lists: List[]; updating: boolean; error?: string; next_refresh_at: number; router_time: string; timezone: string };
type Check = { domain: string; action: string; exception: boolean; healthy: boolean; matches: { category: string; domain: string; enabled: boolean }[] };
const categories = [
  ["threats", "Malware, phishing & scams", "Known malicious domains · HaGeZi TIF Mini"],
  ["ads", "Ads & trackers", "A compact list with lower risk of app breakage · HaGeZi Light"],
  ["adult", "Adult content", "Domains hosting adult content · HaGeZi NSFW"],
  ["gambling", "Gambling", "Betting and gambling domains · HaGeZi Gambling Mini"],
];
const when = (epoch: number) => epoch ? new Date(epoch * 1000).toLocaleString() : "Not downloaded";

export default function NetworkDNSProtection({ apiConnected }: { apiConnected: boolean }) {
  const [status, setStatus] = useState<Status | null>(null);
  const [draft, setDraft] = useState<DNSFilterPolicy | null>(null);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [working, setWorking] = useState(false);
  const [refresh, setRefresh] = useState(0);
  const [check, setCheck] = useState<Check | null>(null);
  const load = useCallback(async (signal: AbortSignal) => {
    try {
      const response = await apiFetch("/api/v1/dns-filter", { signal });
      if (!response.ok) throw new Error(await responseError(response, "DNS filter status unavailable"));
      const next = await response.json() as Status;
      if (!next.policy || !Array.isArray(next.lists)) throw new Error("DNS filter status unavailable");
      if (!signal.aborted) { setStatus(next); setError(""); }
    } catch (e) { if (!signal.aborted) { setError(e instanceof Error ? e.message : "DNS filter unavailable"); setStatus(null); } }
  }, []);
  useVisiblePolling(load, status?.updating ? 3000 : 30000, apiConnected, String(refresh));
  const policy = draft ?? status?.policy;
  const busy = working || Boolean(status?.updating) || !apiConnected || !status;
  const changedElsewhere = draft && status && draft.revision !== status.policy.revision;
  const appliedAny = Object.values(status?.policy.categories ?? {}).some(Boolean);
  const mutate = async (path: string, method: string, body?: unknown) => {
    setWorking(true); setError(""); setNotice("");
    try {
      const response = await apiFetch(`/api/v1/dns-filter${path}`, { method, headers: { "Content-Type": "application/json" }, body: body === undefined ? undefined : JSON.stringify(body) });
      if (!response.ok) throw new Error(await responseError(response, "DNS filter change failed"));
      setDraft(null); setStatus(current => current ? { ...current, updating: true } : current);
      setNotice("Update requested. Existing protection stays in place while lists are prepared and the new resolver configuration is verified.");
      setRefresh(value => value + 1);
    } catch (e) { setError(e instanceof Error ? e.message : "DNS filter change failed"); }
    finally { setWorking(false); }
  };
  const addException = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault(); if (!policy) return;
    const form = new FormData(event.currentTarget);
    const domain = String(form.get("domain") ?? "").trim().toLowerCase().replace(/\.$/, "");
    if (policy.exceptions.some(e => e.domain === domain)) { setError("An exception for this domain already exists."); return; }
    setDraft({ ...policy, exceptions: [...policy.exceptions, { domain, reason: String(form.get("reason") ?? "").trim() }] });
    event.currentTarget.reset();
  };
  const checkDomain = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault(); const domain = String(new FormData(event.currentTarget).get("check_domain") ?? "");
    setWorking(true); setCheck(null);
    try {
      const response = await apiFetch("/api/v1/dns-filter/check", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ domain }) });
      if (!response.ok) throw new Error(await responseError(response, "Domain check failed"));
      setCheck(await response.json() as Check); setError("");
    } catch (e) { setError(e instanceof Error ? e.message : "Domain check failed"); }
    finally { setWorking(false); }
  };

  return <div className="network-dns-protection">
    <article className="dns-protection-card" aria-label="Network protection">
      <header><div><p className="eyebrow">Every device using this router's DNS</p><h3>Network protection</h3><p>Category blocking works across changing IP and MAC addresses.</p></div><div className="dns-protection-actions"><span className={`classic-status-chip ${status?.healthy ? "" : "is-off"}`}>{!status ? "Unavailable" : status.updating ? "Updating" : status.healthy ? "Resolver verified" : status.applied_at ? "Resolver unverified" : "Not configured"}</span><button className="button primary small" disabled={busy || !draft || Boolean(changedElsewhere)} type="button" onClick={() => void mutate("", "PUT", draft)}>Apply protection</button></div></header>
      {error && <p role="alert" className="dns-policy-message">{error}</p>}
      {status?.error && <p role="alert" className="dns-policy-message">{status.error}</p>}
      {notice && !status?.error && <p role="status" className="dns-policy-message">{status?.updating ? notice : "Update finished. Review the applied categories and resolver status below."}</p>}
      <div className="dns-category-list">{categories.map(([id, label, detail]) => <label key={id} className="dns-category-choice"><span><strong>{label}</strong><small>{detail}</small></span><span><input type="checkbox" checked={Boolean(policy?.categories[id])} disabled={busy} onChange={e => policy && setDraft({ ...policy, categories: { ...policy.categories, [id]: e.target.checked } })} /> Block</span></label>)}</div>
      <p className="form-note">New categories are off until you apply them. DNS Activity provides separate risk alerts; notification exceptions there do not allow blocked sites. <a href="#dns-activity">Open DNS Activity →</a></p>
      <p className="form-note">DNS filtering cannot inspect pages inside a site or cover traffic using an external encrypted resolver, VPN or mobile data. Local DNS records and DHCP names take precedence.</p>
    </article>

    <div className="dns-protection-columns">
      <article className="dns-protection-card"><header><div><h3>Check a domain</h3><p>Check the applied lists locally, without sending the domain to a third party.</p></div></header><form className="dns-domain-form" onSubmit={checkDomain}><label className="field"><span>Domain name</span><input name="check_domain" placeholder="example.com" required maxLength={253} /></label><button className="button secondary" disabled={busy} type="submit">Check domain</button></form>
        {check && <div className="dns-domain-result" role="status"><strong>{check.action}: {check.domain}</strong><p>{check.healthy ? "Resolver generation verified." : "Resolver enforcement is unverified."} This is a policy check, not proof of a blocked query or a visit.</p>{check.matches.map((match, index) => <p key={index}>{match.category} · <code>{match.domain}</code> · {match.enabled ? "blocking enabled" : "category off"}</p>)}</div>}
      </article>
      <article className="dns-protection-card"><header><div><h3>Blocking exceptions</h3><p>Allow a domain and all its subdomains. Use this if filtering breaks a site or app.</p></div></header>
        <form className="dns-exception-form" onSubmit={addException}><label className="field"><span>Domain to allow</span><input name="domain" required maxLength={253} placeholder="example.com" /></label><label className="field"><span>Reason (optional)</span><input name="reason" maxLength={200} placeholder="Needed for school" /></label><button className="button secondary" type="submit" disabled={busy || (policy?.exceptions.length ?? 0) >= 200}>Add exception</button></form>
        <ul className="dns-exception-list">{policy?.exceptions.map(exception => <li key={exception.domain}><span><strong>{exception.domain}</strong><small>{exception.reason || "Domain and subdomains"}</small></span><button className="button secondary small" disabled={busy} type="button" aria-label={`Remove exception ${exception.domain}`} onClick={() => setDraft({ ...policy, exceptions: policy.exceptions.filter(e => e.domain !== exception.domain) })}>Remove</button></li>)}</ul>
        {!policy?.exceptions.length && <p className="form-note">No blocking exceptions.</p>}
      </article>
    </div>
    <div className="dns-policy-save"><p>{changedElsewhere ? "Policy changed in another session. Reload before saving." : draft ? "You have unapplied category or exception changes." : `${status?.domains.toLocaleString() ?? "0"} source entries in the applied policy; lists may overlap.`}</p><div>{draft && <button className="button secondary" type="button" disabled={working} onClick={() => setDraft(null)}>Discard changes</button>}</div></div>
    <details className="dns-protection-card dns-list-details"><summary>Maintained lists & coverage</summary><p>HaGeZi public lists · downloaded daily for enabled categories · previous lists retained on failure. Compact lists balance coverage and appliance memory. DNS Activity uses a separate catalog for alerts.</p><div className="dns-list-grid">{status?.lists.map(list => <section key={list.category}><h4>{list.label}</h4><p>{list.entries.toLocaleString()} entries</p><p>Last successful download: {when(list.updated_at)}</p><p>{list.error || (list.updated_at && Date.now() / 1000 - list.updated_at > 172800 ? "List is stale; last working version retained." : "")}</p><a href={list.url} target="_blank" rel="noreferrer">Source list ↗</a></section>)}</div><p>Next automatic refresh: {status?.next_refresh_at ? when(status.next_refresh_at) : "After a category is enabled"}</p><button className="button secondary" disabled={busy || Boolean(draft) || !appliedAny} type="button" onClick={() => void mutate("/refresh", "POST")}>Refresh lists</button>{!appliedAny && <p className="form-note">Enable and apply a category above first — there is nothing to refresh yet. Lists for newly checked categories download automatically when you apply.</p>}{appliedAny && draft && <p className="form-note">Apply or discard the changes above first — refresh covers the applied policy.</p>}<p className="form-note">Router clock: {status?.router_time || "Unavailable"} · {status?.timezone || "Timezone unavailable"}</p></details>
  </div>;
}
