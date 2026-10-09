import type { DNSFilterPolicy, RouterConfig } from "../api-types";
import { normalizeDayWindows, type DeviceProfile, type ScheduleDay } from "./deviceProfiles";
import { dnsCategories, type DNSOperation, type DNSProfileStatus } from "./dnsProtection";

let policy: DNSFilterPolicy = { revision: 1, categories: { threats: true, ads: true }, exceptions: [] };
let appliedAt = Math.floor(Date.now()/1000);
let refreshedAt = appliedAt;
const operations: DNSOperation[] = [];
const json = (data: unknown, status = 200) => new Response(JSON.stringify(data), { status, headers: { "Content-Type": "application/json" } });
type Config = Pick<RouterConfig, "revision" | "adguard" | "dhcp" | "dns">;

function profileContext(config: Config, now: Date): DNSProfileStatus[] {
  return (config.adguard.device_profiles as DeviceProfile[]).map(profile => {
    const windows = normalizeDayWindows(profile.schedule);
    const allowedAt = (at: Date) => {
      const day = at.toLocaleDateString("en-US", { weekday: "long", timeZone: "UTC" }).toLowerCase() as ScheduleDay;
      const clock = at.toISOString().slice(11,16);
      return windows[day].some(window => clock >= window.start && (window.end === "23:59" || clock < window.end));
    };
    const active = profile.enabled && config.adguard.enabled;
    const allowed = allowedAt(now);
    let next: string | null = null;
    if (active) {
      const boundaries = new Set<number>();
      for (let offset = 0; offset <= 8; offset++) {
        const day = new Date(now); day.setUTCHours(0,0,0,0); day.setUTCDate(day.getUTCDate()+offset);
        boundaries.add(+day);
        const key = day.toLocaleDateString("en-US", { weekday: "long", timeZone: "UTC" }).toLowerCase() as ScheduleDay;
        windows[key].forEach(window => [window.start, window.end === "23:59" ? "24:00" : window.end].forEach(clock => { const [h,m] = clock.split(":").map(Number); boundaries.add(+day+(h*60+m)*60000); }));
      }
      const at = [...boundaries].sort((a,b) => a-b).find(at => at > +now && allowedAt(new Date(at)) !== allowed);
      if (at) next = new Date(at).toISOString();
    }
    return { ...profile, state: !profile.enabled ? "paused" : !config.adguard.enabled ? "filter_off" : allowed ? "allowed" : "blocked", allowed_now: active ? allowed : null, next_change_at: next, next_state: next ? allowed ? "blocked" : "allowed" : undefined, missing_reservations: profile.ip_addresses.filter(ip => !config.dhcp.static_leases.some(lease => lease.ip_address === ip)), basis: "configured_schedule", enforcement_verified: false };
  });
}

// In-memory demonstration only: no lists are downloaded and no resolver is changed.
export function demoDNSRequest(url: URL, init: RequestInit, config: Config): Response | null {
  if (!url.pathname.startsWith("/api/v1/dns-filter")) return null;
  const method = (init.method ?? "GET").toUpperCase();
  const now = new Date();
  const context = { config_revision: config.revision, router_time: now.toISOString(), timezone: "UTC", bundled_enabled: config.adguard.enabled, profiles: profileContext(config,now), devices: config.dhcp.static_leases.map(lease => ({ ip: lease.ip_address, name: lease.hostname, reserved: true })), blockers: [], demo: true };
  if (url.pathname.endsWith("/profiles")) return json(context);
  if (url.pathname.endsWith("/operations")) return json({ operations, retention: 20, demo: true });
  if (url.pathname.endsWith("/check")) {
    const input = method === "POST" ? JSON.parse(String(init.body ?? "{}")) : Object.fromEntries(url.searchParams);
    const domain = String(input.domain ?? "").trim().toLowerCase();
    const exception = policy.exceptions.some(e => domain === e.domain || domain.endsWith(`.${e.domain}`));
    const local = config.dns.records.find(record => record.name === domain);
    return json({ domain, exception, action: local ? "Local DNS record" : exception ? "Allow exception" : "Demo: no live catalog checked", healthy: false, policy_revision: policy.revision, config_revision: config.revision, checked_at: now.toISOString(), scope: "demo_policy", matches: [], layers: [{ source: "demo", action: "unverified", reason: local ? `Demonstration local address: ${local.ip}` : "Demo data only. Connect to an appliance to check real lists and resolver health." }], profile: context.profiles.find(profile => profile.ip_addresses.includes(input.device_ip)), demo: true });
  }
  if (method === "PUT" || (method === "POST" && url.pathname.endsWith("/refresh"))) {
    const refresh = method === "POST";
    const next = refresh ? policy : JSON.parse(String(init.body ?? "{}")) as DNSFilterPolicy;
    if (next.revision !== policy.revision) return json({ error: "Policy changed; reload before saving" },409);
    const timestamp = now.toISOString();
    const op: DNSOperation = { id: crypto.randomUUID(), kind: refresh ? "refresh" : "policy", state: "completed", phase: "verified", base_revision: policy.revision, target_revision: policy.revision+1, applied_revision: policy.revision+1, started_at: timestamp, updated_at: timestamp, finished_at: timestamp, actor: "demo" };
    policy = { ...next, revision: next.revision+1 }; appliedAt = Math.floor(+now/1000);
    if (refresh) refreshedAt = appliedAt;
    operations.unshift(op); operations.splice(20);
    return json({ updating: true, operation: op, demo: true },202);
  }
  return json({ ...context, policy, domains: Object.values(policy.categories).filter(Boolean).length*10000, applied_at: appliedAt, healthy: true, health_checked_at: Math.floor(+now/1000), lists: dnsCategories.map(category => ({ category: category.id, label: category.label, url: "https://github.com/hagezi/dns-blocklists", entries: 10000, updated_at: refreshedAt })), updating: false, next_refresh_at: refreshedAt+86400, operation: operations[0] });
}
