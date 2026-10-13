import type { Page } from "@playwright/test";

// The dashboard asks for confirmation with an in-page <dialog> (see
// src/lib/confirmAction.ts), not window.confirm(). These helpers keep the
// page.on("dialog") / page.once("dialog") style of the specs: a handler
// decides each confirmation as soon as it opens, without the spec having to
// perform an action that waits for it.
type Decide = (message: string) => boolean | Promise<boolean>;
type Entry = { decide: Decide; once: boolean };

const registry = new WeakMap<Page, Entry[]>();

function installWatcher() {
  const w = window as unknown as { __mrConfirmWatching?: boolean; __mrConfirmDecide?: (message: string) => Promise<boolean | null> };
  if (w.__mrConfirmWatching) return;
  w.__mrConfirmWatching = true;
  const check = () => {
    const dialog = document.querySelector<HTMLDialogElement>("dialog[data-mr-confirm][open]:not([data-mr-handled])");
    if (!dialog || !w.__mrConfirmDecide) return;
    dialog.setAttribute("data-mr-handled", "");
    const message = dialog.querySelector("[data-mr-confirm-message]")?.textContent ?? "";
    void w.__mrConfirmDecide(message).then((answer) => {
      if (answer === null) return;
      dialog.querySelector<HTMLButtonElement>(answer ? "[data-mr-confirm-accept]" : "[data-mr-confirm-cancel]")?.click();
    });
  };
  const start = () => {
    new MutationObserver(check).observe(document.documentElement, { subtree: true, childList: true, attributes: true, attributeFilter: ["open"] });
    check();
  };
  if (document.documentElement) start();
  else document.addEventListener("DOMContentLoaded", start, { once: true });
}

/** Decide every in-page confirmation (or only the next one with `once`). */
export async function onConfirm(page: Page, decide: Decide, options: { once?: boolean } = {}) {
  let entries = registry.get(page);
  if (!entries) {
    const list: Entry[] = [];
    entries = list;
    registry.set(page, list);
    await page.exposeFunction("__mrConfirmDecide", async (message: string) => {
      // One-shot handlers answer first, in registration order, like page.once.
      const onceIndex = list.findIndex((entry) => entry.once);
      const index = onceIndex >= 0 ? onceIndex : list.length - 1;
      if (index < 0) return null;
      const entry = list[index];
      if (entry.once) list.splice(index, 1);
      return await entry.decide(message);
    });
    await page.addInitScript(installWatcher);
    await page.evaluate(installWatcher).catch(() => undefined);
  }
  entries.push({ decide, once: Boolean(options.once) });
}

export const acceptConfirms = (page: Page) => onConfirm(page, () => true);
export const dismissConfirms = (page: Page) => onConfirm(page, () => false);
