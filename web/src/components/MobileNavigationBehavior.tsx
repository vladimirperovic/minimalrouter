import { useEffect, useRef, useState } from "react";

const MOBILE_QUERY = "(max-width: 900px)";

function sourceToggle() {
  return document.querySelector<HTMLButtonElement>(".dashboard-topbar .dashboard-menu");
}

export default function MobileNavigationBehavior() {
  const [available, setAvailable] = useState(false);
  const [open, setOpen] = useState(false);
  const savedScroll = useRef(0);
  const bodyLocked = useRef(false);

  useEffect(() => {
    const root = document.getElementById("root") ?? document.body;
    let boundSidebar: HTMLElement | null = null;
    let boundMain: HTMLElement | null = null;
    let teardown: (() => void) | null = null;

    const clearBinding = (updateState = true) => {
      teardown?.();
      teardown = null;
      boundSidebar = null;
      boundMain = null;
      if (updateState) {
        setAvailable(false);
        setOpen(false);
      }
    };

    const bind = (sidebar: HTMLElement, main: HTMLElement) => {
      boundSidebar = sidebar;
      boundMain = main;
      setAvailable(true);

      const mobile = () => window.matchMedia(MOBILE_QUERY).matches;

      const unlockBody = () => {
        if (!bodyLocked.current) return;
        const y = savedScroll.current;
        bodyLocked.current = false;
        document.body.style.position = "";
        document.body.style.top = "";
        document.body.style.right = "";
        document.body.style.left = "";
        document.body.style.width = "";
        main.style.removeProperty("--mobile-nav-scroll");
        window.scrollTo({ top: y, left: 0, behavior: "auto" });
      };

      const lockBody = () => {
        if (bodyLocked.current) return;
        savedScroll.current = window.scrollY;
        bodyLocked.current = true;
        main.style.setProperty("--mobile-nav-scroll", `${savedScroll.current}px`);
        document.body.style.position = "fixed";
        document.body.style.top = `-${savedScroll.current}px`;
        document.body.style.right = "0";
        document.body.style.left = "0";
        document.body.style.width = "100%";
      };

      const sync = () => {
        const next = mobile() && sidebar.classList.contains("is-open");
        setOpen(next);
        document.body.classList.toggle("mobile-navigation-open", next);
        if (next) lockBody();
        else unlockBody();
      };

      const close = () => {
        if (!sidebar.classList.contains("is-open")) return;
        sourceToggle()?.click();
      };

      const onMainClick = (event: Event) => {
        if (!mobile() || !sidebar.classList.contains("is-open")) return;
        const target = event.target instanceof Element ? event.target : null;
        if (target?.closest(".dashboard-menu")) return;
        event.preventDefault();
        event.stopPropagation();
        close();
      };

      const onSidebarClick = (event: Event) => {
        if (!mobile() || !sidebar.classList.contains("is-open")) return;
        const target = event.target instanceof Element ? event.target : null;
        if (target?.closest('a[href^="#"]')) savedScroll.current = 0;
      };

      const onKeyDown = (event: KeyboardEvent) => {
        if (event.key === "Escape" && sidebar.classList.contains("is-open")) close();
      };

      const onResize = () => {
        if (!mobile() && sidebar.classList.contains("is-open")) {
          close();
          return;
        }
        sync();
      };

      const sidebarObserver = new MutationObserver(sync);
      sidebarObserver.observe(sidebar, { attributes: true, attributeFilter: ["class"] });
      main.addEventListener("click", onMainClick, true);
      sidebar.addEventListener("click", onSidebarClick, true);
      window.addEventListener("keydown", onKeyDown);
      window.addEventListener("resize", onResize);
      sync();

      teardown = () => {
        sidebarObserver.disconnect();
        main.removeEventListener("click", onMainClick, true);
        sidebar.removeEventListener("click", onSidebarClick, true);
        window.removeEventListener("keydown", onKeyDown);
        window.removeEventListener("resize", onResize);
        document.body.classList.remove("mobile-navigation-open");
        unlockBody();
      };
    };

    const reconcile = () => {
      const sidebar = document.querySelector<HTMLElement>(".dashboard-sidebar");
      const main = document.querySelector<HTMLElement>(".dashboard-main");
      if (sidebar === boundSidebar && main === boundMain) return;
      clearBinding();
      if (sidebar && main) bind(sidebar, main);
    };

    const mountObserver = new MutationObserver(reconcile);
    mountObserver.observe(root, { childList: true, subtree: true });
    reconcile();

    return () => {
      mountObserver.disconnect();
      clearBinding(false);
    };
  }, []);

  const toggle = () => sourceToggle()?.click();

  if (!available) return null;

  return (
    <button
      aria-expanded={open}
      aria-label={open ? "Close navigation" : "Open navigation"}
      className={`dashboard-menu mobile-navigation-toggle${open ? " is-open" : ""}`}
      onClick={toggle}
      type="button"
    />
  );
}
