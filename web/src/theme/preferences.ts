export type DashboardDesign = "noema" | "studio";
export const DESIGN_STORAGE_KEY = "minimalrouter:design";
export const THEME_STORAGE_KEY = "minimalrouter:theme";

export function initialDesign(): DashboardDesign {
  try { return localStorage.getItem(DESIGN_STORAGE_KEY) === "studio" ? "studio" : "noema"; }
  catch { return "noema"; }
}

export function initialDark(): boolean {
  try {
    const stored = localStorage.getItem(THEME_STORAGE_KEY);
    if (stored === "dark" || stored === "light") return stored === "dark";
  } catch { /* Appearance must work without storage. */ }
  return window.matchMedia?.("(prefers-color-scheme: dark)").matches ?? false;
}

// Apply before React paints, including the authentication screen.
export function initializeAppearance() {
  document.documentElement.dataset.design = initialDesign();
  document.documentElement.dataset.theme = initialDark() ? "dark" : "light";
}
