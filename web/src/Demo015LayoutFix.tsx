import { useEffect } from "react";
import { isDemoMode } from "./lib/demoApi";

function repairDemoLayout() {
  // The visual-preview layer injects the quick status cards after the WAN card.
  // In the Overview DOM the WAN card is inside a 3-column command grid, so the
  // injected block becomes an unintended third-column child and gets squeezed.
  // Keep the preview cards, but place them after the complete navy hero instead.
  const hero = document.querySelector<HTMLElement>(".classic-dashboard-overview .overview-status-hero");
  const quickStatus = document.querySelector<HTMLElement>(".demo-015-quick-status-grid");
  if (hero && quickStatus && quickStatus.previousElementSibling !== hero) {
    hero.insertAdjacentElement("afterend", quickStatus);
  }

  // Gateway Quality, DNS Filter and other fact-rich pages already have their own
  // navy subpage hero layout. The generic demo heading class turns that grid into
  // a flex row and collapses the fact strip. Exclude those specialized headers.
  document
    .querySelectorAll<HTMLElement>(".dashboard-section-heading.has-facts.demo-page-heading, .dns-filter-heading.has-facts.demo-page-heading")
    .forEach((heading) => heading.classList.remove("demo-page-heading"));
}

export default function Demo015LayoutFix() {
  useEffect(() => {
    if (!isDemoMode) return;

    let scheduled = false;
    const scheduleRepair = () => {
      if (scheduled) return;
      scheduled = true;
      queueMicrotask(() => {
        scheduled = false;
        repairDemoLayout();
      });
    };

    repairDemoLayout();

    const root = document.getElementById("root") ?? document.body;
    const observer = new MutationObserver(scheduleRepair);
    observer.observe(root, { childList: true, subtree: true });
    window.addEventListener("hashchange", scheduleRepair);

    return () => {
      observer.disconnect();
      window.removeEventListener("hashchange", scheduleRepair);
    };
  }, []);

  return null;
}
