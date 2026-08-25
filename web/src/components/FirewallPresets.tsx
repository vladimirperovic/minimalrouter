import { useState } from "react";
import type { FirewallCustomRule, RouterConfig } from "../api-types";
import "./FirewallPresets.css";

// Ready-made hardening rules for the situations a home network hits most often.
//
// Every preset below is a real `custom_rules` entry, and the nftables generator
// constrains what those can be: each rule matches `iifname <LAN>`, denies are
// emitted before allows, and a `forward` allow is only ever LAN -> WAN egress.
// Since LAN -> WAN is already accepted by default, an "allow" preset would add
// a rule that permits what is permitted anyway — so every preset here is a
// deny. Blocking is the thing this model can actually do from one checkbox.
//
// Exposing a service to yourself from outside is a different mechanism: WAN
// ingress stays WireGuard-only by design, so that is a tunnel port forward
// against a specific host, not a firewall rule. The card says so.

export type Preset = {
  id: string;
  title: string;
  detail: string;
  caution?: string;
  protocol: "tcp" | "udp";
  port: number;
  direction: "forward" | "input";
};

export const PRESETS: readonly Preset[] = [
  {
    id: "smb",
    title: "Block Windows file sharing leaving the network",
    detail: "SMB carries file shares and credentials. Nothing on a home LAN has a reason to speak it to the Internet, and it is the port ransomware scanners look for.",
    protocol: "tcp",
    port: 445,
    direction: "forward",
  },
  {
    id: "netbios",
    title: "Block NetBIOS name broadcasts",
    detail: "The legacy Windows naming service leaks host and workgroup names. It belongs on the LAN, never upstream.",
    protocol: "udp",
    port: 137,
    direction: "forward",
  },
  {
    id: "telnet",
    title: "Block Telnet",
    detail: "Telnet sends passwords in clear text. Outbound Telnet from a home network is almost always a compromised device reaching for another one.",
    protocol: "tcp",
    port: 23,
    direction: "forward",
  },
  {
    id: "smtp",
    title: "Block direct outbound mail",
    detail: "Only a mail server needs to open SMTP itself. Blocking it stops a compromised device relaying spam under your address, which is also why most ISPs block it.",
    caution: "Turn this off if you run your own mail server on the LAN.",
    protocol: "tcp",
    port: 25,
    direction: "forward",
  },
  {
    id: "ftp",
    title: "Block plain FTP",
    detail: "Unencrypted FTP exposes credentials and file contents in transit. SFTP and HTTPS replace it.",
    protocol: "tcp",
    port: 21,
    direction: "forward",
  },
  {
    id: "rdp",
    title: "Block Remote Desktop leaving the network",
    detail: "Outbound RDP from a home LAN is rarely deliberate. Reach your own machines over WireGuard instead.",
    protocol: "tcp",
    port: 3389,
    direction: "forward",
  },
  {
    id: "dns",
    title: "Force devices onto the router DNS",
    detail: "Blocks devices from talking to outside resolvers directly, so smart TVs and phones with a hardcoded 8.8.8.8 fall back to the router. This is what makes DNS filtering actually apply.",
    caution: "A device that refuses to fall back will lose name resolution.",
    protocol: "udp",
    port: 53,
    direction: "forward",
  },
  {
    id: "dot",
    title: "Block DNS-over-TLS",
    detail: "The other way a device escapes your resolver. Closing 853 alongside plain DNS closes the common bypass pair.",
    protocol: "tcp",
    port: 853,
    direction: "forward",
  },
  {
    id: "quic",
    title: "Block QUIC",
    detail: "QUIC carries HTTP/3 over UDP 443 and hides DNS-over-HTTPS inside it. Browsers fall back to TCP TLS automatically, which keeps filtering effective.",
    caution: "Slightly slower video start on some sites; a few apps dislike the fallback.",
    protocol: "udp",
    port: 443,
    direction: "forward",
  },
  {
    id: "plex-relay",
    title: "Block Plex remote relay",
    detail: "Stops a Plex server streaming out through Plex's relay when you only ever watch it at home or over WireGuard. Local playback on the LAN is untouched.",
    caution: "Turn this off if you rely on plex.tv remote access.",
    protocol: "tcp",
    port: 32400,
    direction: "forward",
  },
];

// The rule name is the link between a preset and the rule it created, so
// turning a preset off never removes a rule the operator wrote by hand.
export function ruleNameFor(preset: Preset) {
  return `${preset.title} (preset)`;
}

type Props = {
  config: RouterConfig;
  busy: boolean;
  applyConfig: (mutate: (next: RouterConfig) => void, success: string) => Promise<boolean>;
};

