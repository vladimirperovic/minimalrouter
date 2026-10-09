import { describeSchedule, managedServices, normalizeDayWindows, scheduleDays, type DeviceProfile } from "../lib/deviceProfiles";
import { dnsScheduleTime, type DNSProfileStatus } from "../lib/dnsProtection";
import DNSUtilityIcon from "./DNSUtilityIcon";

type Props = { profile: DeviceProfile; status?: DNSProfileStatus; routerTime?: string; enabled: boolean; reserved: boolean; canEdit: boolean; onEdit: () => void; onToggle: () => void; onRemove: () => void };
export default function DNSProfileCard({ profile, status, routerTime, enabled, reserved, canEdit, onEdit, onToggle, onRemove }: Props) {
  const windows = normalizeDayWindows(profile.schedule);
  const dayIndex = routerTime ? (new Date(`${routerTime.slice(0,10)}T12:00:00Z`).getUTCDay()+6)%7 : -1;
  const day = scheduleDays[dayIndex];
  const today = day ? windows[day[0]] : undefined;
  const times = today?.map(window => `${window.start}–${window.end}`).join(", ");
  const label = !profile.enabled ? "Paused" : !enabled ? "Filter off" : status?.state === "allowed" ? "Allowed now" : status?.state === "blocked" ? "Blocked now" : "Scheduled";
  const tone = profile.enabled && enabled ? status?.state === "allowed" ? "is-allowed" : status?.state === "blocked" ? "is-blocked" : "" : "";
  const minute = (time: string) => Number(time.slice(0,2))*60+Number(time.slice(3,5));
  return <section className="dns-device-card" aria-label={profile.name}>
    <div className="dns-device-heading"><span className="dns-device-avatar"><DNSUtilityIcon kind="device"/></span><div><strong>{profile.name}</strong><code>{profile.ip_addresses.join(", ")}</code></div><span className={`dns-device-state ${tone}`}><span/>{label}</span></div>
    <div className="dns-device-services"><span>Managed services</span><div className="service-tags">{profile.services.map(service => <span key={service}>{managedServices.find(([id]) => id === service)?.[1] ?? service}</span>)}</div></div>
    <div className="dns-device-schedule"><div className="dns-device-today"><span><DNSUtilityIcon kind="clock"/>{day ? `${day[1]} · allowed times` : "Weekly allowed times"}</span><strong>{today ? times || "Blocked all day" : "Router clock unavailable"}</strong></div>
      <div className="dns-week-preview" role="img" aria-label={describeSchedule(profile)}>{scheduleDays.map(([key,label],index) => <div key={key} className={index === dayIndex ? "is-today" : ""} aria-hidden="true" title={`${label}: ${windows[key].map(window => `${window.start}–${window.end}`).join(", ") || "Blocked all day"}`}><span>{label}</span><svg viewBox="0 0 1440 6" preserveAspectRatio="none">{windows[key].map((window,index) => <rect key={index} x={minute(window.start)} y={0} width={(window.end === "23:59" ? 1440 : minute(window.end))-minute(window.start)} height={6}/>)}</svg></div>)}</div>
      <details className="dns-device-full-week"><summary>View exact weekly times</summary><p>{describeSchedule(profile)}</p></details>
      {status?.next_change_at && <p className="dns-device-next"><span className="dns-next-dot"/>Next {status.next_state === "allowed" ? "access window" : "scheduled block"}: {dnsScheduleTime(status.next_change_at)}</p>}
    </div>
    <div className="dns-device-reservation"><DNSUtilityIcon kind={reserved ? "allow" : "note"}/>{reserved ? <span>DHCP reservation configured</span> : <a href="#network">Set a DHCP reservation ↗</a>}</div>
    <div className="dns-device-actions"><button className="dns-pause-button" type="button" disabled={!canEdit} aria-label={`${profile.enabled ? "Pause" : "Resume"} profile ${profile.name}`} onClick={onToggle}><DNSUtilityIcon kind={profile.enabled ? "pause" : "play"}/>{profile.enabled ? "Pause" : "Resume"}</button><div><button className="button secondary small" type="button" disabled={!canEdit} onClick={onEdit}>Edit</button><button className="button secondary small dns-remove-profile" type="button" disabled={!canEdit} aria-label={`Remove profile ${profile.name}`} onClick={onRemove}>Remove</button></div></div>
  </section>;
}
