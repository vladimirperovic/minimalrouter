import { useState } from "react";
import type { RouterConfig } from "../api-types";
import { previewAndApplyConfig } from "../lib/configuration";
import { dnsEnforcementState, setDNSEnforcement } from "../lib/firewall";
import DNSUtilityIcon from "./DNSUtilityIcon";

type Props = { config: RouterConfig | null; canEdit: boolean; rulesInUse: boolean };

// The DNS rules on this page only see lookups that reach the router. Keep the
// one switch that makes them binding at the top of the page, where a parent
// configuring categories or schedules cannot miss it.
export default function DNSEnforcementCard({ config, canEdit, rulesInUse }: Props) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const state = config ? dnsEnforcementState(config.firewall.custom_rules || []) : null;
  const enforced = state === "Active";
  const exposed = rulesInUse && state !== null && !enforced;

  async function change(next: boolean) {
    if (!config || busy) return;
    setBusy(true); setError("");
    try {
      const candidate = structuredClone(config);
      setDNSEnforcement(candidate, next);
      await previewAndApplyConfig(candidate);
    } catch (err) { setError((err as Error).message); }
    finally { setBusy(false); }
  }

  const title = state === null ? "Checking DNS enforcement"
    : enforced ? "Every device must use router DNS"
    : state === "Partial" ? "Router DNS is only partly enforced"
    : "Devices can skip these rules";
  const chip = state === null ? "—" : enforced ? "Enforced" : state === "Partial" ? "Partly enforced" : "Not enforced";

  return <article className={`dns-protection-card dns-enforcement-card ${exposed ? "is-exposed" : ""} ${enforced ? "is-enforced" : ""}`} aria-label="Router DNS enforcement">
    <header>
      <div className="dns-section-lead">
        <span className="dns-disclosure-icon"><DNSUtilityIcon kind="lock"/></span>
        <div>
          <p className="eyebrow">Make the rules binding</p>
          <h3>{title}</h3>
          <p>Categories, exceptions and device schedules only apply to lookups that go through this router. {enforced
            ? "Direct DNS and DNS-over-TLS to other resolvers are blocked for the whole network."
            : "A device set to another DNS server, such as 8.8.8.8, bypasses them."}</p>
        </div>
      </div>
      <span className={`dns-neutral-chip ${enforced ? "is-on" : ""}`}>{chip}</span>
    </header>
    <div className="dns-enforcement-action">
      <p>Blocks TCP/UDP 53 and DNS-over-TLS (TCP 853) toward the Internet; router DNS keeps working. Encrypted DNS over HTTPS and VPNs are not covered. The same rules appear as presets on the Firewall page.</p>
      <button className={`button ${enforced ? "secondary" : "primary"}`} type="button" disabled={!canEdit || busy || state === null} onClick={() => void change(!enforced)}>
        {busy ? "Applying…" : enforced ? "Stop forcing router DNS" : "Force all devices through router DNS"}
      </button>
    </div>
    {error && <p className="dns-message is-error" role="alert">{error}</p>}
  </article>;
}
