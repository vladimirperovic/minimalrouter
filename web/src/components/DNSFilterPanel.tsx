import { useEffect, useRef, useState, type FormEvent } from "react";
import type { RouterConfig } from "../api-types";
import { previewAndApplyConfig, useConfiguration } from "../lib/configuration";
import type { DeviceProfile } from "../lib/deviceProfiles";
import { dnsClockZone, type DNSDevice } from "../lib/dnsProtection";
import { useDNSProtection } from "../lib/useDNSProtection";
import NetworkDNSProtection from "./NetworkDNSProtection";
import DNSProfileDialog from "./DNSProfileDialog";
import DNSProfileCard from "./DNSProfileCard";
import DNSDisclosureHeading from "./DNSDisclosureHeading";
import DNSUtilityIcon from "./DNSUtilityIcon";

type Props = { apiConnected: boolean; onError: (message: string) => void; leases?: { ip_address: string; hostname?: string }[] };
type Editor = { base: RouterConfig; profile?: DeviceProfile };

export default function DNSFilterPanel({ apiConnected, leases = [] }: Props) {
  const config = useConfiguration();
  const control = useDNSProtection(apiConnected);
  const [editor, setEditor] = useState<Editor | null>(null);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [search, setSearch] = useState("");
  const [resolvers, setResolvers] = useState<string | null>(null);
  const [resolverError, setResolverError] = useState("");
  const resolverBase = useRef<RouterConfig | null>(null);
  const configRevision = useRef(config?.revision);
  useEffect(() => {
    if (configRevision.current !== config?.revision) { configRevision.current = config?.revision; control.refresh(); }
  }, [config?.revision, control.refresh]);
  const profiles = (config?.adguard.device_profiles ?? []) as DeviceProfile[];
  const enabled = Boolean(config?.adguard.enabled);
  const deviceMap = new Map<string,DNSDevice>(control.status?.devices.map(device => [device.ip,device]));
  for (const lease of leases) if (!deviceMap.has(lease.ip_address)) deviceMap.set(lease.ip_address,{ip:lease.ip_address,name:lease.hostname ?? "",reserved:false});
  for (const lease of config?.dhcp.static_leases ?? []) deviceMap.set(lease.ip_address,{ip:lease.ip_address,name:lease.hostname,reserved:true});
  const devices = [...deviceMap.values()].sort((a,b) => (a.name || a.ip).localeCompare(b.name || b.ip));
  const canEdit = apiConnected && Boolean(config) && !saving;
  function open(profile?: DeviceProfile) { if (config && canEdit) { setError(""); setEditor({base:structuredClone(config),profile}); } }

  async function persist(base: RouterConfig, nextEnabled: boolean, nextProfiles: DeviceProfile[]) {
    const candidate = structuredClone(base);
    candidate.adguard = {...candidate.adguard,enabled:nextEnabled,filter_devices:[],device_profiles:nextProfiles};
    const result = await previewAndApplyConfig(candidate);
    return !result.cancelled;
  }
  async function saveProfile(profile: DeviceProfile) {
    if (!editor) return false;
    setSaving(true);
    try {
      const current = (editor.base.adguard.device_profiles ?? []) as DeviceProfile[];
      const next = editor.profile ? current.map(item => item.id === profile.id ? profile : item) : [...current,profile];
      return await persist(editor.base,editor.base.adguard.enabled,next);
    } finally { setSaving(false); }
  }
  async function changeProfiles(nextEnabled: boolean, nextProfiles: DeviceProfile[]) {
    if (!config || !canEdit) return;
    setSaving(true); setError("");
    try { await persist(config,nextEnabled,nextProfiles); }
    catch(error) { setError((error as Error).message); }
    finally { setSaving(false); }
  }
  async function saveResolvers(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); if (!config || !canEdit) return;
    setSaving(true); setResolverError("");
    try {
      const candidate = structuredClone(resolverBase.current ?? config);
      candidate.dhcp.dns_servers = (resolvers ?? config.dhcp.dns_servers.join("\n")).split(/[\s,]+/).filter(Boolean);
      const result = await previewAndApplyConfig(candidate);
      if (!result.cancelled) { setResolvers(null); resolverBase.current = null; }
    } catch(error) { setResolverError((error as Error).message); }
    finally { setSaving(false); }
  }
  const visibleProfiles = profiles.filter(profile => `${profile.name} ${profile.ip_addresses.join(" ")} ${profile.services.join(" ")}`.toLowerCase().includes(search.toLowerCase()));
  return <section className="section-block dns-filter" id="adguard">
    <div className="section-heading dns-filter-heading"><div><p className="eyebrow">DNS Filter & device profiles</p><h2>DNS protection & schedules</h2><p className="dns-filter-intro">Manage network filtering, understand a blocked site and set device schedules.</p></div><button className="button primary" disabled={!canEdit || profiles.length >= 64} type="button" onClick={() => open()}>Add device profile</button></div>
    <NetworkDNSProtection control={control} devices={devices} configRevision={config?.revision}/>

    <article className="dns-protection-card dns-profiles-card" aria-label="Device profiles">
      <header><div className="dns-section-lead"><span className="dns-disclosure-icon"><DNSUtilityIcon kind="device"/></span><div><p className="eyebrow">A schedule for each device</p><h3>Device profiles</h3><p>Give each device its own time for the services that matter.</p></div></div><span className="dns-neutral-chip">{profiles.length} / 64 profiles</span></header>
      <div className="dns-profiles-toolbar"><label className="field"><span className="sr-only">Search device profiles</span><span className="dns-tool-input"><DNSUtilityIcon kind="inspect"/><input value={search} onChange={event => setSearch(event.target.value)} placeholder="Search profiles, addresses or services"/></span></label><span className={`dns-neutral-chip ${enabled ? "is-on" : ""}`}>{enabled ? "Schedules enabled" : "Bundled filter & schedules off"}</span></div>
      {!enabled && profiles.length > 0 && <p className="dns-message">Your profiles are saved. Enable bundled protection in Advanced DNS below to activate device schedules. Network categories are independent.</p>}
      {error && <p className="dns-message is-error" role="alert">{error}</p>}
      <div className="dns-profile-list">{visibleProfiles.map(profile => {
        const planned = control.status?.config_revision === config?.revision ? control.status?.profiles.find(item => item.id === profile.id) : undefined;
        const reserved = profile.ip_addresses.every(ip => deviceMap.get(ip)?.reserved);
        return <DNSProfileCard key={profile.id} profile={profile} status={planned} routerTime={control.status?.router_time} enabled={enabled} reserved={reserved} canEdit={canEdit} onEdit={() => open(profile)} onToggle={() => void changeProfiles(enabled,profiles.map(item => item.id === profile.id ? {...item,enabled:!item.enabled} : item))} onRemove={() => void changeProfiles(enabled,profiles.filter(item => item.id !== profile.id))}/>;
      })}</div>
      {!visibleProfiles.length && <div className="dns-profiles-empty"><strong>{profiles.length ? "No matching profiles" : "Make room for offline time"}</strong><p>{profiles.length ? "Try a different name, service or address." : "Choose a device and the hours when selected services are available."}</p>{!profiles.length && <button className="button secondary" disabled={!canEdit} type="button" onClick={() => open()}>Create first profile</button>}</div>}
      <div className="dns-profile-coverage"><DNSUtilityIcon kind="clock"/><div><strong>Router clock: {control.status?.router_time ? `${control.status.router_time.slice(11,19)} · ${dnsClockZone(control.status.router_time,control.status.timezone)}` : "Unavailable"}</strong><p>Times show configured policy. Per-profile runtime enforcement is unverified. Addresses need DHCP reservations.</p></div></div>
    </article>

    <details className="dns-optional-section"><summary className="dns-disclosure-heading"><DNSDisclosureHeading icon="settings" title="Advanced DNS & bundled protection" description="Upstream resolvers and the built-in protection layer" meta={enabled ? "Bundled protection on" : "Bundled protection off"}/></summary><div className="dns-settings-columns">
      <section className="settings-column-card" aria-label="DNS configuration"><header className="dns-settings-heading"><span className="dns-disclosure-icon"><DNSUtilityIcon kind="globe"/></span><div><h3>Upstream resolvers</h3><p>Where the router looks up a domain.</p></div></header><form className="settings-form" onSubmit={saveResolvers}>
        <label className="field"><span>Upstream DNS resolvers</span><textarea name="resolvers" rows={4} required value={resolvers ?? config?.dhcp.dns_servers.join("\n") ?? ""} onChange={event => { if (!resolverBase.current && config) resolverBase.current = structuredClone(config); setResolvers(event.target.value); }} placeholder={"1.1.1.1\n9.9.9.9"}/><small>One IP address per line. Concurrent changes require a fresh review.</small></label>
        <p className="dns-resolver-note"><DNSUtilityIcon kind="allow"/>Resolver changes use the same validation and connectivity safeguards as LAN settings.</p>
        {resolverError && <p className="dns-message is-error" role="alert">{resolverError}</p>}<div className="dns-form-actions">{resolvers !== null && <button className="button secondary" type="button" disabled={saving} onClick={() => { setResolvers(null); resolverBase.current = null; setResolverError(""); }}>Discard resolver changes</button>}<button className="button primary" disabled={!canEdit || resolvers === null} type="submit">{saving ? "Applying…" : "Save changes"}</button></div>
      </form></section>
      <section className="settings-column-card" aria-label="DNS preferences"><header className="dns-settings-heading"><span className="dns-disclosure-icon"><DNSUtilityIcon kind="allow"/></span><div><h3>Bundled protection</h3><p>Built in and ready to use.</p></div></header><div className="dns-bundled-body"><div className="dns-bundled-status"><span className={`dns-device-state ${enabled ? "is-allowed" : ""}`}><span/>{enabled ? "Enabled" : "Disabled"}</span><small>Included with firmware</small></div><ul><li><DNSUtilityIcon kind="allow"/><span><strong>Built-in ad & tracker list</strong><small>A small list that ships with your router.</small></span></li><li><DNSUtilityIcon kind="clock"/><span><strong>Device schedules</strong><small>Activates the enabled profiles above.</small></span></li></ul><p>The four maintained network categories work independently. The bundled list updates with firmware.</p></div><div className="dns-bundled-footer"><button className="button secondary" disabled={!canEdit} type="button" onClick={() => void changeProfiles(!enabled,profiles)}>{enabled ? "Disable bundled protection" : "Enable bundled protection"}</button></div></section>
    </div></details>
    {editor && <DNSProfileDialog profile={editor.profile} devices={devices} routerTime={control.status?.router_time} timezone={control.status?.timezone} onClose={() => setEditor(null)} onSave={saveProfile}/>}
  </section>;
}
