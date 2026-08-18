import { StrictMode, useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import DashboardApp from "./DashboardApp";
import Demo015Preview from "./Demo015Preview";
import "./demoMutationObserverGuard";
import "./index.css";
import "./UXCleanup.css";
import "./Demo015Preview.css";

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
    <Demo015Preview />
  </StrictMode>,
);
