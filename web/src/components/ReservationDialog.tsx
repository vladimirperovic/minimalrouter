import { useEffect, useRef, useState, type FormEvent } from "react";
import type { RouterConfig, StaticLease } from "../api-types";
import { apiFetch, responseError } from "../lib/api";
import { previewAndApplyConfig, readConfiguration } from "../lib/configuration";
import {
  addressInLAN,
  assertUnchanged,
  type AddressAssessment,
  type Lease,
} from "../lib/network";
import {
  insidePool,
  liveLeaseConflictMessage,
  reservationConflictMessage,
  reservationHostnameMessage,
  suggestReservationAddress,
} from "./deviceReservation";

type Props = {
  config: RouterConfig;
  leases: Lease[];
  original?: StaticLease;
  device?: { mac: string; hostname?: string; ip_address: string };
  onClose: () => void;
  onSaved: () => void;
};

export default function ReservationDialog({
  config,
  leases,
  original,
  device,
  onClose,
  onSaved,
}: Props) {
  const target = original || device;
  const [hostname, setHostname] = useState(target?.hostname || "");
  const [mac, setMac] = useState(target?.mac || "");
  const [ip, setIP] = useState(
    original?.ip_address ||
      suggestReservationAddress(
        config.lan.ip_address,
        config.dhcp.range_start,
        [
          ...leases.map((item) => item.ip_address),
          ...(config.dhcp.static_leases || []).map((item) => item.ip_address),
          ...(config.dns?.records || []).map((item) => item.ip),
        ],
        config.lan.cidr,
        config.dhcp.range_end,
      ),
  );
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [check, setCheck] = useState<AddressAssessment | null>(null);
  const [checking, setChecking] = useState(false);
  const dialog = useRef<HTMLDivElement>(null);
  const checkController = useRef<AbortController | null>(null);
  const title = original
    ? `Edit reservation for ${original.hostname || original.mac}`
    : device
      ? `Reserve an IP for ${device.hostname || "this device"}`
      : "Reserve an address";

  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null;
    dialog.current?.querySelector<HTMLInputElement>("input")?.focus();
    return () => {
      checkController.current?.abort();
      previous?.focus();
    };
  }, []);
  useEffect(() => {
    // Disabling the submit button during preview can leave focus on <body>.
    // Return it to the dialog when a cancelled preview keeps the draft open.
    if (!busy && !dialog.current?.contains(document.activeElement))
      dialog.current?.querySelector<HTMLInputElement>("input")?.focus();
  }, [busy]);
  const validate = (candidate: RouterConfig, observed: Lease[]) =>
    reservationHostnameMessage(hostname) ||
    reservationConflictMessage(
      ip,
      mac,
      (candidate.dhcp.static_leases || []).filter(
        (item) => item.id !== original?.id,
      ),
    ) ||
    addressInLAN(ip, candidate) ||
    (insidePool(ip, candidate.dhcp.range_start, candidate.dhcp.range_end)
      ? "Choose an address outside the dynamic DHCP pool."
      : "") ||
    liveLeaseConflictMessage(ip, mac, observed);
  const localError = validate(config, leases);
  const change = (setter: (value: string) => void, value: string) => {
    checkController.current?.abort();
    setChecking(false);
    setCheck(null);
    setError("");
    setter(value);
  };
  const checkAddress = async () => {
    checkController.current?.abort();
    const controller = new AbortController();
    checkController.current = controller;
    setChecking(true);
    setError("");
    try {
      const query = new URLSearchParams({
        ip,
        mac,
        exclude_reservation_id: original?.id || "",
      });
      const response = await apiFetch(
        `/api/v1/network/address-check?${query}`,
        { signal: controller.signal },
      );
      if (!response.ok)
        throw new Error(
          await responseError(response, "Address check unavailable"),
        );
      const value = (await response.json()) as AddressAssessment;
      if (!controller.signal.aborted) setCheck(value);
    } catch (e) {
      if (!controller.signal.aborted)
        setError(e instanceof Error ? e.message : "Address check unavailable");
    } finally {
      if (!controller.signal.aborted) setChecking(false);
    }
  };
  const save = async (event: FormEvent) => {
    event.preventDefault();
    if (localError || busy) return;
    setBusy(true);
    setError("");
    checkController.current?.abort();
    try {
      const next = await readConfiguration({ cache: "reload" });
      if (original)
        assertUnchanged(
          original,
          next.dhcp.static_leases?.find((item) => item.id === original.id),
        );
      const issue = validate(next, leases);
      if (issue) throw new Error(issue);
      const response = await apiFetch(
        `/api/v1/network/address-check?${new URLSearchParams({ ip, mac, exclude_reservation_id: original?.id || "" })}`,
      );
      if (!response.ok)
        throw new Error(
          await responseError(
            response,
            "Cannot refresh address conflicts. Try again before saving.",
          ),
        );
      const assessment = (await response.json()) as AddressAssessment;
      if (
        !["conflict", "unknown", "no_known_conflict"].includes(assessment.state)
      )
        throw new Error(
          "Address check returned incomplete data. Refresh and try again.",
        );
      if (assessment.revision !== next.revision)
        throw new Error(
          "Configuration changed during validation. Your draft is kept; review and try again.",
        );
      if (assessment.state === "conflict")
        throw new Error(assessment.conflicts.join(" "));
      // Unknown lease observation is disclosed; it must never become a claim
      // that an unobserved address is free. Configuration validation still runs.
      if (
        assessment.state === "unknown" &&
        !window.confirm(
          "Current DHCP lease data is unavailable or incomplete. This address has not been verified against all leases. Continue with the configuration preview?",
        )
      )
        return;
      const reservation: StaticLease = {
        id: original?.id || `lease-${crypto.randomUUID()}`,
        hostname: hostname.trim(),
        mac: mac.trim().toLowerCase(),
        ip_address: ip.trim(),
      };
      next.dhcp.static_leases = original
        ? (next.dhcp.static_leases || []).map((item) =>
            item.id === original.id ? reservation : item,
          )
        : [...(next.dhcp.static_leases || []), reservation];
      const result = await previewAndApplyConfig(next);
      if (result.cancelled) return;
      onSaved();
      onClose();
    } catch (e) {
      setError(
        e instanceof Error ? e.message : "Reservation could not be saved",
      );
    } finally {
      setBusy(false);
    }
  };
  return (
    <div
      className="device-reservation-backdrop"
      onMouseDown={(e) => {
        if (!busy && e.target === e.currentTarget) onClose();
      }}
    >
      <div
        ref={dialog}
        className="device-reservation-dialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby="reservation-title"
        onKeyDown={(e) => {
          if (e.key === "Escape" && !busy) {
            e.stopPropagation();
            onClose();
          }
          if (e.key === "Tab") {
            const nodes = Array.from(
              dialog.current?.querySelectorAll<HTMLElement>(
                "button:not(:disabled), input:not(:disabled), a[href]",
              ) || [],
            );
            const first = nodes[0],
              last = nodes[nodes.length - 1];
            if (e.shiftKey && document.activeElement === first) {
              e.preventDefault();
              last?.focus();
            } else if (!e.shiftKey && document.activeElement === last) {
              e.preventDefault();
              first?.focus();
            }
          }
        }}
      >
        <form onSubmit={save}>
          <div className="device-reservation-header">
            <div>
              <span className="device-reservation-kicker">
                DHCP reservation
              </span>
              <h3 id="reservation-title">{title}</h3>
              <p>
                Give this device a consistent address outside the dynamic pool.
              </p>
            </div>
            <button
              type="button"
              className="device-reservation-close"
              disabled={busy}
              aria-label="Close reservation dialog"
              onClick={onClose}
            >
              ×
            </button>
          </div>
          <div className="device-reservation-fields">
            <label className="device-reservation-field device-reservation-name">
              <span id="reservation-name">Device name</span>
              <input
                aria-labelledby="reservation-name"
                aria-describedby="reservation-name-help"
                value={hostname}
                disabled={busy}
                onChange={(e) => change(setHostname, e.target.value)}
                maxLength={63}
                autoComplete="off"
                placeholder="office-laptop"
              />
              <small id="reservation-name-help">
                Optional hostname: letters, numbers and hyphens.
              </small>
            </label>
            <label className="device-reservation-field">
              <span>MAC address</span>
              <input
                value={mac}
                readOnly={Boolean(device)}
                disabled={busy}
                required
                onChange={(e) => change(setMac, e.target.value)}
                placeholder="02:00:00:00:00:20"
              />
            </label>
            <label className="device-reservation-field">
              <span>Reserved IPv4 address</span>
              <input
                value={ip}
                disabled={busy}
                required
                onChange={(e) => change(setIP, e.target.value.trim())}
                inputMode="decimal"
              />
            </label>
          </div>
          {localError && (
            <p className="device-reservation-message is-error" role="alert">
              {localError}
            </p>
          )}
          {error && (
            <p className="device-reservation-message is-error" role="alert">
              {error}
            </p>
          )}
          {check && (
            <p
              className={`device-reservation-message ${check.state === "conflict" ? "is-error" : ""}`}
              role="status"
            >
              {check.state === "conflict"
                ? check.conflicts.join(" ")
                : check.state === "unknown"
                  ? "Lease data is incomplete or unavailable; availability is unknown."
                  : "No configured or observed DHCP conflict found."}{" "}
              {check.limitation}
            </p>
          )}
          <div className="device-reservation-actions">
            <button
              className="button secondary"
              type="button"
              disabled={busy || checking || Boolean(localError)}
              onClick={() => void checkAddress()}
            >
              {checking ? "Checking…" : "Check address"}
            </button>
            <div>
              <button
                className="button secondary"
                type="button"
                disabled={busy}
                onClick={onClose}
              >
                Cancel
              </button>
              <button
                className="button primary"
                type="submit"
                disabled={busy || Boolean(localError)}
              >
                {busy
                  ? "Saving…"
                  : original
                    ? "Save reservation"
                    : "Reserve IP"}
              </button>
            </div>
          </div>
        </form>
      </div>
    </div>
  );
}
