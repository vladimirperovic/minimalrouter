import { useEffect, useMemo, useState } from "react";
import { apiFetch } from "../lib/api";
import {
  type DeviceProfile,
  buildDeviceScheduleProfile,
  idFromName,
  ipv4IsValid,
  macIsValid,
  netmaskFromCIDR,
  normalizedHostname,
} from "../lib/policies";

type InterfaceInfo = {
  name: string;
  mac?: string;
  state?: string;
  carrier?: boolean;
  speed_mbps?: number;
  driver?: string;
  bus_path?: string;
  kind?: string;
  loopback?: boolean;
};

type LeaseInfo = {
  mac: string;
  ip_address: string;
  hostname?: string;
};

type StaticLease = {
  id: string;
  hostname: string;
  mac: string;
  ip_address: string;
};

type DeviceAssignment = {
  id: string;
  hostname: string;
  mac: string;
  ip_address: string;
  zone: "lan" | "iot";
  profile_id: string;
};

type RouterConfig = {
  revision: number;
  system: {
    timezone?: string;
    [key: string]: unknown;
  };
  wan: {
    interface: string;
    [key: string]: unknown;
  };
  lan: {
    interface: string;
    ip_address: string;
    [key: string]: unknown;
  };
  dhcp: {
    static_leases?: StaticLease[];
    [key: string]: unknown;
  };
  iot?: {
    enabled: boolean;
    mode: "dedicated" | "vlan";
    interface: string;
    parent_interface?: string;
    vlan_id?: number;
    ip_address: string;
    netmask: string;
    cidr: string;
    dhcp: {
      enabled: boolean;
      range_start: string;
      range_end: string;
      lease_time: string;
      static_leases?: StaticLease[];
    };
  };
  device_policies?: {
    enabled: boolean;
    profiles: DeviceProfile[];
    assignments: DeviceAssignment[];
  };
  [key: string]: unknown;
};

type NetworkPoliciesProps = {
  apiConnected: boolean;
  interfaces: InterfaceInfo[];
  leases: LeaseInfo[];
  onPendingConfirmation: (transactionID: string) => void;
};

function describeInterface(item: InterfaceInfo) {
  const details = [
    item.carrier ? "link up" : item.state || "link unknown",
    item.speed_mbps ? `${item.speed_mbps} Mbps` : "",
    item.driver || "",
    item.bus_path || "",
    item.mac || "",
  ].filter(Boolean);
  return `${item.name}${details.length ? ` — ${details.join(" · ")}` : ""}`;
}

function policySummary(profile: DeviceProfile) {
  const weekdayWindow = profile.windows.find((window) => window.days.includes("monday"));
  const weekendWindow = profile.windows.find((window) => window.days.includes("saturday"));
  const access = profile.access_mode === "allow_all"
    ? "all Internet"
    : profile.allowed_services.map((service) => service === "youtube" ? "YouTube" : "Steam").join(" + ");
  const weekdayText = weekdayWindow?.all_day
    ? "weekdays all day"
    : weekdayWindow
      ? `weekdays ${weekdayWindow.start}–${weekdayWindow.end}`
      : "weekdays blocked";
  const weekendText = weekendWindow?.all_day
    ? "weekends all day"
    : weekendWindow
      ? `weekends ${weekendWindow.start}–${weekendWindow.end}`
      : "weekends blocked";
  return `${access}; ${weekdayText}; ${weekendText}`;
}

