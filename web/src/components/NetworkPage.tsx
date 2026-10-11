import {
  useCallback,
  useEffect,
  useState,
  type ReactNode,
  type FormEvent,
} from "react";
import type { ApplianceHealth, RouterConfig } from "../api-types";
import { apiFetch, responseError } from "../lib/api";
import { useVisiblePolling } from "../lib/useVisiblePolling";
import {
  addressInLAN,
  assertUnchanged,
  ipNumber,
  managementURL,
  sameValue,
  subnetBounds,
  type LANStatus,
  type NetworkInterface,
} from "../lib/network";
import { isValidIPv4 } from "./deviceReservation";
import NetworkDevices from "./NetworkDevices";
import "./NetworkPage.css";

type Apply = (
  mutate: (next: RouterConfig) => void,
  success: string,
) => Promise<boolean>;
type Props = {
  config: RouterConfig;
  busy: boolean;
  applyConfig: Apply;
  health?: ApplianceHealth | null;
};

function SectionEditor<T>({
  title,
  intro,
  icon,
  value,
  read,
  write,
  validate,
  apply,
  busy,
  label,
  children,
}: {
  title: string;
  intro: string;
  icon: string;
  value: T;
  read: (cfg: RouterConfig) => T;
  write: (cfg: RouterConfig, draft: T) => void;
  validate?: (draft: T) => string;
  apply: Apply;
  busy: boolean;
  label: string;
  children: (draft: T, change: (draft: T) => void) => ReactNode;
}) {
  const [base, setBase] = useState(value);
  const [draft, setDraft] = useState(value);
  const [error, setError] = useState("");
  const [saved, setSaved] = useState(false);
  const dirty = !sameValue(draft, base);
  const changedElsewhere = !sameValue(value, base) && dirty;
  useEffect(() => {
    if (!sameValue(value, base) && (!dirty || sameValue(value, draft))) {
      setBase(value);
      setDraft(value);
    }
    // A canonical refresh must never discard an edited draft.
  }, [value, base, dirty, draft]);
  const change = (next: T) => {
    setDraft(next);
    setSaved(false);
    setError("");
  };
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setError("");
    const issue = validate?.(draft);
    if (issue) {
      setError(issue);
      return;
    }
    const ok = await apply((next) => {
      assertUnchanged(base, read(next));
      write(next, draft);
    }, `${title} settings saved.`);
    if (ok) {
      setBase(draft);
      setSaved(true);
    }
  };
  return (
    <form className="net-card net-editor" onSubmit={submit}>
      <header className="net-card-head">
        <span className="net-card-icon" aria-hidden="true">
          {icon}
        </span>
        <div>
          <h3>{title}</h3>
          <p>{intro}</p>
        </div>
      </header>
      <div className="net-card-body">
        {children(draft, change)}
        {changedElsewhere && (
          <div className="net-notice is-warning" role="alert">
            These settings changed in another session. Your draft is kept.
            <button
              type="button"
              className="button secondary small"
              onClick={() => {
                setBase(value);
                setDraft(value);
                setError("");
              }}
            >
              Load current settings
            </button>
          </div>
        )}
        {error && (
          <p className="net-notice is-error" role="alert">
            {error}
          </p>
        )}
      </div>
      <footer className="net-card-footer">
        <span role="status">
          {saved
            ? "Saved"
            : dirty
              ? "Unsaved changes"
              : "Current configuration"}
        </span>
        <button
          className="button primary"
          type="submit"
          disabled={busy || changedElsewhere}
        >
          {busy ? "Working…" : label}
        </button>
      </footer>
    </form>
  );
}

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <label className="field">
      <span>{label}</span>
      {children}
    </label>
  );
}

