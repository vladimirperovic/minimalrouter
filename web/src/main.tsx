import { StrictMode, useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import DashboardApp from "./DashboardApp";
import Demo015Preview from "./Demo015Preview";
import MobileNavigationBehavior from "./components/MobileNavigationBehavior";
import RecoveryRouteTools from "./components/RecoveryRouteTools";
import "./index.css";
import "./UXCleanup.css";
import "./DashboardDesign.css";
import "./Demo015Preview.css";
import "./V015FinalTweaks.css";
import "./MobileResponsive.css";
import "./FinalPolish.css";
import "./MobileNavigation.css";
// The look loads last: its rules must win ties against every base sheet above.
// tokens define the measurements, look the palette, controls the components
// that the layered sheets left inconsistent.
import "./theme/tokens.css";
import "./theme/look.css";
import "./theme/controls.css";

function CanonicalRevisionBoundary() {
  const [generation, setGeneration] = useState(0);
  useEffect(() => {
    const reset = () => setGeneration((value) => value + 1);
    window.addEventListener("minimalrouter:canonical-revision", reset);
    return () => window.removeEventListener("minimalrouter:canonical-revision", reset);
  }, []);
  return <DashboardApp key={generation} />;
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <CanonicalRevisionBoundary />
    <RecoveryRouteTools />
    <Demo015Preview />
    <MobileNavigationBehavior />
  </StrictMode>,
);
