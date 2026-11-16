import { useState } from "react";
import type { RouterConfig } from "../api-types";
import { exemptionBlocker, exemptionsOf, MAX_DNS_EXEMPTIONS } from "../lib/dnsExemptions";
import type { DNSDevice } from "../lib/dnsProtection";
import DNSUtilityIcon from "./DNSUtilityIcon";

type Props = { config: RouterConfig | null; devices: DNSDevice[]; canEdit: boolean; error: string; onChange: (device: DNSDevice, exempt: boolean) => void };

// dnsmasq applies its block lists to every client, so an exempt device is sent
// past it: its lookups go straight to the first upstream resolver. That is the
// whole feature, and it has side effects the parent should see before enabling it.
export default function DNSExemptCard({ config, devices, canEdit, error, onChange }: Props) {
  const [search, setSearch] = useState("");
  const exempt = new Set(exemptionsOf(config).map((item) => item.ip_address));
  // Reserved devices can be exempted; an exempt device whose reservation was
  // removed stays visible so the exemption can still be switched off.
  const rows = devices.filter((device) => device.reserved || exempt.has(device.ip));
  const needle = search.trim().toLowerCase();
  const visible = rows.filter((device) => `${device.name} ${device.ip}`.toLowerCase().includes(needle));
  return <article className="dns-protection-card dns-exempt-card" aria-label="Devices outside DNS protection">
    <header>
      <div className="dns-section-lead">
        <span className="dns-disclosure-icon"><DNSUtilityIcon kind="device"/></span>
        <div>
          <p className="eyebrow">A device that is not filtered</p>
          <h3>Devices outside DNS protection</h3>
          <p>Network categories such as adult content, gambling and ads do not apply to the devices you switch on here. Their lookups go straight to the first upstream resolver instead of this router.</p>
        </div>
      </div>
      <span className={`dns-neutral-chip ${exempt.size ? "is-on" : ""}`}>{exempt.size} / {MAX_DNS_EXEMPTIONS} exempt</span>
    </header>
    <div className="dns-profiles-toolbar"><label className="field"><span className="sr-only">Search devices</span><span className="dns-tool-input"><DNSUtilityIcon kind="inspect"/><input value={search} onChange={(event) => setSearch(event.target.value)} placeholder="Search by name or address"/></span></label></div>
    {error && <p className="dns-message is-error" role="alert">{error}</p>}
    <ul className="dns-exempt-list">{visible.map((device) => {
      const on = exempt.has(device.ip);
      const blocker = exemptionBlocker(config, device);
      return <li key={device.ip} className={`dns-exempt-row ${on ? "is-exempt" : ""}`}>
        <div><strong>{device.name || device.ip}</strong><code>{device.ip}</code>{blocker && <small>{blocker}</small>}</div>
        <label className="dns-toggle"><span className="sr-only">{on ? "Apply" : "Skip"} DNS protection for {device.name || device.ip}</span><input type="checkbox" checked={on} disabled={!canEdit || Boolean(blocker)} onChange={() => onChange(device, !on)}/><span/></label>
      </li>;
    })}</ul>
    {!visible.length && <p className="dns-message">{rows.length ? "No device matches that search." : "No reserved devices yet. Give a device a DHCP reservation on the Network page first, so the exception cannot move to another device."}</p>}
    <p className="dns-exempt-note">An exempt device also skips the DNS enforcement presets. Names that only the router knows (local DNS records and DHCP names) and DNS Activity do not cover it. Apply and confirm like any network change.</p>
  </article>;
}
