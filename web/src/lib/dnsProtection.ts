import type { DNSFilterPolicy } from "../api-types";

export const dnsCategories = [
  { id: "threats", label: "Malware, phishing & scams", detail: "Known malicious domains", source: "HaGeZi TIF Mini", icon: "shield" },
  { id: "ads", label: "Ads & trackers", detail: "Less tracking, fewer distractions", source: "HaGeZi Light", icon: "eye" },
  { id: "adult", label: "Adult content", detail: "Domains hosting adult content", source: "HaGeZi NSFW", icon: "lock" },
  { id: "gambling", label: "Gambling", detail: "Betting and gambling domains", source: "HaGeZi Gambling Mini", icon: "dice" },
] as const;
export type DNSOperation = { id: string; kind: string; state: string; phase: string; actor?: string; base_revision: number; target_revision: number; applied_revision?: number; started_at: string; updated_at: string; finished_at?: string; error?: string };
export type DNSProfileStatus = { id: string; name: string; state: "paused" | "filter_off" | "allowed" | "blocked"; allowed_now: boolean | null; next_change_at: string | null; next_state?: string; ip_addresses: string[]; services: string[]; missing_reservations: string[]; basis: string; enforcement_verified: boolean };
export type DNSDevice = { ip: string; name: string; reserved: boolean };
export type DNSList = { category: string; label: string; url: string; entries: number; updated_at: number; error?: string };
export type DNSStatus = { policy: DNSFilterPolicy; domains: number; applied_at: number; healthy: boolean; lists: DNSList[]; updating: boolean; error?: string; next_refresh_at: number; refresh_allowed_at?: number; router_time: string; timezone: string; health_checked_at?: number; health_checking?: boolean; activation_pending?: boolean; generated_at?: string; operation?: DNSOperation; profiles: DNSProfileStatus[]; devices: DNSDevice[]; config_revision?: number; bundled_enabled?: boolean; blockers: string[] };
export type DNSDomainCheck = { domain: string; action: string; exception: boolean; healthy: boolean; policy_revision?: number; config_revision?: number; checked_at?: string; scope?: string; matches: { category: string; domain: string; enabled: boolean }[]; layers?: { source: string; action: string; reason: string }[]; profile?: DNSProfileStatus; coverage?: string[] };

export function sameDNSPolicy(a: DNSFilterPolicy, b: DNSFilterPolicy): boolean {
  const exceptions = (policy: DNSFilterPolicy) => policy.exceptions.map(e => ({ domain: e.domain, reason: e.reason ?? "" })).sort((x,y) => x.domain.localeCompare(y.domain));
  return dnsCategories.every(({ id }) => Boolean(a.categories[id]) === Boolean(b.categories[id]))
    && JSON.stringify(exceptions(a)) === JSON.stringify(exceptions(b));
}

export function parseDNSStatus(value: unknown): DNSStatus {
  const next = value as DNSStatus;
  if (!next || !next.policy || !Number.isSafeInteger(next.policy.revision) || !next.policy.categories || typeof next.policy.categories !== "object" || !Array.isArray(next.policy.exceptions)
    || !next.policy.exceptions.every(e => e && typeof e.domain === "string") || !Array.isArray(next.lists) || !next.lists.every(list => list && typeof list.category === "string" && Number.isFinite(list.entries))
    || !Number.isFinite(next.domains) || typeof next.healthy !== "boolean" || typeof next.updating !== "boolean") throw new Error("DNS status returned an invalid response. Last known data has been retained.");
  return { ...next, profiles: Array.isArray(next.profiles) ? next.profiles : [], devices: Array.isArray(next.devices) ? next.devices : [], blockers: Array.isArray(next.blockers) ? next.blockers : [] };
}

export function normalizedException(raw: string): string {
  const domain = raw.trim().toLowerCase().replace(/\.$/, "");
  if (domain.length > 253 || !domain.includes(".") || /^(?:\d+\.){3}\d+$/.test(domain) || /\.(local|lan|arpa)$/.test(domain) || !domain.split(".").every(label => /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/.test(label))) throw new Error("Enter a public domain, such as school.example.com, without a URL, wildcard or IP address.");
  return domain;
}

export const dnsTime = (epoch?: number | string | null) => epoch ? new Date(typeof epoch === "number" ? epoch * 1000 : epoch).toLocaleString() : "Not yet recorded";
export const dnsScheduleTime = (value: string) => `${value.slice(0,10)} · ${value.slice(11,16)} UTC${value.match(/([+-]\d{2}:\d{2})$/)?.[1] ?? ""}`;
export const dnsClockZone = (value: string, zone: string) => `${zone}${value.match(/([+-]\d{2}:\d{2})$/)?.[1] ? ` · UTC${value.slice(-6)}` : ""}`;
export const dnsOperationFinished = (op: DNSOperation) => op.state !== "running";