export default function NetworkPolicies({
  apiConnected,
  interfaces,
  leases,
  onPendingConfirmation,
}: NetworkPoliciesProps) {
  const [config, setConfig] = useState<RouterConfig | null>(null);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [notice, setNotice] = useState("");
  const [error, setError] = useState("");

  const [iotEnabled, setIoTEnabled] = useState(false);
  const [iotMode, setIoTMode] = useState<"dedicated" | "vlan">("dedicated");
  const [iotInterface, setIoTInterface] = useState("eth2");
  const [iotParent, setIoTParent] = useState("eth1");
  const [iotVLAN, setIoTVLAN] = useState("30");
  const [iotIP, setIoTIP] = useState("192.168.30.1");
  const [iotCIDR, setIoTCIDR] = useState("192.168.30.1/24");
  const [iotRangeStart, setIoTRangeStart] = useState("192.168.30.100");
  const [iotRangeEnd, setIoTRangeEnd] = useState("192.168.30.200");

  const [timezone, setTimezone] = useState(() => Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC");
  const [deviceName, setDeviceName] = useState("");
  const [deviceMAC, setDeviceMAC] = useState("");
  const [deviceIP, setDeviceIP] = useState("");
  const [deviceZone, setDeviceZone] = useState<"lan" | "iot">("lan");
  const [weekdayStart, setWeekdayStart] = useState("19:00");
  const [weekdayEnd, setWeekdayEnd] = useState("23:59");
  const [weekendAllDay, setWeekendAllDay] = useState(true);
  const [accessMode, setAccessMode] = useState<"allow_all" | "allow_services">("allow_services");
  const [allowYouTube, setAllowYouTube] = useState(true);
  const [allowSteam, setAllowSteam] = useState(true);

  const usableInterfaces = useMemo(
    () => interfaces.filter((item) => !item.loopback && item.kind !== "wifi"),
    [interfaces],
  );
  const dedicatedIoTInterfaces = useMemo(
    () => usableInterfaces.filter((item) => item.name !== config?.wan.interface && item.name !== config?.lan.interface),
    [usableInterfaces, config?.wan.interface, config?.lan.interface],
  );
  const vlanParents = useMemo(
    () => usableInterfaces.filter((item) => item.name !== config?.wan.interface),
    [usableInterfaces, config?.wan.interface],
  );

  const loadConfig = async () => {
    if (!apiConnected) return;
    setLoading(true);
    try {
      const response = await apiFetch("/api/v1/config");
      const body = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(body.error || `Configuration load failed (${response.status})`);
      const next = body as RouterConfig;
      setConfig(next);
      setTimezone(next.system?.timezone || Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC");
      const iot = next.iot;
      if (iot) {
        setIoTEnabled(iot.enabled === true);
        setIoTMode(iot.mode || "dedicated");
        setIoTInterface(iot.interface || "eth2");
        setIoTParent(iot.parent_interface || next.lan.interface || "eth1");
        setIoTVLAN(String(iot.vlan_id || 30));
        setIoTIP(iot.ip_address || "192.168.30.1");
        setIoTCIDR(iot.cidr || "192.168.30.1/24");
        setIoTRangeStart(iot.dhcp?.range_start || "192.168.30.100");
        setIoTRangeEnd(iot.dhcp?.range_end || "192.168.30.200");
      }
      setError("");
    } catch (loadError) {
      setError(loadError instanceof Error ? loadError.message : "Configuration load failed");
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void loadConfig();
  }, [apiConnected]);

  const mutateConfig = async (mutate: (current: RouterConfig) => void, successMessage: string) => {
    if (!apiConnected) throw new Error("Router API is unavailable");
    const currentResponse = await apiFetch("/api/v1/config");
    const currentBody = await currentResponse.json().catch(() => ({}));
    if (!currentResponse.ok) {
      throw new Error(currentBody.error || `Configuration load failed (${currentResponse.status})`);
    }
    const current = currentBody as RouterConfig;
    mutate(current);
    const response = await apiFetch("/api/v1/config", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(current),
    });
    const body = await response.json().catch(() => ({}));
    if (!response.ok) throw new Error(body.error || `Configuration update failed (${response.status})`);
    if (body.state === "AwaitingConfirmation" && typeof body.id === "string") {
      onPendingConfirmation(body.id);
    }
    setNotice(successMessage);
    setError("");
    await loadConfig();
  };

  const saveIoT = async () => {
    setSaving(true);
    setNotice("");
    try {
      if (!ipv4IsValid(iotIP) || !iotCIDR.startsWith(`${iotIP}/`)) {
        throw new Error("IoT gateway and CIDR must use the same valid IPv4 address");
      }
      const iotNetmask = netmaskFromCIDR(iotCIDR);
      if (!iotNetmask) {
        throw new Error("IoT CIDR must use an IPv4 prefix between /1 and /30");
      }
      if (!ipv4IsValid(iotRangeStart) || !ipv4IsValid(iotRangeEnd)) {
        throw new Error("IoT DHCP range must contain valid IPv4 addresses");
      }
      if (iotEnabled && iotMode === "dedicated" && !iotInterface) {
        throw new Error("Select a dedicated IoT interface");
      }
      if (iotEnabled && iotMode === "vlan" && !iotParent) {
        throw new Error("Select the VLAN trunk interface");
      }
      const currentAssignments = config?.device_policies?.assignments ?? [];
      if (!iotEnabled && currentAssignments.some((assignment) => assignment.zone === "iot")) {
        throw new Error("Remove or move IoT device policies before disabling the IoT zone");
      }
      const vlanID = Number.parseInt(iotVLAN, 10);
      if (iotMode === "vlan" && (!Number.isInteger(vlanID) || vlanID < 1 || vlanID > 4094)) {
        throw new Error("VLAN ID must be between 1 and 4094");
      }
      await mutateConfig((current) => {
        const existingLeases = current.iot?.dhcp?.static_leases ?? [];
        current.iot = {
          enabled: iotEnabled,
          mode: iotMode,
          interface: iotInterface,
          parent_interface: iotMode === "vlan" ? iotParent : "",
          vlan_id: iotMode === "vlan" ? vlanID : 0,
          ip_address: iotIP,
          netmask: iotNetmask,
          cidr: iotCIDR,
          dhcp: {
            enabled: true,
            range_start: iotRangeStart,
            range_end: iotRangeEnd,
            lease_time: current.iot?.dhcp?.lease_time || "12h",
            static_leases: existingLeases,
          },
        };
      }, iotEnabled ? "IoT isolation configuration applied" : "IoT isolation disabled");
    } catch (saveError) {
      setError(saveError instanceof Error ? saveError.message : "IoT configuration failed");
    } finally {
      setSaving(false);
    }
  };

  const selectLease = (value: string) => {
    const lease = leases.find((item) => `${item.mac}|${item.ip_address}` === value);
    if (!lease) return;
    setDeviceName(lease.hostname && lease.hostname !== "*" ? lease.hostname : "device");
    setDeviceMAC(lease.mac);
    setDeviceIP(lease.ip_address);
  };

  const createKidsSchedule = async () => {
    setSaving(true);
    setNotice("");
    try {
      const normalizedMAC = deviceMAC.trim().toLowerCase();
      const normalizedName = normalizedHostname(deviceName);
      if (!normalizedName || !macIsValid(normalizedMAC) || !ipv4IsValid(deviceIP)) {
        throw new Error("Device name, 48-bit MAC address, and fixed IPv4 address are required");
      }
      if (deviceZone === "iot" && !config?.iot?.enabled) {
        throw new Error("Enable and apply the IoT zone before assigning an IoT device policy");
      }
      if (weekdayStart >= weekdayEnd) {
        throw new Error("Weekday end time must be later than start time");
      }
      const services = [allowYouTube ? "youtube" : "", allowSteam ? "steam" : ""].filter(Boolean);
      if (accessMode === "allow_services" && services.length === 0) {
        throw new Error("Select at least one allowed service");
      }
      const suffix = Date.now().toString(36).slice(-7);
      const profileID = idFromName(deviceName || "kids", `profile-${suffix}`);
      const assignmentID = idFromName(deviceName || "device", suffix);
      const profile = buildDeviceScheduleProfile({
        id: profileID,
        name: `${deviceName.trim()} schedule`,
        accessMode,
        allowedServices: services,
        weekdayStart,
        weekdayEnd,
        weekendAllDay,
      });
      const assignment: DeviceAssignment = {
        id: assignmentID,
        hostname: normalizedName,
        mac: normalizedMAC,
        ip_address: deviceIP.trim(),
        zone: deviceZone,
        profile_id: profileID,
      };
      await mutateConfig((current) => {
        current.system.timezone = timezone || "UTC";
        current.device_policies ??= { enabled: false, profiles: [], assignments: [] };
        const duplicate = current.device_policies.assignments.find(
          (item) => item.mac.toLowerCase() === normalizedMAC || item.ip_address === assignment.ip_address,
        );
        if (duplicate) {
          throw new Error("This MAC or IP address already has a device policy");
        }
        const lease: StaticLease = {
          id: assignmentID,
          hostname: normalizedName,
          mac: normalizedMAC,
          ip_address: assignment.ip_address,
        };
        if (deviceZone === "iot") {
          if (!current.iot?.enabled) throw new Error("IoT zone is not enabled");
          const leasesForZone = current.iot.dhcp.static_leases ?? [];
          const conflict = leasesForZone.find(
            (item) => item.mac.toLowerCase() === normalizedMAC || item.ip_address === assignment.ip_address,
          );
          if (conflict && (conflict.mac.toLowerCase() !== normalizedMAC || conflict.ip_address !== assignment.ip_address)) {
            throw new Error("The IoT DHCP reservation conflicts with an existing MAC or IP address");
          }
          if (!conflict) current.iot.dhcp.static_leases = [...leasesForZone, lease];
        } else {
          const leasesForZone = current.dhcp.static_leases ?? [];
          const conflict = leasesForZone.find(
            (item) => item.mac.toLowerCase() === normalizedMAC || item.ip_address === assignment.ip_address,
          );
          if (conflict && (conflict.mac.toLowerCase() !== normalizedMAC || conflict.ip_address !== assignment.ip_address)) {
            throw new Error("The LAN DHCP reservation conflicts with an existing MAC or IP address");
          }
          if (!conflict) current.dhcp.static_leases = [...leasesForZone, lease];
        }
        current.device_policies.enabled = true;
        current.device_policies.profiles = [...current.device_policies.profiles, profile];
        current.device_policies.assignments = [...current.device_policies.assignments, assignment];
      }, `Schedule created for ${deviceName.trim()}`);
      setDeviceName("");
      setDeviceMAC("");
      setDeviceIP("");
    } catch (saveError) {
      setError(saveError instanceof Error ? saveError.message : "Device schedule failed");
    } finally {
      setSaving(false);
    }
  };

  const togglePolicies = async () => {
    setSaving(true);
    setNotice("");
    try {
      const next = !(config?.device_policies?.enabled === true);
      await mutateConfig((current) => {
        current.system.timezone = timezone || "UTC";
        current.device_policies ??= { enabled: false, profiles: [], assignments: [] };
        current.device_policies.enabled = next;
      }, next ? "Device schedules enabled" : "Device schedules paused");
    } catch (saveError) {
      setError(saveError instanceof Error ? saveError.message : "Policy update failed");
    } finally {
      setSaving(false);
    }
  };

  const removePolicy = async (assignment: DeviceAssignment) => {
    setSaving(true);
    setNotice("");
    try {
      await mutateConfig((current) => {
        current.device_policies ??= { enabled: false, profiles: [], assignments: [] };
        current.device_policies.assignments = current.device_policies.assignments.filter((item) => item.id !== assignment.id);
        const stillUsed = current.device_policies.assignments.some((item) => item.profile_id === assignment.profile_id);
        if (!stillUsed) {
          current.device_policies.profiles = current.device_policies.profiles.filter((item) => item.id !== assignment.profile_id);
        }
        if (current.device_policies.assignments.length === 0) current.device_policies.enabled = false;
      }, `Schedule removed from ${assignment.hostname}`);
    } catch (removeError) {
      setError(removeError instanceof Error ? removeError.message : "Policy removal failed");
    } finally {
      setSaving(false);
    }
  };

  const policies = config?.device_policies ?? { enabled: false, profiles: [], assignments: [] };
  const profilesByID = new Map(policies.profiles.map((profile) => [profile.id, profile]));

  return (
    <section className="section-block" id="policies">
      <div className="section-heading">
        <div>
          <p className="eyebrow">Isolation & schedules</p>
          <h2>IoT zone and device access</h2>
        </div>
        <span className={`status-label ${policies.enabled ? "success" : ""}`}>
          <i className="status-dot" /> {policies.enabled ? "Schedules active" : "Schedules paused"}
        </span>
      </div>

      {(error || notice) && (
        <div className={`policy-message ${error ? "is-error" : "is-success"}`} role="status">
          {error || notice}
        </div>
      )}

      <div className="policy-grid">
        <article className="card policy-card">
          <div className="card-title-row">
            <div>
              <h3>Isolated IoT network</h3>
              <p>IoT clients may reach the Internet, DHCP and DNS, but not the main LAN or dashboard.</p>
            </div>
            <label className="policy-toggle-label">
              <input
                type="checkbox"
                checked={iotEnabled}
                onChange={(event) => setIoTEnabled(event.target.checked)}
                disabled={!apiConnected || saving}
              />
              Enabled
            </label>
          </div>

          <div className="policy-form-grid">
            <label className="policy-field">
              <span>Connection mode</span>
              <select value={iotMode} onChange={(event) => setIoTMode(event.target.value as "dedicated" | "vlan")}>
                <option value="dedicated">Dedicated physical port</option>
                <option value="vlan">802.1Q VLAN trunk</option>
              </select>
            </label>

            {iotMode === "dedicated" ? (
              <label className="policy-field">
                <span>IoT interface</span>
                {dedicatedIoTInterfaces.length > 0 ? (
                  <select value={iotInterface} onChange={(event) => setIoTInterface(event.target.value)}>
                    <option value="">Select interface</option>
                    {dedicatedIoTInterfaces.map((item) => (
                      <option key={item.name} value={item.name}>{describeInterface(item)}</option>
                    ))}
                  </select>
                ) : (
                  <input value={iotInterface} onChange={(event) => setIoTInterface(event.target.value)} placeholder="eth2" />
                )}
              </label>
            ) : (
              <>
                <label className="policy-field">
                  <span>VLAN parent / trunk</span>
                  {vlanParents.length > 0 ? (
                    <select value={iotParent} onChange={(event) => setIoTParent(event.target.value)}>
                      <option value="">Select trunk interface</option>
                      {vlanParents.map((item) => (
                        <option key={item.name} value={item.name}>{describeInterface(item)}</option>
                      ))}
                    </select>
                  ) : (
                    <input value={iotParent} onChange={(event) => setIoTParent(event.target.value)} placeholder="eth1" />
                  )}
                </label>
                <label className="policy-field">
                  <span>VLAN ID</span>
                  <input type="number" min="1" max="4094" value={iotVLAN} onChange={(event) => setIoTVLAN(event.target.value)} />
                </label>
              </>
            )}

            <label className="policy-field">
              <span>Gateway IPv4</span>
              <input value={iotIP} onChange={(event) => setIoTIP(event.target.value)} inputMode="decimal" />
            </label>
            <label className="policy-field">
              <span>Gateway CIDR</span>
              <input value={iotCIDR} onChange={(event) => setIoTCIDR(event.target.value)} placeholder="192.168.30.1/24" />
            </label>
            <label className="policy-field">
              <span>DHCP start</span>
              <input value={iotRangeStart} onChange={(event) => setIoTRangeStart(event.target.value)} inputMode="decimal" />
            </label>
            <label className="policy-field">
              <span>DHCP end</span>
              <input value={iotRangeEnd} onChange={(event) => setIoTRangeEnd(event.target.value)} inputMode="decimal" />
            </label>
          </div>

          <div className="policy-warning">
            VLAN mode requires a correctly configured managed-switch trunk. A device on the ordinary LAN subnet is not isolated merely by labeling it IoT.
          </div>
          <button className="button primary" type="button" onClick={() => void saveIoT()} disabled={!apiConnected || saving || loading}>
            {saving ? "Applying…" : "Apply IoT configuration"}
          </button>
        </article>

        <article className="card policy-card">
          <div className="card-title-row">
            <div>
              <h3>Kids and device scheduler</h3>
              <p>Create a fixed DHCP reservation and enforce the schedule in nftables.</p>
            </div>
            <button className="button secondary" type="button" onClick={() => void togglePolicies()} disabled={!apiConnected || saving || policies.assignments.length === 0}>
              {policies.enabled ? "Pause schedules" : "Enable schedules"}
            </button>
          </div>

          {leases.length > 0 && (
            <label className="policy-field policy-field-wide">
              <span>Use a currently connected DHCP client</span>
              <select defaultValue="" onChange={(event) => selectLease(event.target.value)}>
                <option value="">Select a client to fill name, MAC and IP</option>
                {leases.map((lease) => (
                  <option key={`${lease.mac}-${lease.ip_address}`} value={`${lease.mac}|${lease.ip_address}`}>
                    {lease.hostname && lease.hostname !== "*" ? lease.hostname : "Unnamed device"} — {lease.ip_address} · {lease.mac}
                  </option>
                ))}
              </select>
            </label>
          )}

          <div className="policy-form-grid">
            <label className="policy-field">
              <span>Device name</span>
              <input value={deviceName} onChange={(event) => setDeviceName(event.target.value)} placeholder="kids-tablet" />
            </label>
            <label className="policy-field">
              <span>MAC address</span>
              <input value={deviceMAC} onChange={(event) => setDeviceMAC(event.target.value)} placeholder="02:00:00:00:00:10" />
            </label>
            <label className="policy-field">
              <span>Fixed IPv4 address</span>
              <input value={deviceIP} onChange={(event) => setDeviceIP(event.target.value)} placeholder="192.168.1.50" />
            </label>
            <label className="policy-field">
              <span>Network zone</span>
              <select value={deviceZone} onChange={(event) => setDeviceZone(event.target.value as "lan" | "iot")}>
                <option value="lan">Main LAN</option>
                <option value="iot" disabled={!config?.iot?.enabled}>Isolated IoT</option>
              </select>
            </label>
            <label className="policy-field">
              <span>Appliance timezone</span>
              <input value={timezone} onChange={(event) => setTimezone(event.target.value)} placeholder="Europe/Belgrade" />
            </label>
            <label className="policy-field">
              <span>Allowed access</span>
              <select value={accessMode} onChange={(event) => setAccessMode(event.target.value as "allow_all" | "allow_services")}>
                <option value="allow_services">Only selected services</option>
                <option value="allow_all">All Internet traffic</option>
              </select>
            </label>
            <label className="policy-field">
              <span>Weekday access starts</span>
              <input type="time" value={weekdayStart} onChange={(event) => setWeekdayStart(event.target.value)} />
            </label>
            <label className="policy-field">
              <span>Weekday access ends</span>
              <input type="time" value={weekdayEnd} onChange={(event) => setWeekdayEnd(event.target.value)} />
            </label>
          </div>

          {accessMode === "allow_services" && (
            <div className="policy-checks" aria-label="Allowed services">
              <label><input type="checkbox" checked={allowYouTube} onChange={(event) => setAllowYouTube(event.target.checked)} /> YouTube</label>
              <label><input type="checkbox" checked={allowSteam} onChange={(event) => setAllowSteam(event.target.checked)} /> Steam</label>
            </div>
          )}
          <label className="policy-weekend-check">
            <input type="checkbox" checked={weekendAllDay} onChange={(event) => setWeekendAllDay(event.target.checked)} />
            Allow the same access all day on Saturday and Sunday
          </label>

          <div className="policy-template-note">
            Current template: no Internet Monday–Friday before {weekdayStart}; after {weekdayStart} allow {accessMode === "allow_all" ? "all Internet traffic" : [allowYouTube && "YouTube", allowSteam && "Steam"].filter(Boolean).join(" + ") || "no services"}; weekends {weekendAllDay ? "all day" : "blocked"}.
          </div>
          <button className="button primary" type="button" onClick={() => void createKidsSchedule()} disabled={!apiConnected || saving || loading}>
            {saving ? "Applying…" : "Create device schedule"}
          </button>
        </article>
      </div>

      <article className="card table-card policy-list-card">
        <div className="card-title-row">
          <div>
            <h3>Assigned schedules</h3>
            <p>Rules are matched by the fixed reserved IPv4 address on the selected zone.</p>
          </div>
          <span className="quiet-meta">Timezone: <strong>{config?.system?.timezone || timezone}</strong></span>
        </div>
        <div className="table-scroll">
          <table>
            <caption className="sr-only">Device access schedule assignments</caption>
            <thead>
              <tr>
                <th>Device</th>
                <th>Zone</th>
                <th>Reservation</th>
                <th>Schedule</th>
                <th>Action</th>
              </tr>
            </thead>
            <tbody>
              {policies.assignments.length === 0 ? (
                <tr><td colSpan={5} className="policy-empty">No scheduled devices configured.</td></tr>
              ) : policies.assignments.map((assignment) => {
                const profile = profilesByID.get(assignment.profile_id);
                return (
                  <tr key={assignment.id}>
                    <td><strong>{assignment.hostname}</strong><small>{assignment.mac}</small></td>
                    <td><span className="policy-zone-badge">{assignment.zone.toUpperCase()}</span></td>
                    <td><code>{assignment.ip_address}</code></td>
                    <td>{profile ? policySummary(profile) : "Profile missing"}</td>
                    <td>
                      <button className="quiet-button policy-remove" type="button" onClick={() => void removePolicy(assignment)} disabled={saving}>
                        Remove
                      </button>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      </article>

      <p className="policy-footnote">
        YouTube/Steam mode uses DNS-populated destination sets. It is useful for household scheduling, but it is not HTTPS content inspection and cannot guarantee perfect classification when providers change domains or share CDNs.
      </p>
    </section>
  );
}
