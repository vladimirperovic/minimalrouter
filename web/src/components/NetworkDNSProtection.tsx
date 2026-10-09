import { useState, type FormEvent, type ReactNode } from "react";
import type { DNSProtectionControl } from "../lib/useDNSProtection";
import { dnsCategories, dnsTime, dnsClockZone, normalizedException, type DNSDevice } from "../lib/dnsProtection";
import DNSDomainChecker from "./DNSDomainChecker";
import DNSListStatus from "./DNSListStatus";
import DNSUtilityIcon from "./DNSUtilityIcon";

export function DNSIcon({ kind = "shield" }: { kind?: string }) {
  const paths: Record<string, ReactNode> = {
    shield: <><path d="m12 3 8 4v5c0 4-3 7-8 9-5-2-8-5-8-9V7z"/><path d="m8.5 12 2.5 2.5 4.5-5"/></>,
    eye: <><path d="M3 12s3-6 9-6 9 6 9 6-3 6-9 6-9-6-9-6Z"/><circle cx="12" cy="12" r="2.5"/><path d="m4 20 16-16"/></>,
    lock: <><rect x="5" y="10" width="14" height="11" rx="3"/><path d="M8 10V7a4 4 0 0 1 8 0v3M12 14v3"/></>,
    dice: <><rect x="3" y="3" width="18" height="18" rx="5"/><path d="M8 8h.01M16 8h.01M12 12h.01M8 16h.01M16 16h.01"/></>,
  };
  return <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">{paths[kind] ?? paths.shield}</svg>;
}

function DNSOverview({ control }: { control: DNSProtectionControl }) {
  const { status, statusError, observedAt } = control;
  const active = status ? dnsCategories.filter(category => status.policy.categories[category.id]).length : null;
  const devices = status ? new Set(status.profiles.filter(profile => !["paused", "filter_off"].includes(profile.state)).flatMap(profile => profile.ip_addresses)).size : null;
  const label = statusError ? "Status unavailable" : !status ? "Checking protection" : status.updating ? "Updating protection" : status.health_checking ? "Checking resolver" : status.healthy ? "Resolver verified" : status.applied_at ? "Verification needed" : "Ready to configure";
  return <article className="dns-protection-card dns-overview" aria-label="Protection overview">
    <div className="dns-overview-lead"><span className={`dns-overview-symbol ${status?.healthy && !statusError ? "is-healthy" : ""}`}><DNSIcon /></span><div><p className="eyebrow">Protection at a glance</p><h3>{label}</h3><p>{statusError ? `Showing the last known policy${observedAt ? ` from ${new Date(observedAt).toLocaleTimeString()}` : " when available"}.` : "Your network rules, resolver and device schedules in one place."}</p></div><button className="button secondary small" type="button" onClick={control.refresh} disabled={control.saving}>Refresh status</button></div>
    <dl className="dns-overview-metrics">
      <div><dt>Active categories</dt><dd>{active === null ? "—" : <>{active}<small> / 4</small></>}</dd><span>Applied network policy</span></div>
      <div><dt>Scheduled devices</dt><dd>{devices ?? "—"}</dd><span>Configured · enforcement unverified</span></div>
      <div><dt>Last successful apply</dt><dd className="dns-overview-date">{status ? dnsTime(status.applied_at) : "—"}</dd><span>{status ? `Policy revision ${status.policy.revision}` : "Waiting for router"}</span></div>
      <div><dt>Router clock</dt><dd className="dns-overview-date">{status?.router_time ? status.router_time.slice(11,19) : "—"}</dd><span>{status?.router_time ? dnsClockZone(status.router_time, status.timezone) : "Timezone unavailable"}</span></div>
    </dl>
    {statusError && <p role="alert" className="dns-message is-error">{statusError} Use Refresh status to check again. An unavailable status does not confirm a completed change.</p>}
  </article>;
}

