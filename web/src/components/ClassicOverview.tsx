import { useLayoutEffect, useState } from "react";
import type { ComponentProps } from "react";
import { createPortal } from "react-dom";
import BootActivityPanel from "./BootActivityPanel";
import ClassicOverviewBase from "./ClassicOverviewBase";

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

export default function ClassicOverview(props: ComponentProps<typeof ClassicOverviewBase>) {
  return (
    <>
      <ClassicOverviewBase {...props} />
      <BootActivityPortal
        pppoeEnabled={props.config.wan.enabled}
        wireGuardEnabled={props.config.wireguard.enabled}
      />
    </>
  );
}
