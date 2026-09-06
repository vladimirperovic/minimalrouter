import { useEffect, useRef } from "react";

export function useModalFocus(open: boolean, onClose: () => void) {
  const ref = useRef<HTMLDivElement>(null);
  const close = useRef(onClose);
  useEffect(() => { close.current = onClose; }, [onClose]);
  useEffect(() => {
    const dialog = ref.current;
    if (!open || !dialog) return;
    const previous = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const focusable = () => Array.from(dialog.querySelectorAll<HTMLElement>('button:not(:disabled), a[href], input:not(:disabled), select:not(:disabled), textarea:not(:disabled), [tabindex]:not([tabindex="-1"])'))
      .filter(element => element.getClientRects().length > 0 && getComputedStyle(element).visibility !== "hidden");
    const focusFirst = () => (focusable()[0] || dialog).focus();
    const keydown = (event: KeyboardEvent) => {
      if (event.key === "Escape") { event.preventDefault(); close.current(); }
      if (event.key !== "Tab") return;
      const elements = focusable();
      const index = elements.indexOf(document.activeElement as HTMLElement);
      // Handle every Tab: Safari's default traversal may skip buttons and
      // leave the document, which does not emit a containable focusin event.
      event.preventDefault();
      const next = index < 0 ? (event.shiftKey ? elements.length - 1 : 0)
        : (index + (event.shiftKey ? -1 : 1) + elements.length) % elements.length;
      (elements[next] || dialog).focus();
    };
    const contain = (event: FocusEvent) => { if (!dialog.contains(event.target as Node)) focusFirst(); };
    focusFirst();
    document.addEventListener("keydown", keydown);
    document.addEventListener("focusin", contain);
    return () => {
      document.removeEventListener("keydown", keydown);
      document.removeEventListener("focusin", contain);
      if (previous?.isConnected) previous.focus();
    };
  }, [open]);
  return ref;
}
