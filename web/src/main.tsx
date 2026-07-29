import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import App from "./App";
import DashboardLogs from "./components/DashboardLogs";
import "./index.css";

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App />
    <DashboardLogs />
  </StrictMode>,
);
