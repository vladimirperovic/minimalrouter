import { FormEvent, useEffect, useRef, useState } from "react";
import { apiFetch } from "../lib/api";
import {
  createDefaultKidsGrid,
  createEmptyGrid,
  createKidsProfile,
  describeSchedule,
  DeviceProfile,
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
  const [enabled, setEnabled] = useState(false);
  const [profiles, setProfiles] = useState<DeviceProfile[]>([]);
  const [modalOpen, setModalOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const [name, setName] = useState("Kids");
  const [addresses, setAddresses] = useState("");
  const [services, setServices] = useState<string[]>(["youtube", "steam", "wiki"]);
  const [grid, setGrid] = useState<HourGrid>(() => createDefaultKidsGrid());
  const [editingId, setEditingId] = useState<string | null>(null);
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
    if (!apiConnected) return;
    void apiFetch("/api/v1/config")
      .then(async (response) => {
        if (!response.ok) throw new Error(`Configuration load failed (${response.status})`);
        return response.json();
      })
      .then((config) => {
        setEnabled(Boolean(config.adguard?.enabled));
        setProfiles(Array.isArray(config.adguard?.device_profiles) ? config.adguard.device_profiles : []);
      })
      .catch((error) => onError(error instanceof Error ? error.message : "DNS Filter configuration unavailable"));
  }, [apiConnected, onError]);

  useEffect(() => {
    const stopDrag = () => { dragValue.current = null; };
    window.addEventListener("pointerup", stopDrag);
    return () => window.removeEventListener("pointerup", stopDrag);
  }, []);

  const persist = async (nextEnabled: boolean, nextProfiles: DeviceProfile[]) => {
    if (!apiConnected) throw new Error("Router API is unavailable.");
    const currentResponse = await apiFetch("/api/v1/config");
    if (!currentResponse.ok) throw new Error(`Configuration load failed (${currentResponse.status})`);
    const config = await currentResponse.json();
    config.adguard = {
      ...config.adguard,
      enabled: nextEnabled,
      filter_devices: [],
      device_profiles: nextProfiles,
    };
    const response = await apiFetch("/api/v1/config", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(config),
    });
    const body = await response.json().catch(() => ({}));
    if (!response.ok) throw new Error(body.error || `DNS Filter apply failed (${response.status})`);
    setEnabled(nextEnabled);
    setProfiles(nextProfiles);
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
    setEditingId(null);
    setName("Kids");
    setAddresses("");
    setServices(["youtube", "steam", "wiki"]);
    setGrid(createDefaultKidsGrid());
    setModalOpen(true);
  };

  const startEditProfile = (profile: DeviceProfile) => {
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
      const profile = createKidsProfile({
        id: editingId ?? undefined,
        name,
        addresses: addresses.split(","),
        services,
        dayWindows: gridToDayWindows(grid),
      });
      if (editingId) {
        const existing = profiles.find((item) => item.id === editingId);
        await persist(true, profiles.map((item) => (
          item.id === editingId ? { ...profile, enabled: existing?.enabled ?? true } : item
        )));
      } else {
        await persist(true, [...profiles, profile]);
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
    setGrid((current) => ({ ...current, [day]: Array(24).fill(value) }));
  };

  return (
    <section className="section-block dns-filter" id="adguard">
      <div className="section-heading dns-filter-heading has-facts">
        <div className="subpage-hero-head"><div><p className="eyebrow">DNS Filter & Device Profiles</p><h2>Scheduled service access</h2><p className="dns-filter-intro">Devices use static LAN addresses. DNS answers populate nftables sets, and the firewall applies service schedules per device.</p></div><div className="dns-filter-actions"><button className="button secondary" disabled={!apiConnected || saving} onClick={toggleGlobal} type="button">{enabled ? "Disable DNS Filter" : "Enable DNS Filter"}</button><button className="button primary" disabled={!apiConnected || saving} onClick={openAdd} type="button">Add device profile</button></div></div>
        <dl className="subpage-hero-facts"><div><dt>Filtering</dt><dd>{enabled ? "Active" : "Disabled"}</dd><small>DNS and firewall policy</small></div><div><dt>Profiles</dt><dd>{profiles.length}</dd><small>configured devices</small></div><div><dt>Active profiles</dt><dd>{profiles.filter((profile) => profile.enabled).length}</dd><small>scheduled policies</small></div><div><dt>Services</dt><dd>{new Set(profiles.flatMap((profile) => profile.services)).size}</dd><small>unique service groups</small></div></dl>
      </div>

      <article className="card table-card">
        <div className="card-title-row">
          <div>
            <h3>Device profiles</h3>
            <p>For a Kids profile you choose the allowed hours separately for each day of the week.</p>
          </div>
        </div>
        <div className="elegant-table-container">
          <table className="elegant-device-table">
            <caption className="sr-only">DNS Filter device profiles</caption>
            <colgroup><col /><col className="elegant-col-mac" /><col className="elegant-col-ip" /><col className="elegant-col-expires" /><col style={{ width: 120 }} /><col className="elegant-col-actions" /></colgroup>
            <thead>
              <tr><th>Profile</th><th>Devices</th><th>Services</th><th>Schedule</th><th>Status</th><th className="elegant-th-actions">Action</th></tr>
            </thead>
            <tbody>
              {profiles.length === 0 ? (
                <tr><td className="empty-state dns-profile-empty-cell" colSpan={6}><div className="dns-profile-empty"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><path d="M4 5h16M7 12h10M10 19h4" /><circle cx="12" cy="12" r="9" /></svg><strong>No device profiles yet</strong><span>Create a profile to schedule service access for selected devices.</span><button className="button secondary" disabled={!apiConnected || saving} onClick={() => setModalOpen(true)} type="button">Create first profile</button></div></td></tr>
              ) : profiles.map((profile) => (
                <tr key={profile.id}>
                  <td className="elegant-cell-name"><strong>{profile.name}</strong></td>
                  <td className="elegant-cell-ip"><code>{profile.ip_addresses.join(", ")}</code></td>
                  <td><div className="service-tags">{profile.services.map((service) => <span key={service}>{service}</span>)}</div></td>
                  <td>{describeSchedule(profile)}</td>
                  <td>
                    <button className={`status-pill ${profile.enabled ? "is-active" : ""}`} disabled={saving} onClick={() => toggleProfile(profile.id)} type="button">
                      {profile.enabled ? "Active" : "Paused"}
                    </button>
                  </td>
                  <td className="elegant-cell-actions"><div className="device-row-actions"><button className="button secondary small" disabled={saving} onClick={() => startEditProfile(profile)} type="button">Edit</button><button className="icon-danger" disabled={saving} onClick={() => removeProfile(profile.id)} title="Remove profile" type="button">✕</button></div></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </article>

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
                  <fieldset className="field service-picker">
                    <legend>Managed services</legend>
                    <p>Select the services controlled by this schedule.</p>
                    <div className="service-checkboxes">
                      {managedServices.map(([value, label]) => (
                        <label key={value}><input checked={services.includes(value)} onChange={() => toggleService(value)} type="checkbox" />{label}</label>
                      ))}
                    </div>
                  </fieldset>

                  <fieldset className="weekly-scheduler">
                    <legend>Allowed time</legend>
                    <div className="scheduler-toolbar">
                      <p>Coloured hours are allowed. Click or drag across the cells to change the schedule.</p>
                      <div>
                        <button className="button secondary compact" onClick={() => setGrid(createDefaultKidsGrid())} type="button">Default</button>
                        <button className="button secondary compact" onClick={() => setGrid(Object.fromEntries(scheduleDays.map(([day]) => [day, Array(24).fill(true)])) as HourGrid)} type="button">Allow all</button>
                        <button className="button secondary compact" onClick={() => setGrid(createEmptyGrid())} type="button">Block all</button>
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
                                type="button"
                              />
                            ))}
                          </div>
                        ))}
                      </div>
                    </div>
                  </fieldset>
                  <p className="form-note">By default YouTube, Steam and Wikipedia are allowed on weekdays from 19:00, and all day at weekends. You can change any hour on any day.</p>
              <div className="modal-actions"><button className="button secondary" onClick={closeModal} type="button">Cancel</button><button className="button primary" disabled={saving} type="submit">{saving ? "Applying…" : "Save profile"}</button></div>
            </form>
          </section>
        </div>
      )}
    </section>
  );
}