function AddressMap({
  config,
  status,
}: {
  config: RouterConfig;
  status: LANStatus | null;
}) {
  const bounds = subnetBounds(config.lan.cidr);
  if (!bounds) return null;
  const span = bounds.last - bounds.first + 1;
  const position = (ip: string) =>
    Math.max(0, Math.min(100, ((ipNumber(ip) - bounds.first) / span) * 100));
  const start = position(config.dhcp.range_start),
    end = position(config.dhcp.range_end);
  const percent =
    status && status.pool.capacity
      ? Math.min(
          100,
          (status.pool.observed_leases / status.pool.capacity) * 100,
        )
      : 0;
  return (
    <article className="net-card net-address-space">
      <header className="net-card-head">
        <div>
          <span className="net-kicker">Address space</span>
          <h3>A place for every device</h3>
          <p>Keep fixed addresses separate from automatic allocation.</p>
        </div>
        <span className="net-chip">{config.lan.cidr}</span>
      </header>
      <div className="net-card-body">
        <svg
          className="net-address-track"
          viewBox="0 0 1000 30"
          preserveAspectRatio="none"
          role="img"
          aria-label={`DHCP pool ${config.dhcp.range_start} to ${config.dhcp.range_end}; gateway ${config.lan.ip_address}; ${config.dhcp.static_leases?.length || 0} reservations`}
        >
          <rect
            className="net-pool-range"
            x={start * 10}
            width={Math.max(0.4, end - start + 100 / span) * 10}
            height={30}
          />
          <rect
            className="net-address-pin is-gateway"
            x={position(config.lan.ip_address) * 10}
            y={3}
            width={4}
            height={24}
          >
            <title>Gateway {config.lan.ip_address}</title>
          </rect>
          {(config.dhcp.static_leases || []).map((item) => (
            <rect
              key={item.id}
              className="net-address-pin"
              x={position(item.ip_address) * 10}
              y={6}
              width={3}
              height={18}
            >
              <title>
                {item.hostname || item.mac} · {item.ip_address}
              </title>
            </rect>
          ))}
        </svg>
        <div className="net-map-legend">
          <span>
            <i className="is-gateway" />
            Gateway
          </span>
          <span>
            <i />
            Reservation
          </span>
          <span>
            <i className="is-pool" />
            DHCP pool
          </span>
          <small>Unmarked does not mean verified free.</small>
        </div>
        <div className="net-pool-summary">
          <div>
            <strong>
              {status?.pool.free == null ? "Unknown" : status.pool.free}
            </strong>
            <span>Unallocated pool addresses</span>
          </div>
          <div>
            <strong>
              {status ? status.pool.observed_leases : "—"}
              <small> / {status?.pool.capacity ?? "—"}</small>
            </strong>
            <span>Observed DHCP allocation</span>
          </div>
          <div>
            <strong>{config.dhcp.static_leases?.length || 0}</strong>
            <span>Fixed reservations</span>
          </div>
        </div>
        <meter
          className="net-pool-meter"
          min={0}
          max={100}
          value={percent}
          aria-label="Observed pool allocation"
        />
      </div>
    </article>
  );
}

