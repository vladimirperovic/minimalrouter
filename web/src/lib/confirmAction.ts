import "../theme/confirm-dialog.css";

export type ConfirmOptions = {
  title?: string;
  confirmLabel?: string;
  cancelLabel?: string;
  /** Destructive actions get a warning-coloured confirm button. */
  danger?: boolean;
};

// An in-page replacement for window.confirm(). Embedded and locked-down
// browsers auto-dismiss native dialogs — the Smart Change Preview silently
// cancelled every change in such a browser — and native boxes cannot show a
// multi-line review legibly. A modal <dialog> keeps focus inside, closes on
// Escape as "cancel", and is announced as an alertdialog.
let active: Promise<boolean> | null = null;

export function confirmAction(message: string, options: ConfirmOptions = {}): Promise<boolean> {
  // One decision at a time: a second request waits for the first to settle.
  const run = (): Promise<boolean> => new Promise((resolve) => {
    const dialog = document.createElement("dialog");
    dialog.className = "mr-confirm";
    dialog.setAttribute("role", "alertdialog");
    dialog.setAttribute("aria-modal", "true");
    dialog.dataset.mrConfirm = "";
    const id = `mr-confirm-${Math.random().toString(36).slice(2)}`;
    dialog.setAttribute("aria-labelledby", `${id}-title`);
    dialog.setAttribute("aria-describedby", `${id}-message`);

    const title = document.createElement("h2");
    title.id = `${id}-title`;
    title.textContent = options.title ?? "Please confirm";
    const body = document.createElement("p");
    body.id = `${id}-message`;
    body.className = "mr-confirm-message";
    body.dataset.mrConfirmMessage = "";
    body.textContent = message;

    const actions = document.createElement("div");
    actions.className = "mr-confirm-actions";
    const cancel = document.createElement("button");
    cancel.type = "button";
    cancel.className = "button secondary";
    cancel.textContent = options.cancelLabel ?? "Cancel";
    cancel.dataset.mrConfirmCancel = "";
    const accept = document.createElement("button");
    accept.type = "button";
    accept.className = `button ${options.danger ? "danger" : "primary"}`;
    accept.textContent = options.confirmLabel ?? "Confirm";
    accept.dataset.mrConfirmAccept = "";
    actions.append(cancel, accept);
    dialog.append(title, body, actions);

    let settled = false;
    const finish = (value: boolean) => {
      if (settled) return;
      settled = true;
      if (dialog.open) dialog.close();
      dialog.remove();
      resolve(value);
    };
    cancel.addEventListener("click", () => finish(false));
    accept.addEventListener("click", () => finish(true));
    // Escape fires "cancel"; treat it, and any other close, as a refusal.
    dialog.addEventListener("cancel", (event) => { event.preventDefault(); finish(false); });
    dialog.addEventListener("close", () => finish(false));

    // Inside .dashboard-app so the shared button styles apply; showModal()
    // still renders it in the top layer above every page element.
    (document.querySelector(".dashboard-app") ?? document.body).append(dialog);
    if (typeof dialog.showModal === "function") dialog.showModal();
    else dialog.setAttribute("open", "");
    // Cancel is the safe default for an accidental Enter.
    cancel.focus();
  });

  const next = (active ?? Promise.resolve(false)).then(run, run);
  active = next;
  void next.then(() => { if (active === next) active = null; });
  return next;
}
