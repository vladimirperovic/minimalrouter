import DNSUtilityIcon, { type DNSUtilityIconKind } from "./DNSUtilityIcon";

export default function DNSDisclosureHeading({ icon, title, description, meta }: { icon: DNSUtilityIconKind; title: string; description: string; meta?: string }) {
  return <><span className="dns-disclosure-icon"><DNSUtilityIcon kind={icon}/></span><span className="dns-disclosure-copy"><strong>{title}</strong><small>{description}</small></span>{meta && <span className="dns-disclosure-meta">{meta}</span>}<span className="dns-disclosure-chevron"><DNSUtilityIcon kind="chevron"/></span></>;
}
