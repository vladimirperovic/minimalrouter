import { useCallback, useMemo, useRef, useState } from "react";
import type { RouterConfig, StaticLease } from "../api-types";
import { apiFetch, responseError } from "../lib/api";
import { previewAndApplyConfig, readConfiguration } from "../lib/configuration";
import {
  assertUnchanged,
  ipNumber,
  type DeviceInventory,
  type Lease,
} from "../lib/network";
import { useVisiblePolling } from "../lib/useVisiblePolling";
import ReservationDialog from "./ReservationDialog";
import "./DeviceLeasesTable.css";

type Pause = { ip: string; until_unix: number };
type Row = {
  ip: string;
  mac: string;
  name: string;
  lease?: Lease;
  reservation?: StaticLease;
  lastSeen?: number;
  bytes?: number;
  isNew?: boolean;
  conflict?: boolean;
};
type Edit = {
  original?: StaticLease;
  device?: { mac: string; ip_address: string; hostname?: string };
};

export default function NetworkDevices({ config }: { config: RouterConfig }) {
  const [inventory, setInventory] = useState<DeviceInventory | null>(null);
  const [error, setError] = useState("");
  const [actionError, setActionError] = useState("");
  const [notice, setNotice] = useState("");
  const [pauses, setPauses] = useState<Pause[]>([]);
  const [pausesAvailable, setPausesAvailable] = useState(false);
  const [refresh, setRefresh] = useState(0);
  const [search, setSearch] = useState("");
  const [filter, setFilter] = useState("all");
  const [sort, setSort] = useState("name");
  const [page, setPage] = useState(0);
  const [edit, setEdit] = useState<Edit | null>(null);
  const [busy, setBusy] = useState("");
  const [pauseMenu, setPauseMenu] = useState("");
  const epoch = useRef(0);
  const poll = useCallback(async (signal: AbortSignal) => {
    const generation = epoch.current;
    const results = await Promise.allSettled([
      apiFetch("/api/v1/network/devices", { signal }).then(async (response) => {
        if (!response.ok)
          throw new Error(
            await responseError(response, "Device inventory unavailable"),
          );
        return (await response.json()) as DeviceInventory;
      }),
      apiFetch("/api/v1/devices/pauses", { signal }).then(async (response) => {
        if (!response.ok) throw new Error("Internet pause state unavailable");
        return (await response.json()) as { pauses: Pause[] };
      }),
    ]);
    if (signal.aborted || generation !== epoch.current) return;
    if (
      results[0].status === "fulfilled" &&
      Array.isArray(results[0].value.leases) &&
      results[0].value.lease_status &&
      results[0].value.activity
    ) {
      const next = results[0].value;
      setInventory((previous) => ({
        ...next,
        leases: next.lease_status.available
          ? next.leases
          : previous?.leases || [],
      }));
      setError("");
    } else
      setError(
        "Device inventory unavailable. Previously loaded data may be out of date.",
      );
    if (
      results[1].status === "fulfilled" &&
      Array.isArray(results[1].value.pauses)
    ) {
      setPauses(results[1].value.pauses);
      setPausesAvailable(true);
    } else setPausesAvailable(false);
  }, []);
  useVisiblePolling(poll, 30000, true, `${config.revision}:${refresh}`);
  const openEditor = (trigger: HTMLButtonElement, target: Edit) => { trigger.focus(); setEdit(target); };
  const reload = () => {
    epoch.current++;
    setRefresh((n) => n + 1);
  };
  const reservations = config.dhcp.static_leases || [];
  const rows = useMemo(() => {
    const byIP = new Map<string, Row>();
    for (const activity of config.accounting?.enabled &&
    inventory?.activity.available
      ? inventory.activity.devices
      : [])
      byIP.set(activity.address, {
        ip: activity.address,
        mac: "",
        name: "Unknown device",
        lastSeen: activity.last_seen_epoch,
        bytes: activity.month_bytes,
        isNew: activity.new_this_month,
      });
    for (const lease of inventory?.leases || [])
      byIP.set(lease.ip_address, {
        ...byIP.get(lease.ip_address),
        ip: lease.ip_address,
        mac: lease.mac,
        name: lease.hostname || "Unnamed device",
        lease,
      });
    for (const reservation of reservations) {
      const existing = byIP.get(reservation.ip_address);
      byIP.set(reservation.ip_address, {
        ...existing,
        ip: reservation.ip_address,
        mac: reservation.mac,
        name: reservation.hostname || existing?.name || "Unnamed device",
        reservation,
        conflict: Boolean(
          existing?.lease &&
            existing.mac.toLowerCase() !== reservation.mac.toLowerCase(),
        ),
      });
    }
    for (const pause of pauses)
      if (!byIP.has(pause.ip))
        byIP.set(pause.ip, { ip: pause.ip, mac: "", name: "Paused address" });
    return [...byIP.values()];
  }, [inventory, reservations, config.accounting?.enabled, pauses]);
  const filtered = rows
    .filter(
      (row) =>
        `${row.name} ${row.mac} ${row.ip}`
          .toLowerCase()
          .includes(search.toLowerCase()) &&
        (filter === "all" ||
          (filter === "leased" && row.lease) ||
          (filter === "reserved" && row.reservation) ||
          (filter === "history" && !row.lease && !row.reservation) ||
          (filter === "paused" && pauses.some((item) => item.ip === row.ip))),
    )
    .sort((a, b) =>
      sort === "ip"
        ? ipNumber(a.ip) - ipNumber(b.ip)
        : sort === "seen"
          ? (b.lastSeen || 0) - (a.lastSeen || 0)
          : a.name.localeCompare(b.name) || ipNumber(a.ip) - ipNumber(b.ip),
    );
  const maxPage = Math.max(0, Math.ceil(filtered.length / 25) - 1);
  const currentPage = Math.min(page, maxPage);
  const action = async (
    row: Row,
    kind: "wake" | "pause" | "resume",
    seconds = 0,
  ) => {
    epoch.current++;
    setBusy(`${kind}:${row.ip}`);
    setActionError("");
    setNotice("");
    try {
      const response = await apiFetch(
        kind === "wake" ? "/api/v1/network/wol" : `/api/v1/devices/${kind}`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(
            kind === "wake" ? { mac: row.mac } : { ip: row.ip, seconds },
          ),
        },
      );
      if (!response.ok)
        throw new Error(await responseError(response, `${kind} failed`));
      if (kind !== "wake") {
        const body = await response.json();
        if (Array.isArray(body.pauses)) {
          setPauses(body.pauses);
          setPausesAvailable(true);
        }
        setPauseMenu("");
      }
      setNotice(
        kind === "wake"
          ? `Wake packet sent to ${row.name}. This does not confirm the device is awake.`
          : kind === "pause"
            ? `Internet paused for ${row.name}.`
            : `Internet resumed for ${row.name}.`,
      );
      reload();
    } catch (e) {
      setActionError(e instanceof Error ? e.message : "Action failed");
    } finally {
      setBusy("");
    }
  };
  const remove = async (original: StaticLease) => {
    setBusy(`remove:${original.id}`);
    setActionError("");
    try {
      const next = await readConfiguration({ cache: "reload" });
      assertUnchanged(
        original,
        next.dhcp.static_leases?.find((item) => item.id === original.id),
      );
      next.dhcp.static_leases = (next.dhcp.static_leases || []).filter(
        (item) => item.id !== original.id,
      );
      const result = await previewAndApplyConfig(next);
      if (!result.cancelled) {
        setNotice(
          "Reservation removed. An existing lease may remain valid until it expires.",
        );
        reload();
      }
    } catch (e) {
      setActionError(
        e instanceof Error ? e.message : "Reservation could not be removed",
      );
    } finally {
      setBusy("");
    }
  };
  const leaseStatus = inventory?.lease_status;
  const observationUnknown =
    Boolean(error) ||
    !leaseStatus?.available ||
    leaseStatus.truncated ||
    leaseStatus.rejected_entries > 0;
  return (
    <article className="net-card net-devices">
      <header className="net-card-head">
        <div>
          <span className="net-kicker">Your local network</span>
          <h3>Devices & reservations</h3>
          <p>Leases, fixed addresses and recent activity in one place.</p>
        </div>
        <button
          className="button primary"
          disabled={Boolean(busy)}
          onClick={e => openEditor(e.currentTarget, {})}
          type="button"
        >
          Add reservation
        </button>
      </header>
      <div className="net-device-toolbar">
        <label className="net-search">
          <span className="sr-only">Search devices</span>
          <input
            type="search"
            value={search}
            onChange={(e) => {
              setSearch(e.target.value);
              setPage(0);
            }}
            placeholder="Search name, IP or MAC"
          />
        </label>
        <label>
          <span className="sr-only">Filter devices</span>
          <select
            value={filter}
            onChange={(e) => {
              setFilter(e.target.value);
              setPage(0);
            }}
          >
            <option value="all">All devices</option>
            <option value="leased">DHCP leases</option>
            <option value="reserved">Reservations</option>
            <option value="history">History only</option>
            <option value="paused">Internet paused</option>
          </select>
        </label>
        <label>
          <span className="sr-only">Sort devices</span>
          <select value={sort} onChange={(e) => setSort(e.target.value)}>
            <option value="name">Name</option>
            <option value="ip">IP address</option>
            <option value="seen">Last activity</option>
          </select>
        </label>
        <button
          className="button secondary small"
          onClick={reload}
          type="button"
        >
          Refresh
        </button>
      </div>
      {(error || observationUnknown) && (
        <p className="net-notice is-warning" role="status">
          {error ||
            (inventory
              ? "Lease observation is unavailable or incomplete. Counts do not represent the full network."
              : "Loading device inventory…")}
        </p>
      )}
      {!pausesAvailable && (
        <p className="net-notice" role="status">
          Internet pause state unavailable; refresh before changing it.
        </p>
      )}
      {actionError && (
        <p className="net-notice is-error" role="alert">
          {actionError}
        </p>
      )}
      {notice && (
        <p className="net-notice" role="status">
          {notice}
        </p>
      )}
      <div className="net-device-list">
        {filtered.slice(currentPage * 25, currentPage * 25 + 25).map((row) => {
          const pause = pauses.find((item) => item.ip === row.ip);
          return (
            <div className="net-device-row" key={row.ip}>
              <div className="net-device-identity">
                <span className="net-device-symbol" aria-hidden="true">
                  <svg
                    viewBox="0 0 24 24"
                    fill="none"
                    stroke="currentColor"
                    strokeWidth="1.5"
                  >
                    <rect x="3" y="4" width="18" height="13" rx="2" />
                    <path d="M8 21h8m-4-4v4" />
                  </svg>
                </span>
                <div>
                  <strong>{row.name}</strong>
                  <code>{row.ip}</code>
                  <small>{row.mac || "MAC not observed"}</small>
                </div>
              </div>
              <div className="net-device-state">
                <div className="net-tags">
                  {row.conflict && (
                    <span className="is-paused">Address conflict</span>
                  )}
                  {row.reservation && <span>Reserved</span>}
                  {row.lease && (
                    <span>
                      {observationUnknown
                        ? "Previously observed lease"
                        : "DHCP lease"}
                    </span>
                  )}
                  {!row.lease && !row.reservation && <span>History</span>}
                  {pause && <span className="is-paused">Internet paused</span>}
                </div>
                <small>
                  {row.lease
                    ? row.lease.expires_at
                      ? `Lease expires ${new Date(row.lease.expires_at * 1000).toLocaleString()}`
                      : "Lease has no expiry"
                    : row.lastSeen
                      ? `Traffic sampled ${new Date(row.lastSeen * 1000).toLocaleString()}`
                      : "No current DHCP lease"}
                </small>
                {row.lastSeen && (
                  <small>
                    Traffic sampled at this IP{" "}
                    {new Date(row.lastSeen * 1000).toLocaleString()}
                  </small>
                )}
                {row.conflict && (
                  <small>Observed lease belongs to {row.lease?.mac}</small>
                )}
                {pause && (
                  <small>
                    {pause.until_unix
                      ? `Paused until ${new Date(pause.until_unix * 1000).toLocaleTimeString()}`
                      : "Paused until resumed"}
                  </small>
                )}
              </div>
              <div className="net-device-actions">
                {row.mac && !row.conflict && (
                  <button
                    className="button secondary small"
                    disabled={Boolean(busy)}
                    onClick={() => void action(row, "wake")}
                    type="button"
                  >
                    {busy === `wake:${row.ip}` ? "Sending…" : "Wake"}
                  </button>
                )}
                {row.reservation ? (
                  <>
                    <button
                      className="button secondary small"
                      disabled={Boolean(busy)}
                      onClick={e => openEditor(e.currentTarget, { original: row.reservation })}
                      type="button"
                    >
                      Edit
                    </button>
                    <button
                      className="button secondary small danger"
                      disabled={Boolean(busy)}
                      onClick={() => void remove(row.reservation!)}
                      type="button"
                    >
                      Remove
                    </button>
                  </>
                ) : (
                  row.mac && (
                    <button
                      className="button secondary small"
                      disabled={Boolean(busy)}
                      aria-label={`Reserve an IP address for ${row.name}`}
                      onClick={e =>
                        openEditor(e.currentTarget, {
                          device: {
                            mac: row.mac,
                            hostname: row.name,
                            ip_address: row.ip,
                          },
                        })
                      }
                      type="button"
                    >
                      Reserve IP
                    </button>
                  )
                )}
                {pause ? (
                  <button
                    className="button secondary small"
                    disabled={Boolean(busy) || !pausesAvailable}
                    onClick={() => void action(row, "resume")}
                    type="button"
                  >
                    Resume
                  </button>
                ) : (
                  <div className="device-pause-control">
                    <button
                      className="button secondary small"
                      disabled={
                        Boolean(busy) ||
                        observationUnknown ||
                        !pausesAvailable ||
                        row.conflict ||
                        (!row.lease && !row.reservation)
                      }
                      onClick={() =>
                        setPauseMenu(pauseMenu === row.ip ? "" : row.ip)
                      }
                      type="button"
                    >
                      Pause Internet
                    </button>
                    {pauseMenu === row.ip && (
                      <div className="device-pause-menu">
                        {[
                          [900, "15 min"],
                          [3600, "1 hour"],
                          [0, "Until resumed"],
                        ].map(([seconds, label]) => (
                          <button
                            key={seconds}
                            type="button"
                            onClick={() =>
                              void action(row, "pause", Number(seconds))
                            }
                          >
                            {label}
                          </button>
                        ))}
                      </div>
                    )}
                  </div>
                )}
              </div>
            </div>
          );
        })}
        {filtered.length === 0 && (
          <p className="net-empty">
            {error || observationUnknown
              ? "Device data is incomplete. Refresh to check again."
              : search || filter !== "all"
                ? "No devices match these filters."
                : "No leases, reservations or recent traffic observed."}
          </p>
        )}
      </div>
      <footer className="net-device-footer">
        <span>
          {filtered.length} matching devices ·{" "}
          {leaseStatus?.collected_at
            ? `Observed ${new Date(leaseStatus.collected_at).toLocaleTimeString()}`
            : "Observation time unavailable"}
          <small>
            A lease is not proof a device is online. Traffic history requires
            accounting.
          </small>
        </span>
        {maxPage > 0 && (
          <div>
            <button
              className="button secondary small"
              disabled={currentPage === 0}
              onClick={() => setPage(currentPage - 1)}
              type="button"
            >
              Previous
            </button>
            <span>
              {currentPage + 1} / {maxPage + 1}
            </span>
            <button
              className="button secondary small"
              disabled={currentPage >= maxPage}
              onClick={() => setPage(currentPage + 1)}
              type="button"
            >
              Next
            </button>
          </div>
        )}
      </footer>
      {edit && (
        <ReservationDialog
          config={config}
          leases={inventory?.leases || []}
          {...edit}
          onClose={() => setEdit(null)}
          onSaved={reload}
        />
      )}
    </article>
  );
}
