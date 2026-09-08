import { useEffect, useRef, useState } from "react";
import type { DashboardDesign } from "../theme/preferences";

type Props = { design: DashboardDesign; onDesign: (value: DashboardDesign) => void; dark: boolean; onDark: (value: boolean) => void };

export default function AppearancePicker({ design, onDesign, dark, onDark }: Props) {
  const [open, setOpen] = useState(false);
  const host = useRef<HTMLDivElement>(null);
  const trigger = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    if (!open) return;
    const outside = (event: PointerEvent) => { if (!host.current?.contains(event.target as Node)) setOpen(false); };
    const escape = (event: KeyboardEvent) => { if (event.key === "Escape") { setOpen(false); trigger.current?.focus(); } };
    document.addEventListener("pointerdown", outside);
    document.addEventListener("keydown", escape);
    return () => { document.removeEventListener("pointerdown", outside); document.removeEventListener("keydown", escape); };
  }, [open]);
  return <div className="appearance-picker" ref={host}>
    <button ref={trigger} type="button" className="classic-topbar-button appearance-trigger" aria-label="Choose theme" aria-expanded={open} aria-controls="appearance-options" onClick={() => setOpen(!open)}>
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" aria-hidden="true"><rect x="3" y="3" width="18" height="18" rx="5"/><path d="M9 3v18M9 9h12"/></svg><span>{design === "studio" ? "Studio" : "Noema"}</span>
    </button>
    {open && <section id="appearance-options" className="appearance-options" aria-label="Appearance preferences">
      <header><strong>Make it yours.</strong><p>Two perspectives. The same router.</p></header>
      <fieldset><legend>Dashboard theme</legend><div className="appearance-designs">{([['noema', 'Noema', 'Warm & editorial'], ['studio', 'Studio', 'Calm & precise']] as const).map(([id, name, note]) => <label key={id} className={`appearance-design ${id}`}><input type="radio" name="dashboard-design" value={id} checked={design === id} onChange={() => onDesign(id)}/><span className="appearance-swatch" aria-hidden="true"><i/><i/><i/></span><strong>{name}</strong><small>{note}</small></label>)}</div></fieldset>
      <fieldset><legend>Color mode</legend><div className="appearance-modes">{(['Light', 'Dark'] as const).map(label => <label key={label}><input type="radio" name="color-mode" checked={dark === (label === 'Dark')} onChange={() => onDark(label === 'Dark')}/><span>{label}</span></label>)}</div></fieldset>
      <small className="appearance-footnote">Saved on this browser</small>
    </section>}
  </div>;
}
