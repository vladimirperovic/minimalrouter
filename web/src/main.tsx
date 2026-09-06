import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import DashboardApp from "./DashboardApp";
import Demo015Preview from "./Demo015Preview";
import MobileNavigationBehavior from "./components/MobileNavigationBehavior";
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
import "./theme/typography.css";
import "./theme/surfaces.css";
import "./theme/controls.css";
import "./theme/overview.css";

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <DashboardApp />
    <Demo015Preview />
    <MobileNavigationBehavior />
  </StrictMode>,
);