export default function NetworkPage({
  config,
  busy,
  applyConfig,
  health,
}: Props) {
  const [status, setStatus] = useState<LANStatus | null>(null);
  const [interfaces, setInterfaces] = useState<NetworkInterface[]>([]);
  const [error, setError] = useState("");
  const [refresh, setRefresh] = useState(0);
  const load = useCallback(async (signal: AbortSignal) => {
    const results = await Promise.allSettled([
      apiFetch("/api/v1/network/lan", { signal }).then(async (r) => {
        if (!r.ok)
          throw new Error(await responseError(r, "LAN status unavailable"));
        return (await r.json()) as LANStatus;
      }),
      apiFetch("/api/v1/system/interfaces", { signal }).then(async (r) => {
        if (!r.ok) throw new Error("Interface discovery unavailable");
        return (await r.json()) as { interfaces: NetworkInterface[] };
      }),
    ]);
    if (signal.aborted) return;
    if (results[0].status === "fulfilled" && results[0].value.pool) {
      setStatus(results[0].value);
      setError("");
    } else
      setError(
        "LAN observation unavailable. Previously loaded data may be out of date.",
      );
    setInterfaces(
      results[1].status === "fulfilled" &&
        Array.isArray(results[1].value.interfaces)
        ? results[1].value.interfaces
        : [],
    );
  }, []);
  useVisiblePolling(load, 30000, true, `${config.revision}:${refresh}`);
  const link = interfaces.find((item) => item.name === config.lan.interface);
  const linkLabel = !link
    ? "Unknown"
    : !link.up
      ? "Down"
      : !link.carrier_known
        ? "Carrier unknown"
        : link.carrier
          ? "Connected"
          : "No carrier";
  const dnsCheck = health?.checks.find((item) => item.id === "dns_dhcp");
  const lanRead = (cfg: RouterConfig) => cfg.lan;
  const dhcpRead = (cfg: RouterConfig) => ({
    enabled: cfg.dhcp.enabled,
    range_start: cfg.dhcp.range_start,
    range_end: cfg.dhcp.range_end,
    lease_time: cfg.dhcp.lease_time,
  });
  const dnsRead = (cfg: RouterConfig) => ({
    resolvers: (cfg.dhcp.dns_servers || []).join(", "),
  });
  const wanRead = (cfg: RouterConfig) => ({ ...cfg.wan, password: "" });
  const recordsRead = (cfg: RouterConfig) => cfg.dns?.records || [];
  const common = { apply: applyConfig, busy };
  return (
    <section className="dashboard-section net-workspace" id="network">
      <header className="net-hero">
        <div>
          <span className="net-kicker">Connectivity</span>
          <h2>LAN & DHCP</h2>
          <p>
            Your local network, clearly mapped.
            <br />
            Manage addresses, devices and the DNS they use.
          </p>
        </div>
        <div className="net-hero-side">
          <span className="net-chip">
            {config.lan.interface} · {linkLabel}
          </span>
          <button
            className="button secondary"
            onClick={() => setRefresh((n) => n + 1)}
            type="button"
          >
            Refresh status
          </button>
        </div>
        <dl className="net-facts">
          <div>
            <dt>LAN gateway</dt>
            <dd>{config.lan.ip_address}</dd>
            <small>{config.lan.cidr}</small>
          </div>
          <div>
            <dt>DHCP allocation</dt>
            <dd>{config.dhcp.enabled ? "Enabled" : "Disabled"}</dd>
            <small>
              {config.dhcp.range_start}–{config.dhcp.range_end.split(".").pop()}
            </small>
          </div>
          <div>
            <dt>DNS health</dt>
            <dd>
              {dnsCheck?.state === "healthy"
                ? "Resolving"
                : dnsCheck?.state === "degraded"
                  ? "Needs attention"
                  : "Unknown"}
            </dd>
            <small>{dnsCheck?.summary || "No current DNS observation"}</small>
          </div>
        </dl>
      </header>
      {error && (
        <p className="net-notice is-warning" role="status">
          {error}
        </p>
      )}
      {status?.warnings.map((warning) => (
        <p className="net-notice is-warning" role="status" key={warning}>
          {warning}
        </p>
      ))}
      <AddressMap config={config} status={error ? null : status} />
      <div className="net-config-grid">
        <SectionEditor
          {...common}
          title="Local network"
          intro="The address your devices use to reach the router."
          icon="⌘"
          value={lanRead(config)}
          read={lanRead}
          write={(next, draft) => {
            next.lan = {
              ...draft,
              cidr: `${draft.ip_address}/${next.lan.cidr.split("/")[1]}`,
            };
          }}
          validate={(draft) => addressInLAN(draft.ip_address, config, true)}
          label="Save LAN"
        >
          {(draft, change) => (
            <>
              <div className="form-grid two">
                <Field label="Interface">
                  <input
                    name="lan_interface"
                    value={draft.interface}
                    readOnly
                    title="Changing the LAN interface requires the local recovery console"
                  />
                </Field>
                <Field label="Prefix">
                  <input
                    value={`/${config.lan.cidr.split("/")[1]}`}
                    readOnly
                    title="Changing the LAN subnet requires the local recovery console"
                  />
                </Field>
              </div>
              <Field label="Gateway IPv4">
                <input
                  name="lan_ip"
                  value={draft.ip_address}
                  onChange={(e) =>
                    change({ ...draft, ip_address: e.target.value.trim() })
                  }
                  required
                />
              </Field>
              <p className="net-help">
                Interface and subnet changes require the local recovery console.
              </p>
              {draft.ip_address !== config.lan.ip_address && (
                <p className="net-notice">
                  After applying, open{" "}
                  <strong>{managementURL({ ...config, lan: draft })}</strong>{" "}
                  and confirm access before automatic rollback.
                </p>
              )}
            </>
          )}
        </SectionEditor>
        <SectionEditor
          {...common}
          title="DHCP server"
          intro="Allocate addresses automatically, within a defined pool."
          icon="↔"
          value={dhcpRead(config)}
          read={dhcpRead}
          write={(next, draft) => {
            next.dhcp = { ...next.dhcp, ...draft };
          }}
          validate={(draft) => {
            const startError = addressInLAN(draft.range_start, config),
              endError = addressInLAN(draft.range_end, config);
            if (startError || endError) return startError || endError;
            if (ipNumber(draft.range_start) > ipNumber(draft.range_end))
              return "Pool start must not be after pool end.";
            if (
              ipNumber(config.lan.ip_address) >= ipNumber(draft.range_start) &&
              ipNumber(config.lan.ip_address) <= ipNumber(draft.range_end)
            )
              return "The pool cannot contain the gateway.";
            const duration = /^(\d+)([mh])$/.exec(draft.lease_time);
            const minutes = duration
              ? Number(duration[1]) * (duration[2] === "h" ? 60 : 1)
              : 0;
            return minutes < 1 || minutes > 10080
              ? "Lease duration must be 1m–168h."
              : "";
          }}
          label="Save DHCP"
        >
          {(draft, change) => (
            <>
              <label className="net-toggle">
                <span>
                  <strong>Automatic allocation</strong>
                  <small>DNS continues to work when DHCP is off.</small>
                </span>
                <input
                  type="checkbox"
                  checked={draft.enabled}
                  onChange={(e) =>
                    change({ ...draft, enabled: e.target.checked })
                  }
                />
              </label>
              <div className="form-grid two">
                <Field label="Range start">
                  <input
                    name="dhcp_start"
                    value={draft.range_start}
                    onChange={(e) =>
                      change({ ...draft, range_start: e.target.value.trim() })
                    }
                    required
                  />
                </Field>
                <Field label="Range end">
                  <input
                    name="dhcp_end"
                    value={draft.range_end}
                    onChange={(e) =>
                      change({ ...draft, range_end: e.target.value.trim() })
                    }
                    required
                  />
                </Field>
              </div>
              <Field label="Lease time">
                <input
                  name="lease_time"
                  value={draft.lease_time}
                  onChange={(e) =>
                    change({ ...draft, lease_time: e.target.value })
                  }
                  pattern="[0-9]+[mh]"
                  required
                  title="1m–168h, for example 30m or 12h"
                />
              </Field>
              {!draft.enabled && config.dhcp.enabled && (
                <p className="net-notice is-warning">
                  Devices may lose access when existing leases expire. Keep
                  another address allocation method available.
                </p>
              )}
            </>
          )}
        </SectionEditor>
      </div>
      <div className="net-config-grid">
        <SectionEditor
          {...common}
          title="DNS resolvers"
          intro="Clients ask the router; the router asks these upstreams."
          icon="◎"
          value={dnsRead(config)}
          read={dnsRead}
          write={(next, draft) => {
            next.dhcp.dns_enabled = false;
            next.dhcp.dns_servers = draft.resolvers
              .split(",")
              .map((item) => item.trim())
              .filter(Boolean);
          }}
          validate={(draft) => {
            const values = draft.resolvers
              .split(",")
              .map((item) => item.trim())
              .filter(Boolean);
            return values.length < 1 ||
              values.length > 3 ||
              values.some(
                (item) =>
                  !isValidIPv4(item) ||
                  item === config.lan.ip_address ||
                  item.startsWith("127."),
              ) ||
              new Set(values).size !== values.length
              ? "Enter 1–3 different IPv4 upstream resolvers, excluding the router itself."
              : "";
          }}
          label="Save DNS"
        >
          {(draft, change) => (
            <>
              <div className="net-dns-route">
                <span>Devices</span>
                <i>→</i>
                <strong>{config.lan.ip_address}</strong>
                <i>→</i>
                <span>Upstream DNS</span>
              </div>
              <Field label="Upstream resolvers, comma separated">
                <input
                  name="dns_servers"
                  value={draft.resolvers}
                  onChange={(e) => change({ resolvers: e.target.value })}
                  required
                />
              </Field>
              <p className="net-help">
                DHCP option 6 advertises the router address. Upstreams use plain
                DNS; encrypted upstream DNS is not supported by this appliance.
              </p>
            </>
          )}
        </SectionEditor>
        <SectionEditor
          {...common}
          title="Local DNS names"
          intro="Friendly names for fixed devices and local services."
          icon="↗"
          value={recordsRead(config)}
          read={recordsRead}
          write={(next, draft) => {
            next.dns = { ...next.dns, records: draft };
          }}
          validate={(draft) =>
            draft.some(
              (item) =>
                !isValidIPv4(item.ip) ||
                !item.name ||
                item.name.length > 253 ||
                item.name
                  .split(".")
                  .some(
                    (label) =>
                      !/^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/i.test(label),
                  ),
            )
              ? "Each record needs a valid DNS name and IPv4 address."
              : ""
          }
          label="Save records"
        >
          {(draft, change) => (
            <>
              <div className="net-record-list">
                {draft.map((record, i) => (
                  <div className="net-record-row" key={i}>
                    <Field label="Name">
                      <input
                        name={`dns_record_name_${i}`}
                        value={record.name}
                        placeholder="nas.home.arpa"
                        onChange={(e) =>
                          change(
                            draft.map((item, index) =>
                              index === i
                                ? { ...item, name: e.target.value }
                                : item,
                            ),
                          )
                        }
                        required
                      />
                    </Field>
                    <Field label="IPv4 address">
                      <input
                        name={`dns_record_ip_${i}`}
                        value={record.ip}
                        onChange={(e) =>
                          change(
                            draft.map((item, index) =>
                              index === i
                                ? { ...item, ip: e.target.value.trim() }
                                : item,
                            ),
                          )
                        }
                        required
                      />
                    </Field>
                    <button
                      className="button secondary small"
                      type="button"
                      aria-label={`Remove ${record.name || "record"}`}
                      onClick={() =>
                        change(draft.filter((_, index) => index !== i))
                      }
                    >
                      ×
                    </button>
                  </div>
                ))}
              </div>
              {draft.length === 0 && (
                <p className="net-empty">No local DNS records yet.</p>
              )}
              <button
                className="button secondary small"
                type="button"
                onClick={() => change([...draft, { name: "", ip: "" }])}
              >
                Add record
              </button>
            </>
          )}
        </SectionEditor>
      </div>
      <NetworkDevices config={config} />
      <details className="net-wan">
        <summary>
          <span className="net-card-icon" aria-hidden="true">
            ↗
          </span>
          <span>
            <strong>WAN / PPPoE</strong>
            <small>Uplink credentials and interface settings</small>
          </span>
          <span className="net-chip">
            {config.wan.enabled ? "Enabled" : "Disabled"} ·{" "}
            {config.wan.interface}
          </span>
        </summary>
        <SectionEditor
          {...common}
          title="Internet uplink"
          intro="Changes may briefly interrupt Internet connectivity."
          icon="↗"
          value={wanRead(config)}
          read={wanRead}
          write={(next, draft) => {
            next.wan = {
              ...next.wan,
              ...draft,
              password: draft.password || next.wan.password,
            };
          }}
          label="Save WAN"
        >
          {(draft, change) => (
            <>
              <label className="net-toggle">
                <span>Enable PPPoE WAN</span>
                <input
                  type="checkbox"
                  checked={draft.enabled}
                  onChange={(e) =>
                    change({ ...draft, enabled: e.target.checked })
                  }
                />
              </label>
              <div className="form-grid two">
                <Field label="WAN interface">
                  <input
                    name="wan_interface"
                    list="network-interface-options"
                    value={draft.interface}
                    onChange={(e) =>
                      change({ ...draft, interface: e.target.value })
                    }
                    required
                  />
                  <datalist id="network-interface-options">
                    {interfaces
                      .filter((item) => item.name !== config.lan.interface)
                      .map((item) => (
                        <option key={item.name} value={item.name}>
                          {item.up ? "Up" : "Down"}
                        </option>
                      ))}
                  </datalist>
                </Field>
                <Field label="MTU">
                  <input
                    name="wan_mtu"
                    type="number"
                    min={1280}
                    max={1500}
                    value={draft.mtu}
                    onChange={(e) =>
                      change({ ...draft, mtu: Number(e.target.value) })
                    }
                    required
                  />
                </Field>
                <Field label="PPPoE username">
                  <input
                    name="pppoe_username"
                    value={draft.username}
                    onChange={(e) =>
                      change({ ...draft, username: e.target.value })
                    }
                  />
                </Field>
                <Field label="New PPPoE password">
                  <input
                    name="pppoe_password"
                    type="password"
                    autoComplete="new-password"
                    placeholder="Leave blank to keep stored password"
                    value={draft.password}
                    onChange={(e) =>
                      change({ ...draft, password: e.target.value })
                    }
                  />
                </Field>
              </div>
            </>
          )}
        </SectionEditor>
      </details>
    </section>
  );
}
