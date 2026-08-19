import { useEffect, useLayoutEffect, useState } from "react";
import type { ComponentProps } from "react";
import { createPortal } from "react-dom";
import { apiFetch } from "../lib/api";
import BootActivityPanel from "./BootActivityPanel";
import ClassicOverviewBase from "./ClassicOverviewBase";

type GatewayInsights = {
  available: boolean;
  sampled_hours: number;
  uptime_percent: number;
  outages: number;
};

function BootActivityPortal({ pppoeEnabled, wireGuardEnabled }: { pppoeEnabled: boolean; wireGuardEnabled: boolean }) {
  const [host, setHost] = useState<HTMLElement | null>(null);

  useLayoutEffect(() => {
    const hero = document.querySelector<HTMLElement>(".classic-dashboard-overview .overview-status-hero");
    if (!hero) return;
    const container = document.createElement("div");
    container.className = "boot-activity-host";
    hero.insertAdjacentElement("afterend", container);
    setHost(container);
    return () => container.remove();
  }, []);

  return host ? createPortal(
    <BootActivityPanel pppoeEnabled={pppoeEnabled} wireGuardEnabled={wireGuardEnabled} />,
    host,
  ) : null;
}

function AvailabilityPortal() {
  const [host, setHost] = useState<HTMLElement | null>(null);
  const [insights, setInsights] = useState<GatewayInsights | null>(null);

  useLayoutEffect(() => {
    const card = document.querySelector<HTMLElement>(".classic-dashboard-overview .overview-wan-card");
    if (!card) return;
    const container = document.createElement("div");
    container.className = "overview-availability-host";
    card.appendChild(container);
    setHost(container);
    return () => container.remove();
  }, []);

  useEffect(() => {
    let active = true;
    const controller = new AbortController();
    const load = async () => {
      try {
        const response = await apiFetch("/api/v1/gateway/insights", { signal: controller.signal });
        if (!response.ok) return;
        const body = await response.json() as GatewayInsights;
        if (active) setInsights(body);
      } catch (error) {
        if ((error as Error).name !== "AbortError") {
          // Availability is advisory; the primary WAN state remains visible.
        }
      }
    };
    void load();
    const timer = window.setInterval(() => void load(), 30_000);
    return () => {
      active = false;
      controller.abort();
      window.clearInterval(timer);
    };
  }, []);

  if (!host) return null;
  let label = "30-day uptime · collecting";
  if (insights?.available && insights.sampled_hours > 0) {
    const outages = `${insights.outages} outage${insights.outages === 1 ? "" : "s"}`;
    if (insights.sampled_hours >= 29 * 24) {
      label = `30 days uptime ${insights.uptime_percent.toFixed(2)}% · ${outages}`;
    } else {
      const coverageDays = Math.max(1, Math.floor(insights.sampled_hours / 24));
      label = `${coverageDays}d coverage ${insights.uptime_percent.toFixed(2)}% · ${outages}`;
    }
  }
  return createPortal(<p className="overview-availability-summary">{label}</p>, host);
}

export default function ClassicOverview(props: ComponentProps<typeof ClassicOverviewBase>) {
  return (
    <>
      <ClassicOverviewBase {...props} />
      <AvailabilityPortal />
      <BootActivityPortal
        pppoeEnabled={props.config.wan.enabled}
        wireGuardEnabled={props.config.wireguard.enabled}
      />
    </>
  );
}
