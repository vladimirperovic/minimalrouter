import { StrictMode, useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import DashboardApp from "./DashboardApp";
import "./index.css";

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
  </StrictMode>,
);
