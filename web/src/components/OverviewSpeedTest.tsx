import { useEffect, useRef, useState } from "react";
import { apiFetch, responseError } from "../lib/api";

const STORAGE_KEY = "minimalrouter:wan-speed-estimate";
type Measurement = { download_mbps: number; upload_mbps: number; measured_at?: number };

function validMeasurement(value: unknown): value is Measurement {
  if (!value || typeof value !== "object") return false;
  const sample = value as Measurement;
  return typeof sample.download_mbps === "number" && Number.isFinite(sample.download_mbps) && sample.download_mbps > 0
    && typeof sample.upload_mbps === "number" && Number.isFinite(sample.upload_mbps) && sample.upload_mbps > 0;
}

function readMeasurement(): Measurement | null {
  try {
    const value: unknown = JSON.parse(localStorage.getItem(STORAGE_KEY) || "null");
    return validMeasurement(value) ? value : null;
  } catch { return null; }
}

export default function OverviewSpeedTest({ connected }: { connected: boolean }) {
  const [measurement, setMeasurement] = useState(readMeasurement);
  const [running, setRunning] = useState(false);
  const [error, setError] = useState("");
  const controller = useRef<AbortController | null>(null);
  useEffect(() => {
    const refresh = () => setMeasurement(readMeasurement());
    window.addEventListener("minimalrouter:wan-speed-estimate", refresh);
    return () => { controller.current?.abort(); window.removeEventListener("minimalrouter:wan-speed-estimate", refresh); };
  }, []);

  const measure = async () => {
    if (controller.current || !connected) return;
    const request = new AbortController();
    controller.current = request;
    setRunning(true);
    setError("");
    try {
      const response = await apiFetch("/api/v1/qos/speedtest", { method: "POST", signal: request.signal });
      if (!response.ok) throw new Error(await responseError(response, "Speed test unavailable. Try again."));
      const body: unknown = await response.json();
      if (!validMeasurement(body)) throw new Error("No valid speed measurement returned. Try again.");
      if (request.signal.aborted) return;
      const result = { download_mbps: body.download_mbps, upload_mbps: body.upload_mbps, measured_at: Date.now() };
      setMeasurement(result);
      try {
        localStorage.setItem(STORAGE_KEY, JSON.stringify(result));
        window.dispatchEvent(new Event("minimalrouter:wan-speed-estimate"));
      } catch { /* A measurement remains usable when storage is blocked. */ }
    } catch (failure) {
      if (!request.signal.aborted) setError(failure instanceof Error ? failure.message : "Speed test unavailable. Try again.");
    } finally {
      if (!request.signal.aborted) { controller.current = null; setRunning(false); }
    }
  };

  const measuredAt = typeof measurement?.measured_at === "number" ? new Date(measurement.measured_at) : null;
  const timestamp = measuredAt && Number.isFinite(measuredAt.getTime()) ? measuredAt.toLocaleString() : "Time unavailable";
  return <div className="overview-speed-test">
    <dt>Line speed</dt>
    <dd className={measurement ? undefined : "is-info"} aria-live="polite">{running ? "Testing…" : measurement ? `${measurement.download_mbps.toFixed(0)} / ${measurement.upload_mbps.toFixed(0)}` : "Not measured"}</dd>
    <small title={measurement ? `Last measurement: ${timestamp}` : undefined}>{measurement ? "Mbps down / up" : "On-demand measurement"}</small>
    <button type="button" className="overview-inline-action" onClick={() => void measure()} disabled={running || !connected}
      aria-describedby="overview-speed-note">{running ? "Testing…" : measurement ? "Test again ↻" : "Measure speed ↻"}</button>
    <small id="overview-speed-note" className="overview-speed-note">{!connected ? "Connect WAN to measure." : "Uses Internet data; briefly bypasses QoS."}</small>
    {error && <small role="alert" className="overview-speed-error">{error}{measurement ? " Previous result retained." : ""}</small>}
  </div>;
}