function OperationNotice({ control }: { control: DNSProtectionControl }) {
  const { status, pending, notice, error } = control;
  const op = pending?.id && status?.operation?.id !== pending.id ? undefined : status?.operation;
  const running = Boolean(pending || status?.updating);
  const phases: Record<string, string> = { queued: "Queued", preparing: "Checking current policy", downloading: "Preparing lists", applying: "Applying and verifying resolver", verified: "Resolver verified", interrupted: "Interrupted; review needed", outcome_unknown: "Checking outcome", recovery_required: "Recovery required" };
  if (!running && !notice && !error && !status?.error) return null;
  return <div className={`dns-operation-banner ${error || status?.error ? "has-error" : ""}`}>
    <span className={`dns-operation-dot ${running ? "is-running" : ""}`} aria-hidden="true" />
    <div><strong>{running ? phases[op?.phase ?? ""] ?? "Verifying the requested change" : error || status?.error ? "DNS change needs attention" : "Protection updated"}</strong>
      <p role={error || status?.error ? "alert" : "status"}>{error || status?.error || (running && control.statusError ? "The request was accepted, but its outcome is not confirmed. Your changes are retained." : notice || "Existing protection remains in place until verification completes.")}</p>
      {op && <small>Operation {op.id.slice(0, 8)} · {dnsTime(op.started_at)}</small>}
    </div>
    {control.statusError && <button className="button secondary small" onClick={control.refresh} type="button">Check again</button>}
  </div>;
}

function CategoryChoices({ control }: { control: DNSProtectionControl }) {
  const { policy, draft, busy, conflict, status } = control;
  const blocked = Boolean(status?.blockers.length);
  return <article className="dns-protection-card" aria-label="Network protection">
    <header><div><p className="eyebrow">For every device using router DNS</p><h3>Network protection</h3><p>Choose what to filter. Review your selection, then apply it.</p></div><span className="dns-neutral-chip">{draft ? "Unapplied changes" : "Applied policy"}</span></header>
    <div className="dns-category-list">{dnsCategories.map(category => {
      const checked = Boolean(policy?.categories[category.id]);
      return <label key={category.id} className={`dns-category-choice ${checked ? "is-selected" : ""}`}><span className="dns-category-symbol"><DNSIcon kind={category.icon}/></span><span className="dns-category-copy"><strong>{category.label}</strong><small>{category.detail}</small><span>{category.source}</span></span><span className="dns-toggle"><input type="checkbox" aria-label={category.label} checked={checked} disabled={busy || blocked} onChange={event => policy && control.edit({ ...policy, categories: { ...policy.categories, [category.id]: event.target.checked } })}/><span aria-hidden="true"/></span></label>;
    })}</div>
    <div className="dns-policy-save"><div><strong>{conflict ? "A newer policy is available" : draft ? "Your selection is ready to apply" : "Changes stay under your control"}</strong><p>{conflict ? "Another session changed this policy. Discard your draft to load the current version before editing again." : draft ? "You have unapplied category or exception changes. They stay here when you visit another page." : status ? `${status.domains.toLocaleString()} source entries in the applied policy; lists may overlap.` : "Waiting for applied policy information."}</p></div><div>{draft && <button className="button secondary" type="button" disabled={busy} onClick={control.discard}>Discard changes</button>}<button className="button primary" type="button" disabled={busy || !draft || conflict || blocked} onClick={() => void control.apply("policy")}>Apply protection</button></div></div>
    {status?.blockers.map(blocker => <p className="dns-message" key={blocker}>{blocker}</p>)}
  </article>;
}

