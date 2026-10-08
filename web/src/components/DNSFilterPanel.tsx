import { FormEvent, useEffect, useRef, useState } from "react";
import type { RouterConfig } from "../api-types";
import NetworkDNSProtection from "./NetworkDNSProtection";
import { previewAndApplyConfig, readConfiguration, useConfiguration } from "../lib/configuration";
import {
  createDefaultKidsGrid,
  createEmptyGrid,
  createKidsProfile,
  describeSchedule,
  DeviceProfile,
  DayWindows,
  gridToDayWindows,
  HourGrid,
  managedServices,
  normalizeDayWindows,
  scheduleDays,
  ScheduleDay,
} from "../lib/deviceProfiles";

type Props = {
  apiConnected: boolean;
  onError: (message: string) => void;
};

export default function DNSFilterPanel({ apiConnected, onError }: Props) {
  const config = useConfiguration();
  const enabled = Boolean(config?.adguard.enabled);
  const profiles = (config?.adguard.device_profiles || []) as DeviceProfile[];
  const [modalOpen, setModalOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const [name, setName] = useState("Kids");
  const [addresses, setAddresses] = useState("");
  const [services, setServices] = useState<string[]>(["youtube", "steam", "wiki"]);
  const [grid, setGrid] = useState<HourGrid>(() => createDefaultKidsGrid());
  const [editingId, setEditingId] = useState<string | null>(null);
  const editBase = useRef<RouterConfig | null>(null);
  const [scheduleChanged, setScheduleChanged] = useState(false);
  const [preciseWindows, setPreciseWindows] = useState<DayWindows | null>(null);
  const dragValue = useRef<boolean | null>(null);

  const gridFromProfile = (profile: DeviceProfile): HourGrid => {
    const windows = normalizeDayWindows(profile.schedule);
    return Object.fromEntries(scheduleDays.map(([day]) => {
      const slots = Array<boolean>(24).fill(false);
      for (const item of windows[day] ?? []) {
        const from = Number(item.start.slice(0, 2));
        const to = item.end === "23:59" ? 24 : Number(item.end.slice(0, 2));
        for (let hour = from; hour < Math.min(to, 24); hour += 1) slots[hour] = true;
      }
      return [day, slots];
    })) as unknown as HourGrid;
  };

  useEffect(() => {
    const stopDrag = () => { dragValue.current = null; };
    window.addEventListener("pointerup", stopDrag);
    return () => window.removeEventListener("pointerup", stopDrag);
  }, []);

  const persist = async (nextEnabled: boolean, nextProfiles: DeviceProfile[], base = config) => {
    if (!apiConnected) throw new Error("Router API is unavailable.");
    if (!base) throw new Error("Reload the configuration before saving.");
    // Keep the revision that produced these profiles. A fresh revision with
    // stale profiles would bypass the server's concurrent-edit protection.
    const candidate = structuredClone(base);
    candidate.adguard = {
      ...candidate.adguard,
      enabled: nextEnabled,
      filter_devices: [],
      device_profiles: nextProfiles,
    };
    const result = await previewAndApplyConfig(candidate);
    return !result.cancelled;
  };

  const saveResolvers = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    const resolvers = String(form.get("resolvers") || "").split(/[\s,]+/).filter(Boolean);
    setSaving(true);
    try {
      const next = await readConfiguration({ cache: "reload" });
      next.dhcp.dns_servers = resolvers;
      const result = await previewAndApplyConfig(next);
      if (!result.cancelled) onError("");
    } catch (error) { onError(error instanceof Error ? error.message : "DNS update failed"); }
    finally { setSaving(false); }
  };

  const closeModal = () => {
    setModalOpen(false);
    setEditingId(null);
    setName("Kids");
    setAddresses("");
    setServices(["youtube", "steam", "wiki"]);
    setGrid(createDefaultKidsGrid());
  };

  const openAdd = () => {
    setPreciseWindows(null);
    editBase.current = config ? structuredClone(config) : null;
    setScheduleChanged(false);
    setEditingId(null);
    setName("Kids");
    setAddresses("");
    setServices(["youtube", "steam", "wiki"]);
    setGrid(createDefaultKidsGrid());
    setModalOpen(true);
  };

  const startEditProfile = (profile: DeviceProfile) => {
    setPreciseWindows(structuredClone(normalizeDayWindows(profile.schedule)));
    editBase.current = config ? structuredClone(config) : null;
    setScheduleChanged(false);
    setEditingId(profile.id);
    setName(profile.name);
    setAddresses(profile.ip_addresses.join(", "));
    setServices([...profile.services]);
    setGrid(gridFromProfile(profile));
    setModalOpen(true);
  };

  const toggleGlobal = async () => {
    setSaving(true);
    try {
      await persist(!enabled, profiles);
      onError("");
    } catch (error) {
      onError(error instanceof Error ? error.message : "DNS Filter update failed");
    } finally {
      setSaving(false);
    }
  };

  const submitProfile = async (event: FormEvent) => {
    event.preventDefault();
    setSaving(true);
    try {
      const base = editBase.current;
      if (!base) throw new Error("Reload the configuration before saving.");
      const baseProfiles = (base.adguard.device_profiles || []) as DeviceProfile[];
      const existing = baseProfiles.find((item) => item.id === editingId);
      const profile = createKidsProfile({
        id: editingId ?? undefined,
        name,
        addresses: addresses.split(","),
        services,
        dayWindows: existing && !scheduleChanged ? normalizeDayWindows(existing.schedule) : preciseWindows ?? gridToDayWindows(grid),
      });
      if (existing && !scheduleChanged) profile.schedule = structuredClone(existing.schedule);
      if (editingId) {
        if (!await persist(base.adguard.enabled, baseProfiles.map((item) => (
          item.id === editingId ? { ...profile, enabled: existing?.enabled ?? true } : item
        )), base)) return;
      } else {
        if (!await persist(base.adguard.enabled, [...baseProfiles, profile], base)) return;
      }
      closeModal();
      onError("");
    } catch (error) {
      onError(error instanceof Error ? error.message : "Profile could not be saved");
    } finally {
      setSaving(false);
    }
  };

  const toggleProfile = async (id: string) => {
    setSaving(true);
    try {
      const next = profiles.map((profile) => profile.id === id ? { ...profile, enabled: !profile.enabled } : profile);
      await persist(enabled, next);
      onError("");
    } catch (error) {
      onError(error instanceof Error ? error.message : "Profile update failed");
    } finally {
      setSaving(false);
    }
  };

  const removeProfile = async (id: string) => {
    setSaving(true);
    try {
      await persist(enabled, profiles.filter((profile) => profile.id !== id));
      onError("");
    } catch (error) {
      onError(error instanceof Error ? error.message : "Profile removal failed");
    } finally {
      setSaving(false);
    }
  };

  const toggleService = (service: string) => {
    setServices((current) => current.includes(service)
      ? current.filter((item) => item !== service)
      : [...current, service]);
  };

  const setHour = (day: ScheduleDay, hour: number, value: boolean) => {
    setPreciseWindows(null);
    setScheduleChanged(true);
    setGrid((current) => ({
      ...current,
      [day]: current[day].map((slot, index) => index === hour ? value : slot),
    }));
  };

  const startPaint = (day: ScheduleDay, hour: number) => {
    const next = !grid[day][hour];
    dragValue.current = next;
    setHour(day, hour, next);
  };

  const paint = (day: ScheduleDay, hour: number) => {
    if (dragValue.current !== null) setHour(day, hour, dragValue.current);
  };

  const setDay = (day: ScheduleDay, value: boolean) => {
    setPreciseWindows(null);
    setScheduleChanged(true);
    setGrid((current) => ({ ...current, [day]: Array(24).fill(value) }));
  };

  return (
    <section className="section-block dns-filter" id="adguard">
      <div className="section-heading dns-filter-heading has-facts">
        <div className="subpage-hero-head"><div><p className="eyebrow">DNS Filter & Device Profiles</p><h2>DNS blocking & scheduled access</h2><p className="dns-filter-intro">Choose categories to block across the network, check domains, and manage optional schedules for devices with reserved addresses.</p></div><div className="dns-filter-actions"><button className="button primary" disabled={!apiConnected || saving} onClick={openAdd} type="button">Add device profile</button></div></div>
        <dl className="subpage-hero-facts"><div><dt>Bundled filter</dt><dd>{enabled ? "Enabled" : "Disabled"}</dd><small>built-in list and device schedules</small></div><div><dt>Profiles</dt><dd>{profiles.length}</dd><small>configured devices</small></div><div><dt>Enabled profiles</dt><dd>{profiles.filter((profile) => profile.enabled).length}</dd><small>scheduled policies</small></div><div><dt>Services</dt><dd>{new Set(profiles.flatMap((profile) => profile.services)).size}</dd><small>unique service groups</small></div></dl>
      </div>

      <NetworkDNSProtection apiConnected={apiConnected} />
      <details className="dns-optional-section"><summary>Advanced DNS & bundled protection</summary>
      <div className="dns-settings-columns">
      <section className="settings-column-card" aria-label="DNS configuration"><header><h2>Configuration</h2><p>Set the resolvers used by your network.</p></header><form className="settings-form" key={(config?.dhcp.dns_servers || []).join(",")} onSubmit={saveResolvers}><label className="field"><span>Upstream DNS resolvers</span><textarea name="resolvers" rows={4} required defaultValue={(config?.dhcp.dns_servers || []).join("\n")} placeholder="1.1.1.1\n9.9.9.9"/><small>One IP address per line. All configured resolvers are retained.</small></label><button className="button primary" disabled={!apiConnected || saving} type="submit">{saving ? "Applying…" : "Save changes"}</button></form><p className="settings-safety-note">Resolver changes use the same validation and connectivity safeguards as LAN settings.</p></section>
      <section className="settings-column-card" aria-label="DNS preferences"><header><h2>Preferences</h2><p>Control network-wide DNS blocking and device schedules.</p></header><div className="dns-preference-row"><div><strong>DNS filtering</strong><p>Block the built-in ad and tracker list and apply enabled device schedules. The blocking list ships with firmware; it does not refresh separately.</p></div><button className="button secondary" disabled={!apiConnected || saving} type="button" onClick={toggleGlobal}>{enabled ? "Disable DNS Filter" : "Enable DNS Filter"}</button></div><div className="dns-preference-row"><div><strong>Device profiles</strong><p>{profiles.length} configured · {profiles.filter(p => p.enabled).length} enabled. Each profile retains its devices, services and full weekly schedule.</p></div><button className="button primary" disabled={!apiConnected || saving} onClick={openAdd} type="button">Create a device profile</button></div><p className="settings-safety-note">DNS Activity separately monitors risky domains using updated category lists. Its alerts and notification exceptions do not change blocking rules.</p></section>
      </div>
      </details>

      <details className="dns-optional-section" open={profiles.length > 0}><summary>Optional device schedules · {profiles.length} profiles</summary>
      <article className="card table-card dns-profile-table">
        <div className="card-title-row">
          <div>
            <h3>Device profiles</h3>
            <p>Schedules require stable IPv4 addresses. Reserve each address in LAN & DHCP. Enabled policies are scheduled; runtime enforcement is not measured per profile.</p>
          </div>
        </div>
        <div className="elegant-table-container">
          <table className="elegant-device-table">
            <caption className="sr-only">DNS Filter device profiles</caption>
            <colgroup><col className="elegant-col-name" /><col className="elegant-col-mac" /><col className="elegant-col-ip" /><col className="elegant-col-expires" /><col className="elegant-col-w120" /><col className="elegant-col-actions" /></colgroup>
            <thead>
              <tr><th>Profile</th><th>Devices</th><th>Services</th><th>Schedule</th><th>Status</th><th className="elegant-th-actions">Action</th></tr>
            </thead>
            <tbody>
              {profiles.length === 0 ? (
                <tr><td className="empty-state dns-profile-empty-cell" colSpan={6}><div className="dns-profile-empty"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><path d="M4 5h16M7 12h10M10 19h4" /><circle cx="12" cy="12" r="9" /></svg><strong>No device profiles yet</strong><span>Create a profile to schedule service access for selected devices.</span><button className="button secondary" disabled={!apiConnected || saving} onClick={openAdd} type="button">Create first profile</button></div></td></tr>
              ) : profiles.map((profile) => (
                <tr key={profile.id}>
                  <td className="elegant-cell-name" data-label="Profile"><strong>{profile.name}</strong></td>
                  <td className="elegant-cell-ip" data-label="Devices"><code>{profile.ip_addresses.join(", ")}</code><small>{profile.ip_addresses.every(ip => config?.dhcp.static_leases?.some(lease => lease.ip_address === ip)) ? "DHCP reservations configured" : "Check DHCP reservations"}</small></td>
                  <td data-label="Services"><div className="service-tags">{profile.services.map((service) => <span key={service}>{service}</span>)}</div></td>
                  <td data-label="Schedule">{describeSchedule(profile)}</td>
                  <td data-label="Status">
                    <button className={`status-pill ${enabled && profile.enabled ? "is-active" : ""}`} disabled={saving || !apiConnected} onClick={() => toggleProfile(profile.id)} type="button">
                      {!profile.enabled ? "Paused" : enabled ? "Scheduled" : "Filter off"}
                    </button>
                  </td>
                  <td className="elegant-cell-actions"><div className="device-row-actions"><button className="button secondary small" disabled={saving} onClick={() => startEditProfile(profile)} type="button">Edit</button><button className="icon-danger" disabled={saving} onClick={() => removeProfile(profile.id)} title="Remove profile" type="button">✕</button></div></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </article>
      </details>

      {modalOpen && (
        <div className="modal-backdrop" role="presentation">
          <section aria-labelledby="profile-title" aria-modal="true" className="modal-panel dns-profile-modal" role="dialog">
            <div className="modal-heading">
              <div className="dns-profile-modal-title"><span aria-hidden="true"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round"><path d="M12 3 4 7v5c0 4.4 3 7.3 8 9 5-1.7 8-4.6 8-9V7z" /><path d="M9 12h6M12 9v6" /></svg></span><div><p className="eyebrow">Parental control</p><h2 id="profile-title">{editingId ? "Edit device profile" : "Add device profile"}</h2><p>Choose the devices, managed services and the hours when access is allowed.</p></div></div>
              <button aria-label="Close profile dialog" className="modal-close" onClick={closeModal} type="button">✕</button>
            </div>
            <form className="form-grid dns-profile-form" onSubmit={submitProfile}>
              <div className="dns-profile-basics">
                  <label className="field"><span>Profile name</span><input onChange={(event) => setName(event.target.value)} placeholder="Kids" required value={name} /></label>
                  <label className="field"><span>Device IP addresses</span><input onChange={(event) => setAddresses(event.target.value)} placeholder="192.168.1.50, 192.168.1.51" required value={addresses} /></label>
              </div>
                  <fieldset className="field service-picker" aria-labelledby="dns-filter-services-title">
                    <div className="fieldset-title" id="dns-filter-services-title">Managed services</div>
                    <p>Select the services controlled by this schedule.</p>
                    <div className="service-checkboxes">
                      {managedServices.map(([value, label]) => (
                        <label key={value}><input checked={services.includes(value)} onChange={() => toggleService(value)} type="checkbox" />{label}</label>
                      ))}
                    </div>
                  </fieldset>

                  <fieldset className="weekly-scheduler" aria-labelledby="dns-filter-schedule-title">
                    <div className="fieldset-title" id="dns-filter-schedule-title">Allowed time</div>
                    <div className="scheduler-toolbar">
                      <p>Coloured hours are allowed. Changing this grid replaces the schedule with whole-hour slots. Existing minute-precise times are preserved until you change the grid.</p>
                      <div>
                        <button className="button secondary compact" onClick={() => { setPreciseWindows(null); setScheduleChanged(true); setGrid(createDefaultKidsGrid()); }} type="button">Default</button>
                        <button className="button secondary compact" onClick={() => { setPreciseWindows(null); setScheduleChanged(true); setGrid(Object.fromEntries(scheduleDays.map(([day]) => [day, Array(24).fill(true)])) as HourGrid); }} type="button">Allow all</button>
                        <button className="button secondary compact" onClick={() => { setPreciseWindows(null); setScheduleChanged(true); setGrid(createEmptyGrid()); }} type="button">Block all</button>
                      </div>
                    </div>
                    <div className="scheduler-scroll">
                      <div className="scheduler-grid">
                        <div className="scheduler-corner" />
                        {Array.from({ length: 24 }, (_, hour) => <span className="scheduler-hour" key={hour}>{String(hour).padStart(2, "0")}</span>)}
                        {scheduleDays.map(([day, label]) => (
                          <div className="scheduler-row" key={day}>
                            <div className="scheduler-day">
                              <strong>{label}</strong>
                              <span>
                                <button aria-label={`Allow all ${label}`} onClick={() => setDay(day, true)} type="button">All</button>
                                <button aria-label={`Block all ${label}`} onClick={() => setDay(day, false)} type="button">None</button>
                              </span>
                            </div>
                            {grid[day].map((allowed, hour) => (
                              <button
                                aria-label={`${label} ${String(hour).padStart(2, "0")}:00 ${allowed ? "allowed" : "blocked"}`}
                                aria-pressed={allowed}
                                className={`scheduler-slot ${allowed ? "is-allowed" : ""}`}
                                key={hour}
                                onPointerDown={(event) => { event.preventDefault(); startPaint(day, hour); }}
                                onPointerEnter={() => paint(day, hour)}
                                onKeyDown={event => { if (event.key === " " || event.key === "Enter") { event.preventDefault(); setHour(day, hour, !allowed); } }}
                                type="button"
                              />
                            ))}
                          </div>
                        ))}
                      </div>
                    </div>
                  </fieldset>
                  <details className="dns-precise-schedule"><summary>Edit exact times (hours and minutes)</summary><p>These windows replace the hourly grid when edited. An end time of 23:59 means through midnight.</p>{scheduleDays.map(([day, label]) => {
                    const windows = (preciseWindows ?? gridToDayWindows(grid))[day];
                    const update = (next: typeof windows) => { setScheduleChanged(true); setPreciseWindows(current => ({ ...(current ?? gridToDayWindows(grid)), [day]: next })); };
                    return <div className="dns-precise-day" key={day}><strong>{label}</strong>{windows.map((window, index) => <div className="dns-precise-window" key={index}><label className="field"><span>{label} start {index + 1}</span><input type="time" value={window.start} required onChange={event => update(windows.map((w, i) => i === index ? { ...w, start: event.target.value } : w))} /></label><label className="field"><span>{label} end {index + 1}</span><input type="time" value={window.end} required onChange={event => update(windows.map((w, i) => i === index ? { ...w, end: event.target.value } : w))} /></label><button className="button secondary small" type="button" aria-label={`Remove ${label} window ${index + 1}`} onClick={() => update(windows.filter((_, i) => i !== index))}>Remove</button></div>)}<button className="button secondary small" disabled={windows.length >= 8} type="button" onClick={() => update([...windows, { start: "19:00", end: "22:00" }])}>Add {label} window</button></div>;
                  })}</details>
                  <p className="form-note">By default YouTube, Steam and Wikipedia are allowed on weekdays from 19:00, and all day at weekends. You can change any hour on any day.</p>
              <div className="modal-actions"><button className="button secondary" onClick={closeModal} type="button">Cancel</button><button className="button primary" disabled={saving} type="submit">{saving ? "Applying…" : "Save profile"}</button></div>
            </form>
          </section>
        </div>
      )}
    </section>
  );
}
