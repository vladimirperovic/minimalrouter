import { useEffect, useRef, useState, type FormEvent, type KeyboardEvent } from "react";
import { useModalFocus } from "../lib/useModalFocus";
import { createDefaultKidsGrid, createKidsProfile, gridToDayWindows, hourCoverage, managedServices, normalizeDayWindows, paintScheduleHour, scheduleDays, type AccessWindow, type DayWindows, type DeviceProfile, type ScheduleDay } from "../lib/deviceProfiles";
import type { DNSDevice } from "../lib/dnsProtection";

type Props = { profile?: DeviceProfile; devices: DNSDevice[]; routerTime?: string; timezone?: string; onClose: () => void; onSave: (profile: DeviceProfile) => Promise<boolean> };

function WeeklySchedule({ windows, change, disabled }: { windows: DayWindows; change: (next: DayWindows) => void; disabled: boolean }) {
  const [focusSlot, setFocusSlot] = useState(0);
  const grid = useRef<HTMLDivElement>(null);
  const drag = useRef<{ allowed: boolean; last: number } | null>(null);
  const latest = useRef(windows);
  useEffect(() => { latest.current = windows; }, [windows]);
  useEffect(() => {
    const stop = () => { drag.current = null; };
    window.addEventListener("pointerup", stop); window.addEventListener("pointercancel", stop); window.addEventListener("blur", stop);
    return () => { window.removeEventListener("pointerup", stop); window.removeEventListener("pointercancel", stop); window.removeEventListener("blur", stop); };
  }, []);
  const update = (day: ScheduleDay, next: AccessWindow[]) => { const value = { ...latest.current, [day]: next }; latest.current = value; change(value); };
  const paint = (slot: number, allowed: boolean) => {
    const day = scheduleDays[Math.floor(slot/24)][0];
    update(day, paintScheduleHour(latest.current[day], slot%24, allowed));
  };
  const keydown = (event: KeyboardEvent<HTMLButtonElement>, slot: number) => {
    const moves: Record<string,number> = { ArrowLeft:-1, ArrowRight:1, ArrowUp:-24, ArrowDown:24 };
    if (event.key in moves) { event.preventDefault(); const next = Math.max(0,Math.min(167,slot+moves[event.key])); setFocusSlot(next); grid.current?.querySelector<HTMLButtonElement>(`[data-slot="${next}"]`)?.focus(); }
    if (event.key === " " || event.key === "Enter") { event.preventDefault(); paint(slot, hourCoverage(latest.current[scheduleDays[Math.floor(slot/24)][0]], slot%24) < 1); }
  };
  return <>
    <fieldset className="weekly-scheduler" disabled={disabled}><legend>Allowed time</legend>
      <div className="scheduler-toolbar"><p>Paint hours to allow access. Striped cells contain precise times. Editing one hour preserves the rest of your schedule.</p><div>
        <button className="button secondary compact" type="button" onClick={() => change(gridToDayWindows(createDefaultKidsGrid()))}>Default</button>
        <button className="button secondary compact" type="button" onClick={() => change(Object.fromEntries(scheduleDays.map(([day]) => [day,[{ start:"00:00",end:"23:59" }]])) as DayWindows)}>Allow all</button>
        <button className="button secondary compact" type="button" onClick={() => change(Object.fromEntries(scheduleDays.map(([day]) => [day,[]])) as unknown as DayWindows)}>Block all</button>
      </div></div>
      <div className="scheduler-scroll"><div ref={grid} className="scheduler-grid" onPointerMove={event => {
        if (!drag.current || disabled) return;
        const cell = document.elementFromPoint(event.clientX,event.clientY)?.closest<HTMLElement>("[data-slot]");
        if (!cell || !grid.current?.contains(cell)) return;
        const slot = Number(cell.dataset.slot);
        if (slot !== drag.current.last) { drag.current.last = slot; paint(slot,drag.current.allowed); }
      }}>
        <div className="scheduler-corner"/>{Array.from({length:24},(_,hour) => <span className="scheduler-hour" key={hour}>{String(hour).padStart(2,"0")}</span>)}
        {scheduleDays.map(([day,label],dayIndex) => <div className="scheduler-row" key={day}><div className="scheduler-day"><strong>{label}</strong><span><button type="button" aria-label={`Allow all ${label}`} onClick={() => update(day,[{start:"00:00",end:"23:59"}])}>All</button><button type="button" aria-label={`Block all ${label}`} onClick={() => update(day,[])}>None</button></span></div>{Array.from({length:24},(_,hour) => {
          const coverage = hourCoverage(windows[day],hour), slot = dayIndex*24+hour;
          return <button key={hour} type="button" data-slot={slot} tabIndex={slot === focusSlot ? 0 : -1} aria-label={`${label} ${String(hour).padStart(2,"0")}:00 ${coverage === 1 ? "allowed" : coverage > 0 ? "partially allowed" : "blocked"}`} aria-pressed={coverage === 1 ? true : coverage > 0 ? "mixed" : false} className={`scheduler-slot ${coverage === 1 ? "is-allowed" : coverage > 0 ? "is-partial" : ""}`} onFocus={() => setFocusSlot(slot)} onKeyDown={event => keydown(event,slot)} onPointerDown={event => { if (event.button !== 0) return; event.preventDefault(); event.currentTarget.focus({preventScroll:true}); event.currentTarget.setPointerCapture(event.pointerId); drag.current = {allowed:coverage<1,last:slot}; paint(slot,coverage<1); }} />;
        })}</div>)}
      </div></div>
      <p className="dns-schedule-legend"><span className="is-allowed"/> Allowed <span className="is-partial"/> Part of the hour <span/> Blocked · use arrow keys to move</p>
    </fieldset>
    <details className="dns-precise-schedule"><summary>Edit exact times (hours and minutes)</summary><p>Minute-precise changes appear in the grid. An end time of 23:59 means through midnight.</p>
      {scheduleDays.map(([day,label]) => <div className="dns-precise-day" key={day}><strong>{label}</strong><div>{windows[day].length === 0 && <p className="form-note">Blocked all day</p>}{windows[day].map((window,index) => <div className="dns-precise-window" key={index}>
        <label className="field"><span>{label} start {index+1}</span><input type="time" required disabled={disabled} value={window.start} onChange={event => update(day,windows[day].map((item,i) => i === index ? {...item,start:event.target.value} : item))}/></label>
        <label className="field"><span>{label} end {index+1}</span><input type="time" required disabled={disabled} value={window.end} onChange={event => update(day,windows[day].map((item,i) => i === index ? {...item,end:event.target.value} : item))}/></label>
        <button className="button secondary small" type="button" disabled={disabled} aria-label={`Remove ${label} window ${index+1}`} onClick={() => update(day,windows[day].filter((_,i) => i !== index))}>Remove</button>
      </div>)}<button className="button secondary small" type="button" disabled={disabled || windows[day].length >= 12} onClick={() => update(day,[...windows[day],{start:"19:00",end:"22:00"}])}>Add {label} window</button></div></div>)}
    </details>
  </>;
}

