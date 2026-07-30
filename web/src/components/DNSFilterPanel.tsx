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
  const [profileTemplate, setProfileTemplate] = useState<"" | "kids">("");
  const [name, setName] = useState("Kids");
  const [addresses, setAddresses] = useState("");
  const [services, setServices] = useState<string[]>(["youtube", "steam", "wiki"]);
  const [grid, setGrid] = useState<HourGrid>(() => createDefaultKidsGrid());
  const dragValue = useRef<boolean | null>(null);

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
    setProfileTemplate("");
    setName("Kids");
    setAddresses("");
    setServices(["youtube", "steam", "wiki"]);
    setGrid(createDefaultKidsGrid());
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
    if (profileTemplate !== "kids") {
      onError("Odaberite Kids profil prije podešavanja rasporeda.");
      return;
    }
    setSaving(true);
    try {
      const profile = createKidsProfile({
        name,
        addresses: addresses.split(","),
        services,
        dayWindows: gridToDayWindows(grid),
      });
      await persist(true, [...profiles, profile]);
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
      <div className="section-heading dns-filter-heading">
        <div>
          <p className="eyebrow">DNS Filter & Device Profiles</p>
          <h2>Scheduled service access</h2>
          <p className="dns-filter-intro">
            Uređaji koriste statičke LAN adrese. DNS odgovori pune nftables skupove,
            a firewall prekida YouTube, Steam i druge izabrane servise izvan dozvoljenog vremena.
          </p>
        </div>
        <div className="dns-filter-actions">
          <span className="quiet-meta">Status: <strong>{enabled ? "Active" : "Disabled"}</strong></span>
          <button className="button secondary" disabled={!apiConnected || saving} onClick={toggleGlobal} type="button">
            {enabled ? "Disable DNS Filter" : "Enable DNS Filter"}
          </button>
          <button className="button primary" disabled={!apiConnected || saving} onClick={() => setModalOpen(true)} type="button">
            Add device profile
          </button>
        </div>
      </div>

      <article className="card table-card">
        <div className="card-title-row">
          <div>
            <h3>Device profiles</h3>
            <p>Za Kids profil biraš dozvoljene sate posebno za svaki dan u sedmici.</p>
          </div>
        </div>
        <div className="table-scroll">
          <table>
            <caption className="sr-only">DNS Filter device profiles</caption>
            <thead>
              <tr><th>Profile</th><th>Devices</th><th>Services</th><th>Schedule</th><th>Status</th><th>Action</th></tr>
            </thead>
            <tbody>
              {profiles.length === 0 ? (
                <tr><td className="empty-state" colSpan={6}>No device profiles yet.</td></tr>
              ) : profiles.map((profile) => (
                <tr key={profile.id}>
                  <td><strong>{profile.name}</strong></td>
                  <td><code>{profile.ip_addresses.join(", ")}</code></td>
                  <td><div className="service-tags">{profile.services.map((service) => <span key={service}>{service}</span>)}</div></td>
                  <td>{describeSchedule(profile)}</td>
                  <td>
                    <button className={`status-pill ${profile.enabled ? "is-active" : ""}`} disabled={saving} onClick={() => toggleProfile(profile.id)} type="button">
                      {profile.enabled ? "Active" : "Paused"}
                    </button>
                  </td>
                  <td><button className="icon-danger" disabled={saving} onClick={() => removeProfile(profile.id)} title="Remove profile" type="button">✕</button></td>
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
              <div><p className="eyebrow">Parental control</p><h2 id="profile-title">Device profile</h2></div>
              <button aria-label="Close profile dialog" className="modal-close" onClick={closeModal} type="button">✕</button>
            </div>
            <form className="form-grid" onSubmit={submitProfile}>
              <label className="field">
                <span>Profile type</span>
                <select onChange={(event) => setProfileTemplate(event.target.value as "" | "kids")} required value={profileTemplate}>
                  <option value="">Select profile…</option>
                  <option value="kids">Kids</option>
                </select>
              </label>

              {profileTemplate === "kids" && (
                <>
                  <label className="field"><span>Profile name</span><input onChange={(event) => setName(event.target.value)} required value={name} /></label>
                  <label className="field"><span>Static IP addresses</span><input onChange={(event) => setAddresses(event.target.value)} placeholder="192.168.1.50, 192.168.1.51" required value={addresses} /></label>
                  <fieldset className="field service-picker">
                    <legend>Managed services</legend>
                    <div className="service-checkboxes">
                      {managedServices.map(([value, label]) => (
                        <label key={value}><input checked={services.includes(value)} onChange={() => toggleService(value)} type="checkbox" />{label}</label>
                      ))}
                    </div>
                  </fieldset>

                  <fieldset className="weekly-scheduler">
                    <legend>Allowed time</legend>
                    <div className="scheduler-toolbar">
                      <p>Obojeni sati su dozvoljeni. Klikni ili prevuci preko polja da promijeniš raspored.</p>
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
                  <p className="form-note">Podrazumijevano su YouTube, Steam i Wikipedia dozvoljeni radnim danima od 19:00, a vikendom cijeli dan. Ti možeš promijeniti svaki sat i svaki dan.</p>
                </>
              )}

              <div className="modal-actions"><button className="button secondary" onClick={closeModal} type="button">Cancel</button><button className="button primary" disabled={saving || profileTemplate !== "kids"} type="submit">{saving ? "Applying…" : "Save profile"}</button></div>
            </form>
          </section>
        </div>
      )}
    </section>
  );
}
