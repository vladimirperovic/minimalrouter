import { useCallback, useState } from "react";
import { apiFetch, responseError } from "../lib/api";
import { dnsTime, type DNSOperation } from "../lib/dnsProtection";
import { useVisiblePolling } from "../lib/useVisiblePolling";
import type { DNSProtectionControl } from "../lib/useDNSProtection";
import DNSDisclosureHeading from "./DNSDisclosureHeading";
import DNSUtilityIcon from "./DNSUtilityIcon";

function DNSOperationHistory({ control }: { control: DNSProtectionControl }) {
  const [open, setOpen] = useState(false);
  const [history, setHistory] = useState<DNSOperation[] | null>(null);
  const [error, setError] = useState("");
  const [refresh, setRefresh] = useState(0);
  const load = useCallback(async (signal: AbortSignal) => {
    try {
      const response = await apiFetch("/api/v1/dns-filter/operations", { signal, cache: "no-store" });
      if (!response.ok) throw new Error(await responseError(response, "DNS history unavailable"));
      const body = await response.json() as { operations: DNSOperation[] };
      if (!Array.isArray(body.operations)) throw new Error("DNS history unavailable on this firmware.");
      if (!signal.aborted) { setHistory(body.operations); setError(""); }
    } catch (error) { if (!signal.aborted) setError((error as Error).message); }
  }, []);
  useVisiblePolling(load, control.pending || control.status?.updating ? 3000 : 0, open, `${control.status?.operation?.updated_at}|${refresh}`);
  return <details className="dns-protection-card dns-list-details" onToggle={event => setOpen(event.currentTarget.open)}><summary className="dns-disclosure-heading"><DNSDisclosureHeading icon="history" title="Recent DNS changes" description="A record of requests, verification and outcomes" meta={control.status?.operation ? control.status.operation.state.replaceAll("_", " ") : "Last 20 operations"}/></summary>
    {error && <p role="alert" className="dns-message is-error">{error} <button className="button secondary small" type="button" onClick={() => setRefresh(value => value+1)}>Retry history</button></p>}
    {!history && !error && <p>Loading DNS history…</p>}
    {history?.length === 0 && <div className="dns-history-empty"><span className="dns-disclosure-icon"><DNSUtilityIcon kind="history"/></span><div><strong>Your recent changes will appear here</strong><p>No DNS operations recorded yet. Each new apply or refresh gets a timestamp, revision and verified outcome.</p></div></div>}
    <ol className="dns-operation-history">{history?.map(op => <li key={op.id}><span className={`dns-history-mark ${op.state === "completed" ? "is-complete" : ""}`} aria-hidden="true"><DNSUtilityIcon kind={op.state === "completed" ? "allow" : op.kind === "refresh" ? "history" : "settings"}/></span><div><strong>{op.kind === "refresh" ? "List refresh" : "Policy change"}</strong><p>{dnsTime(op.started_at)} · revision {op.base_revision}{op.applied_revision ? ` → ${op.applied_revision}` : ""}</p>{op.error && <p className="dns-history-error">{op.error}</p>}<small>{op.id.slice(0,8)} · {op.phase.replaceAll("_", " ")}</small></div><span className="dns-neutral-chip">{op.state.replaceAll("_", " ")}</span></li>)}</ol>
  </details>;
}

export default function DNSListStatus({ control }: { control: DNSProtectionControl }) {
  const { status, busy, draft } = control;
  const now = status?.router_time ? new Date(status.router_time).getTime()/1000 : Date.now()/1000;
  const cooldown = Boolean(status?.refresh_allowed_at && status.refresh_allowed_at > now);
  const appliedAny = Object.values(status?.policy.categories ?? {}).some(Boolean);
  const activeLists = status?.lists.filter(list => status.policy.categories[list.category]).length ?? 0;
  return <>
    <details className="dns-protection-card dns-list-details"><summary className="dns-disclosure-heading"><DNSDisclosureHeading icon="lists" title="Maintained lists & coverage" description="Sources, freshness and automatic refresh" meta={status ? `${activeLists} active categories` : "Awaiting status"}/></summary>
      <p className="dns-details-intro">HaGeZi public lists refresh daily for enabled categories. Your installed rules stay active when a download fails.</p>
      <div className="dns-list-grid">{status?.lists.map(list => <section key={list.category} className={status.policy.categories[list.category] ? "is-enabled" : ""}><div className="dns-list-heading"><span className="dns-source-label">HaGeZi · {list.category}</span><span className={`dns-neutral-chip ${status.policy.categories[list.category] ? "is-on" : ""}`}>{status.policy.categories[list.category] ? "Enabled" : "Off"}</span></div><h4>{list.label}</h4><strong className="dns-list-count">{list.updated_at ? list.entries.toLocaleString() : "—"}<small> source entries</small></strong><div className="dns-source-freshness"><DNSUtilityIcon kind="clock"/><span>{list.updated_at ? `Downloaded ${dnsTime(list.updated_at)}` : "Not downloaded yet"}</span></div>{(list.error || (list.updated_at > 0 && now-list.updated_at > 86400)) && <p className="dns-message">{list.error || "Older than 24 hours. The last installed version is retained."}</p>}<a href={list.url} target="_blank" rel="noreferrer">View source list <DNSUtilityIcon kind="arrow"/></a></section>)}</div>
      <div className="dns-list-refresh"><span className="dns-disclosure-icon"><DNSUtilityIcon kind="history"/></span><div><strong>Next automatic refresh</strong><p>{status?.next_refresh_at ? dnsTime(status.next_refresh_at) : "After a category is enabled"}</p>{cooldown && <small>Manual refresh available after {dnsTime(status?.refresh_allowed_at)}.</small>}</div><button className="button secondary dns-refresh-button" disabled={busy || Boolean(draft) || !appliedAny || cooldown || Boolean(status?.blockers.length)} type="button" onClick={() => void control.apply("refresh")}><DNSUtilityIcon kind="history"/>Refresh lists</button></div>
      {!appliedAny && <p className="form-note">Enable and apply a category first. Newly enabled lists download automatically.</p>}{appliedAny && draft && <p className="form-note">Apply or discard your policy changes before refreshing lists.</p>}
      <p className="dns-coverage-note">DNS filtering cannot inspect individual pages or cover external encrypted DNS, VPNs or mobile data. Local DNS records and active DHCP names take precedence. <a href="#dns-activity">DNS Activity</a> provides separate lookup monitoring and risk alerts.</p>
    </details>
    <DNSOperationHistory control={control}/>
  </>;
}
