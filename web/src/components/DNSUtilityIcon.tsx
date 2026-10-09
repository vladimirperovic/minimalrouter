import type { ReactNode } from "react";

export type DNSUtilityIconKind = "inspect" | "allow" | "globe" | "device" | "note" | "lock" | "plus" | "arrow" | "lists" | "history" | "settings" | "chevron" | "clock" | "pause" | "play";

export default function DNSUtilityIcon({ kind }: { kind: DNSUtilityIconKind }) {
  const paths: Record<typeof kind, ReactNode> = {
    inspect: <><circle cx="10.5" cy="10.5" r="6.5"/><path d="m15.5 15.5 5 5M7.5 10.5h6M10.5 7.5v6"/></>,
    allow: <><path d="m12 3 8 4v5c0 4-3 7-8 9-5-2-8-5-8-9V7z"/><path d="m8.5 12 2.5 2.5 4.5-5"/></>,
    globe: <><circle cx="12" cy="12" r="9"/><ellipse cx="12" cy="12" rx="4" ry="9"/><path d="M3 12h18"/></>,
    device: <><rect x="3" y="4" width="18" height="13" rx="2"/><path d="M8 21h8M12 17v4"/></>,
    note: <><path d="M14 3H6a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V9zM14 3v6h6M8 13h8M8 17h5"/></>,
    lock: <><rect x="5" y="10" width="14" height="11" rx="3"/><path d="M8 10V7a4 4 0 0 1 8 0v3M12 14v3"/></>,
    plus: <path d="M12 5v14M5 12h14"/>,
    arrow: <path d="M4 12h16m-6-6 6 6-6 6"/>,
    lists: <><rect x="4" y="3" width="16" height="18" rx="3"/><path d="M8 8h.01M11 8h5M8 12h.01M11 12h5M8 16h.01M11 16h5"/></>,
    history: <><path d="M3 11a9 9 0 1 1 2.6 7M3 4v7h7M12 7v5l3 2"/></>,
    settings: <><path d="M4 6h7m5 0h4M4 12h2m5 0h9M4 18h9m5 0h2"/><circle cx="13.5" cy="6" r="2.5"/><circle cx="8.5" cy="12" r="2.5"/><circle cx="15.5" cy="18" r="2.5"/></>,
    chevron: <path d="m8 10 4 4 4-4"/>,
    clock: <><circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/></>,
    pause: <><path d="M8 5v14M16 5v14"/></>,
    play: <path d="m8 4 12 8-12 8z"/>,
  };
  return <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">{paths[kind]}</svg>;
}