export default function FirewallPresets({ config, busy, applyConfig }: Props) {
  const rules: FirewallCustomRule[] = config.firewall.custom_rules || [];
  const [selected, setSelected] = useState<Record<string, boolean>>({});

  const activeRule = (preset: Preset) =>
    rules.find((rule) => rule.name === ruleNameFor(preset) && rule.enabled);

  const enablePreset = async (preset: Preset) => {
    const applied = await applyConfig((next) => {
      const name = ruleNameFor(preset);
      const existing = (next.firewall.custom_rules || []).find((rule) => rule.name === name);
      if (existing) {
        next.firewall = {
          ...next.firewall,
          custom_rules: (next.firewall.custom_rules || []).map((rule) =>
            rule.name === name ? { ...rule, enabled: true } : rule,
          ),
        };
        return;
      }
      next.firewall = {
        ...next.firewall,
        custom_rules: [
          ...(next.firewall.custom_rules || []),
          {
            id: `preset-${preset.id}-${Date.now().toString(36)}`,
            name,
            action: "deny",
            direction: preset.direction,
            protocol: preset.protocol,
            src_ip: "",
            dst_port: preset.port,
            enabled: true,
          },
        ],
      };
    }, `${preset.title} is on.`);
    // Keep the tick when the write did not land: the error banner sits at the
    // top of the page, so silently clearing the row leaves no local trace of
    // what the operator just tried.
    if (applied) {
      setSelected((current) => ({ ...current, [preset.id]: false }));
    }
  };

  const disablePreset = (preset: Preset) => {
    applyConfig((next) => {
      const name = ruleNameFor(preset);
      next.firewall = {
        ...next.firewall,
        custom_rules: (next.firewall.custom_rules || []).filter((rule) => rule.name !== name),
      };
    }, `${preset.title} is off.`);
  };

  const activeCount = PRESETS.filter((preset) => activeRule(preset)).length;

  return (
    <article className="card table-card firewall-presets">
      <div className="card-title-row">
        <div>
          <h3>Suggested rules</h3>
          <p>
            Common hardening rules, ready to switch on. Each one blocks a protocol leaving the
            local network; nothing here opens the router to the Internet.
          </p>
        </div>
        <span className="quiet-meta">{activeCount} of {PRESETS.length} on</span>
      </div>

      <p className="form-note firewall-presets-note">
        To reach a service at home from outside — a Plex library, a NAS — WAN stays closed by
        design and you add a <strong>tunnel port forward</strong> for that host on this page
        instead. These presets only restrict what may leave the LAN.
      </p>

      <div className="elegant-table-container">
        <table className="elegant-device-table firewall-presets-table">
          <colgroup>
            <col className="firewall-presets-col-select" />
            <col />
            <col className="elegant-col-mac" />
            <col className="firewall-presets-col-state" />
            <col className="elegant-col-actions" />
          </colgroup>
          <thead>
            <tr>
              <th><span className="visually-hidden">Select</span></th>
              <th>Rule</th>
              <th>Match</th>
              <th>State</th>
              <th className="elegant-th-actions">Action</th>
            </tr>
          </thead>
          <tbody>
            {PRESETS.map((preset) => {
              const on = Boolean(activeRule(preset));
              const checked = on || Boolean(selected[preset.id]);
              return (
                <tr className={on ? "is-preset-on" : ""} key={preset.id}>
                  <td className="firewall-presets-select">
                    <input
                      aria-label={`Select ${preset.title}`}
                      checked={checked}
                      disabled={on || busy}
                      onChange={(event) => setSelected((current) => ({ ...current, [preset.id]: event.target.checked }))}
                      type="checkbox"
                    />
                  </td>
                  <td className="firewall-presets-rule">
                    <strong>{preset.title}</strong>
                    <small>{preset.detail}</small>
                    {preset.caution && <small className="firewall-presets-caution">{preset.caution}</small>}
                  </td>
                  <td className="elegant-cell-mac">
                    <code>{preset.protocol.toUpperCase()} {preset.port}</code>
                    <small>{preset.direction === "forward" ? "leaving the LAN" : "to the router"}</small>
                  </td>
                  <td>
                    <span className={on ? "preset-state is-on" : "preset-state"}>
                      <i aria-hidden="true" />
                      {on ? "Active" : "Off"}
                    </span>
                  </td>
                  <td className="elegant-cell-actions">
                    <div className="device-row-actions">
                      {on ? (
                        <button
                          className="button secondary small"
                          disabled={busy}
                          onClick={() => disablePreset(preset)}
                          type="button"
                        >
                          Turn off
                        </button>
                      ) : (
                        <button
                          className="button primary small"
                          disabled={busy || !selected[preset.id]}
                          onClick={() => void enablePreset(preset)}
                          title={selected[preset.id] ? undefined : "Tick the rule first"}
                          type="button"
                        >
                          Turn on
                        </button>
                      )}
                    </div>
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </article>
  );
}
