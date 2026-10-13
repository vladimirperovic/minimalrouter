import type { GatewayHistoryPoint, GatewaySummary } from "../api-types";

export const finite = (v: unknown): v is number => typeof v === "number" && Number.isFinite(v) && v >= 0;
export const record = (v: unknown): v is Record<string, unknown> => !!v && typeof v === "object" && !Array.isArray(v);
export const date = (v: unknown): v is string => typeof v === "string" && Number.isFinite(Date.parse(v));
export const states = ["unknown", "healthy", "degraded", "offline", "flapping"];
const validTarget = (v: unknown) => record(v) && typeof v.target === "string" && typeof v.reachable === "boolean"
  && [v.latency_ms,v.jitter_ms,v.packets_sent,v.packets_received].every(n => n === undefined || finite(n))
  && finite(v.packet_loss_percent) && v.packet_loss_percent <= 100 && (v.error === undefined || typeof v.error === "string");
export function validSummary(v: unknown): v is GatewaySummary {
  return record(v) && typeof v.available === "boolean" && typeof v.enabled === "boolean" && states.includes(String(v.state))
    && record(v.link) && typeof v.link.connected === "boolean" && (v.timestamp === undefined || date(v.timestamp))
    && [v.link.local_ip,v.link.peer_ip].every(ip => ip === undefined || typeof ip === "string")
    && (v.targets === undefined || (Array.isArray(v.targets) && v.targets.every(validTarget)))
    && (v.peer_probe === undefined || validTarget(v.peer_probe));
}
export function validPoints(v: unknown): v is { points: GatewayHistoryPoint[] } {
  return record(v) && Array.isArray(v.points) && v.points.every(p => record(p) && date(p.timestamp)
    && (p.latency_ms === undefined || finite(p.latency_ms)) && finite(p.packet_loss_percent) && p.packet_loss_percent <= 100);
}
export type Insights = { available: boolean; sampled_hours: number; samples: number; uptime_percent: number; outages: number; public_ip_changes: { timestamp: string; old_ip: string; new_ip: string }[] };
export function validInsights(v: unknown): v is Insights {
  return record(v) && typeof v.available === "boolean" && finite(v.sampled_hours) && finite(v.samples) && finite(v.uptime_percent) && v.uptime_percent <= 100 && finite(v.outages)
    && Array.isArray(v.public_ip_changes) && v.public_ip_changes.every(c => record(c) && date(c.timestamp) && typeof c.old_ip === "string" && typeof c.new_ip === "string");
}
export type Diagnostic = { overall: string; cause: string; checks: Record<string, { ok: boolean; detail: string }> };
export function validDiagnostic(v: unknown): v is Diagnostic {
  return record(v) && ["healthy", "degraded", "failed"].includes(String(v.overall)) && typeof v.cause === "string" && record(v.checks)
    && ["pppoe", "internet", "dns", "https"].every(k => record(v.checks) && record(v.checks[k]))
    && Object.values(v.checks).every(c => record(c) && typeof c.ok === "boolean" && typeof c.detail === "string");
}
export type Recovery = { state: string; reason: string; updated_at: string; next_attempt_at?: string; last_attempt_at?: string; last_result?: string };
export function validRecovery(v: unknown): v is Recovery {
  return record(v) && ["armed","paused","waiting","blocked","verifying","cooldown","recovering"].includes(String(v.state)) && typeof v.reason === "string" && date(v.updated_at)
    && (v.next_attempt_at === undefined || date(v.next_attempt_at)) && (v.last_attempt_at === undefined || date(v.last_attempt_at)) && (v.last_result === undefined || typeof v.last_result === "string");
}
export type Events = {
  outages: { started_at: string; ended_at?: string; last_observed_at: string; duration_seconds: number; outcome: string; partial: boolean; cause?: "pppoe_link" | "internet" }[];
  recovery_history_available: boolean;
  recoveries: { id: string; timestamp: string; event_type: string; details: Record<string, string> }[] | null;
};
export function validEvents(v: unknown): v is Events {
  return record(v) && typeof v.recovery_history_available === "boolean" && Array.isArray(v.outages)
    && v.outages.every(o => record(o) && date(o.started_at) && date(o.last_observed_at) && (o.ended_at === undefined || date(o.ended_at)) && finite(o.duration_seconds) && ["recovered","ongoing","unknown"].includes(String(o.outcome)) && typeof o.partial === "boolean")
    && (v.recoveries === null || (Array.isArray(v.recoveries) && v.recoveries.every(e => record(e) && typeof e.id === "string" && date(e.timestamp) && typeof e.event_type === "string" && record(e.details) && Object.values(e.details).every(value => typeof value === "string"))));
}
export function duration(seconds: number) {
  if (!finite(seconds)) return "Unavailable";
  const minutes = Math.floor(seconds / 60), hours = Math.floor(minutes / 60), days = Math.floor(hours / 24);
  return days ? `${days}d ${hours % 24}h` : hours ? `${hours}h ${minutes % 60}m` : minutes ? `${minutes}m ${Math.floor(seconds % 60)}s` : `${Math.floor(seconds)}s`;
}
export function summaryState(summary: GatewaySummary | null, interval: number, now: number) {
  if (!validSummary(summary) || !summary.available) return "Unavailable";
  if (!summary.enabled) return "Monitoring off";
  const at = Date.parse(summary.timestamp || "");
  if (!Number.isFinite(at) || at > now + 5000 || now - at > Math.max(120, interval + 30) * 1000) return "Stale";
  return summary.state.charAt(0).toUpperCase() + summary.state.slice(1);
}
export const metric = (n: unknown, suffix = "") => finite(n) ? `${Number(n.toFixed(1))}${suffix}` : "Unavailable";
export const localTime = (at?: string) => date(at) ? new Date(at).toLocaleString() : "Unavailable";

export const diagnosticAdvice: Record<string, { text: string; href: string; label: string }> = {
  pppoe_link: { text: "Check the WAN cable and PPPoE credentials.", href: "#network", label: "WAN settings" },
  internet_reachability: { text: "The probe targets did not reply. Check the ISP path and target results; ICMP failure alone does not prove all Internet traffic is blocked.", href: "#gateway-targets", label: "Monitoring settings" },
  dns: { text: "Check the configured resolver and DNS service.", href: "#network", label: "LAN & DHCP" },
  https: { text: "Check the router clock and firewall rules; the HTTPS endpoint may also be unavailable.", href: "#firewall", label: "Firewall" },
};