function ExceptionsEditor({ control }: { control: DNSProtectionControl }) {
  const [error, setError] = useState("");
  const [search, setSearch] = useState("");
  const [limit, setLimit] = useState(20);
  const policy = control.policy;
  const exceptions = policy?.exceptions ?? [];
  const visible = exceptions.filter(entry => `${entry.domain} ${entry.reason ?? ""}`.toLowerCase().includes(search.toLowerCase()));
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault(); if (!policy || control.busy) return;
    const form = new FormData(event.currentTarget);
    try {
      const domain = normalizedException(String(form.get("domain") ?? ""));
      if (exceptions.some(entry => entry.domain === domain)) throw new Error("An exception for this domain already exists.");
      if (exceptions.length >= 200) throw new Error("The limit is 200 exceptions.");
      control.edit({ ...policy, exceptions: [...exceptions, { domain, reason: String(form.get("reason") ?? "").trim() }] });
      setError(""); event.currentTarget.reset();
    } catch (error) { setError((error as Error).message); }
  };
  return <article className="dns-protection-card dns-utility-card dns-exceptions-card" aria-label="Blocking exceptions">
    <header className="dns-tool-header"><div className="dns-tool-topline"><span className="dns-tool-symbol"><DNSUtilityIcon kind="allow"/></span><span className="dns-tool-badge"><strong>{exceptions.length}</strong> / 200 exceptions</span></div><div><p className="eyebrow">Keep the sites you need</p><h3>Blocking exceptions</h3><p>Give a domain and its subdomains a pass through your DNS blocking lists.</p></div></header>
    <form className="dns-exception-form" onSubmit={submit}>
      <label className="field"><span>Domain to allow</span><span className="dns-tool-input"><DNSUtilityIcon kind="globe"/><input name="domain" required maxLength={253} placeholder="school.example.com" disabled={control.busy} /></span></label>
      <label className="field"><span>Reason (optional)</span><span className="dns-tool-input"><DNSUtilityIcon kind="note"/><input name="reason" maxLength={200} placeholder="e.g. School resources" disabled={control.busy}/></span></label>
      {error && <p role="alert" className="dns-message is-error">{error}</p>}
      <div className="dns-tool-action"><span>Add to your policy draft</span><button className="button secondary dns-tool-button" type="submit" disabled={control.busy || exceptions.length >= 200}><DNSUtilityIcon kind="plus"/>Add exception</button></div>
    </form>
    {exceptions.length > 0 && <label className="field dns-inline-search"><span className="sr-only">Search exceptions</span><input value={search} onChange={event => { setSearch(event.target.value); setLimit(20); }} placeholder="Search domains or reasons" /></label>}
    <ul className="dns-exception-list">{visible.slice(0, limit).map(entry => <li key={entry.domain}><span><strong>{entry.domain}</strong><small>{entry.reason || "Domain and subdomains"}</small></span><button className="button secondary small" type="button" disabled={control.busy} aria-label={`Remove exception ${entry.domain}`} onClick={() => policy && control.edit({ ...policy, exceptions: exceptions.filter(item => item.domain !== entry.domain) })}>Remove</button></li>)}</ul>
    {visible.length > limit && <button className="button secondary small" type="button" onClick={() => setLimit(value => value+20)}>Show more exceptions ({visible.length-limit})</button>}
    {!visible.length && <div className="dns-tool-empty"><span className="dns-tool-empty-icon"><DNSUtilityIcon kind="allow"/></span><div><strong>{exceptions.length ? "No matching exceptions" : "No exceptions yet"}</strong><p>{exceptions.length ? "Try a different domain or reason." : "Allowed domains will appear here. Your selected categories apply as usual."}</p></div></div>}
    <div className="dns-tool-note"><DNSUtilityIcon kind="note"/><div><strong>Apply when you’re ready</strong><p>Save with <strong>Apply protection</strong> above. Device schedules and DNS Activity notifications remain separate.</p></div></div>
  </article>;
}

export default function NetworkDNSProtection({ control, devices, configRevision }: { control: DNSProtectionControl; devices: DNSDevice[]; configRevision?: number }) {
  return <div className="network-dns-protection">
    <DNSOverview control={control}/>
    <OperationNotice control={control}/>
    <CategoryChoices control={control}/>
    <div className="dns-protection-columns"><DNSDomainChecker status={control.status} devices={devices} configRevision={configRevision} disabled={control.busy} refresh={control.refresh}/><ExceptionsEditor control={control}/></div>
    <DNSListStatus control={control}/>
  </div>;
}
