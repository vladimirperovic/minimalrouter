// Demo-only safety guard for DOM-decoration observers.
//
// The v0.1.5 visual preview intentionally decorates the rendered dashboard DOM.
// A decoration performed from inside a MutationObserver callback can itself
// produce a childList mutation. Disconnecting while the callback runs prevents
// a self-triggered observer loop while still allowing later application changes
// to be observed after the microtask boundary.

const nativeMutationObserver = window.MutationObserver;
const guardFlag = "__minimalrouterDemoMutationObserverGuard";

type DemoWindow = Window & {
  [guardFlag]?: boolean;
};

if (import.meta.env.VITE_DEMO_MODE && nativeMutationObserver && !(window as DemoWindow)[guardFlag]) {
  class GuardedMutationObserver {
    private readonly inner: MutationObserver;
    private target: Node | null = null;
    private options: MutationObserverInit | null = null;
    private stopped = false;

    constructor(callback: MutationCallback) {
      this.inner = new nativeMutationObserver((records) => {
        if (this.stopped) return;

        const target = this.target;
        const options = this.options;
        this.inner.disconnect();

        callback(records, this as unknown as MutationObserver);

        queueMicrotask(() => {
          if (!this.stopped && target && options) {
            this.inner.observe(target, options);
          }
        });
      });
    }

    observe(target: Node, options: MutationObserverInit) {
      this.target = target;
      this.options = options;
      this.stopped = false;
      this.inner.observe(target, options);
    }

    disconnect() {
      this.stopped = true;
      this.inner.disconnect();
    }

    takeRecords() {
      return this.inner.takeRecords();
    }
  }

  window.MutationObserver = GuardedMutationObserver as unknown as typeof MutationObserver;
  (window as DemoWindow)[guardFlag] = true;
}
