// Visual skins for the dashboard shell.
//
// A skin is a pure CSS layer keyed off `data-skin` on the document element. It
// remaps the `--classic-*` design tokens the shell already reads and then
// applies a focused set of structural overrides on top. "classic" is the
// appliance default look and deliberately ships no stylesheet at all, so the
// default rendering path is byte-for-byte what it was before skins existed.

export type SkinID = "classic" | "console" | "atelier" | "topology" | "studio";

export type SkinDefinition = {
  id: SkinID;
  label: string;
  summary: string;
};

export const SKINS: readonly SkinDefinition[] = [
  { id: "classic", label: "Classic", summary: "The default appliance look" },
  { id: "console", label: "Console", summary: "Dense operator console with monospaced figures" },
  { id: "atelier", label: "Atelier", summary: "Calm and spacious, settings-app rows" },
  { id: "topology", label: "Topology", summary: "Network-first, teal and technical" },
  { id: "studio", label: "Studio", summary: "Warm paper, bronze accent, serif figures" },
];

export const DEFAULT_SKIN: SkinID = "classic";

export const SKIN_STORAGE_KEY = "minimalrouter:skin";

function isSkinID(value: string | null): value is SkinID {
  return SKINS.some((skin) => skin.id === value);
}

export function initialSkin(): SkinID {
  try {
    const stored = window.localStorage.getItem(SKIN_STORAGE_KEY);
    if (isSkinID(stored)) return stored;
  } catch {
    // Private-mode browsers can throw on storage access; fall back to the
    // shipped look rather than failing to render.
  }
  return DEFAULT_SKIN;
}

export function applySkin(skin: SkinID) {
  document.documentElement.dataset.skin = skin;
  try {
    window.localStorage.setItem(SKIN_STORAGE_KEY, skin);
  } catch {
    // Persisting the skin is a convenience; failing to store it must never
    // break the dashboard.
  }
}