export default function DNSProfileDialog({ profile, devices, routerTime, timezone, onClose, onSave }: Props) {
  const [name,setName] = useState(profile?.name ?? "Kids");
  const [addresses,setAddresses] = useState(profile?.ip_addresses.join(", ") ?? "");
  const [services,setServices] = useState(profile?.services ?? ["youtube","steam","wiki"]);
  const [windows,setWindows] = useState<DayWindows>(() => profile ? structuredClone(normalizeDayWindows(profile.schedule)) : gridToDayWindows(createDefaultKidsGrid()));
  const [scheduleChanged,setScheduleChanged] = useState(false);
  const [error,setError] = useState("");
  const [saving,setSaving] = useState(false);
  const mounted = useRef(true);
  const ref = useModalFocus(true, () => { if (!saving) onClose(); });
  useEffect(() => { mounted.current = true; const overflow = document.body.style.overflow; document.body.style.overflow = "hidden"; return () => { mounted.current = false; document.body.style.overflow = overflow; }; }, []);
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); if (saving) return;
    setError(""); setSaving(true);
    try {
      const next = createKidsProfile({ id:profile?.id, name, addresses:addresses.split(/[\s,]+/), services, dayWindows:windows });
      next.enabled = profile?.enabled ?? true;
      if (profile && !scheduleChanged) next.schedule = structuredClone(profile.schedule);
      if (await onSave(next) && mounted.current) onClose();
    } catch(error) { if (mounted.current) setError((error as Error).message); }
    finally { if (mounted.current) setSaving(false); }
  }
  return <div className="modal-backdrop" role="presentation"><div ref={ref} className="modal-panel dns-profile-modal" role="dialog" aria-modal="true" aria-labelledby="profile-title" tabIndex={-1}>
    <div className="modal-heading"><div className="dns-profile-modal-title"><div><p className="eyebrow">Device schedules</p><h2 id="profile-title">{profile ? "Edit device profile" : "Add device profile"}</h2><p>Choose devices, services and the times when access is allowed.</p></div></div><button className="modal-close" aria-label="Close profile dialog" type="button" disabled={saving} onClick={onClose}>✕</button></div>
    <form className="dns-profile-form" onSubmit={submit}>
      <div className="dns-profile-basics"><label className="field"><span>Profile name</span><input value={name} onChange={event => setName(event.target.value)} required maxLength={64} disabled={saving}/></label><label className="field"><span>Add known device</span><select value="" disabled={saving || !devices.length} onChange={event => { const selected = new Set(addresses.split(/[\s,]+/).filter(Boolean)); selected.add(event.target.value); setAddresses([...selected].join(", ")); }}><option value="">{devices.length ? "Choose from DHCP and reservations" : "No known devices — enter an address below"}</option>{devices.map(device => <option key={device.ip} value={device.ip}>{device.name || "Unnamed device"} · {device.ip}{device.reserved ? " · reserved" : ""}</option>)}</select></label></div>
      <label className="field"><span>Device IP addresses</span><input value={addresses} onChange={event => setAddresses(event.target.value)} placeholder="192.168.1.50, 192.168.1.51" required disabled={saving}/><small>Schedules need stable LAN IPv4 addresses. Configure reservations in LAN & DHCP.</small></label>
      <fieldset className="service-picker" disabled={saving}><legend>Managed services</legend><p>These services follow the allowed times below.</p><div className="service-checkboxes">{managedServices.map(([id,label]) => <label key={id}><input type="checkbox" checked={services.includes(id)} onChange={() => setServices(current => current.includes(id) ? current.filter(item => item !== id) : [...current,id])}/>{label}</label>)}</div></fieldset>
      <p className="dns-router-clock">Router time: <strong>{routerTime || "Unavailable"}</strong> · {timezone || "Timezone unavailable"}. The schedule uses the router clock.</p>
      <WeeklySchedule windows={windows} change={value => { setWindows(value); setScheduleChanged(true); }} disabled={saving}/>
      <p className="form-note">Runtime enforcement is not measured per profile. External encrypted DNS or VPNs can bypass DNS-derived service matching.</p>
      <div className="modal-actions dns-dialog-footer">{error && <p role="alert" className="dns-message is-error">{error}</p>}<div><button className="button secondary" type="button" disabled={saving} onClick={onClose}>Cancel</button><button className="button primary" type="submit" disabled={saving}>{saving ? "Applying…" : "Save profile"}</button></div></div>
    </form>
  </div></div>;
}
