import { useEffect, useRef } from "react";
import { SKINS, type SkinID } from "../skins/skins";
import "./SkinMenu.css";

type Props = {
  skin: SkinID;
  onSelect: (skin: SkinID) => void;
  open: boolean;
  setOpen: (open: boolean) => void;
};

export default function SkinMenu({ skin, onSelect, open, setOpen }: Props) {
  const rootRef = useRef<HTMLDivElement>(null);
  const active = SKINS.find((entry) => entry.id === skin) ?? SKINS[0];

  useEffect(() => {
    if (!open) return;
    const onPointerDown = (event: PointerEvent) => {
      if (rootRef.current && !rootRef.current.contains(event.target as Node)) setOpen(false);
    };
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") setOpen(false);
    };
    document.addEventListener("pointerdown", onPointerDown);
    document.addEventListener("keydown", onKeyDown);
    return () => {
      document.removeEventListener("pointerdown", onPointerDown);
      document.removeEventListener("keydown", onKeyDown);
    };
  }, [open, setOpen]);

  return (
    <div className="skin-menu" ref={rootRef}>
      <button
        aria-expanded={open}
        aria-haspopup="menu"
        className="classic-topbar-button skin-menu-trigger"
        onClick={() => setOpen(!open)}
        title={`Appearance: ${active.label}`}
        type="button"
      >
        <svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
          <path d="M12 3a9 9 0 0 0 0 18c1.4 0 2.2-.9 2.2-2 0-1.3-1.1-1.7-1.1-2.7 0-.7.6-1.3 1.4-1.3H16a5 5 0 0 0 5-5c0-3.9-4-7-9-7Z" />
          <circle cx="7.8" cy="11.5" r="1.1" fill="currentColor" stroke="none" />
          <circle cx="11" cy="7.6" r="1.1" fill="currentColor" stroke="none" />
          <circle cx="15.8" cy="8.6" r="1.1" fill="currentColor" stroke="none" />
        </svg>
        <span className="skin-menu-label">{active.label}</span>
      </button>

      {open && (
        <div className="skin-menu-panel" role="menu" aria-label="Dashboard appearance">
          <p className="skin-menu-heading">Appearance</p>
          {SKINS.map((entry) => (
            <button
              aria-checked={entry.id === skin}
              className={entry.id === skin ? "skin-menu-item is-active" : "skin-menu-item"}
              key={entry.id}
              onClick={() => {
                onSelect(entry.id);
                setOpen(false);
              }}
              role="menuitemradio"
              type="button"
            >
              <span className={`skin-menu-swatch is-${entry.id}`} aria-hidden="true" />
              <span className="skin-menu-copy">
                <strong>{entry.label}</strong>
                <small>{entry.summary}</small>
              </span>
              {entry.id === skin && (
                <svg aria-hidden="true" className="skin-menu-check" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.4" strokeLinecap="round" strokeLinejoin="round">
                  <path d="m5 12.5 4.5 4.5L19 7.5" />
                </svg>
              )}
            </button>
          ))}
          <p className="skin-menu-foot">Layout and data stay the same. Only the visual treatment changes.</p>
        </div>
      )}
    </div>
  );
}
