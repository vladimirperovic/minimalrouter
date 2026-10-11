import type { ReactNode } from "react";

const destinations: Record<string, [string, string]> = {
  recovery: ["recovery", "Recovery"], transaction: ["recovery", "Recovery"], config: ["recovery", "Recovery"],
  backup: ["recovery", "Recovery"], storage: ["recovery", "Recovery"],
  dns_dhcp: ["network", "LAN & DHCP"], pppoe_service: ["network", "WAN settings"],
  gateway: ["gateway", "Gateway Quality"], firewall: ["firewall", "Firewall"], conntrack: ["firewall", "Firewall"],
  wireguard: ["wireguard", "WireGuard"], "wireguard-client": ["wireguard", "WireGuard"],
  ddns: ["cloudflare", "DynDNS"], qos: ["qos", "QoS / SQM"],
  dns_filter: ["dns-filter", "DNS Filter"],
  time: ["logs", "Diagnostics"], memory: ["logs", "Diagnostics"], services: ["logs", "Diagnostics"],
};

/** Local destinations only: health response text cannot supply a navigation URL. */
export default function HealthCheckLink({ id, label, children, onOpenUpdates }: {
  id: string; label: string; children: ReactNode; onOpenUpdates?: () => void;
}) {
  if ((id === "update" || id === "updates") && onOpenUpdates) {
    return <button type="button" className="health-settings-link" aria-label={`${label} → Updates`} onClick={onOpenUpdates}>{children}</button>;
  }
  const [section, destination] = Object.hasOwn(destinations, id) ? destinations[id] : ["logs", "Diagnostics"];
  return <a className="health-settings-link" href={`#${section}`} aria-label={`${label} → ${destination}`}>{children}</a>;
}
