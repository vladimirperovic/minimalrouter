import { useEffect } from "react";

/** One in-flight request; hidden panels neither poll nor publish late results. */
export function useVisiblePolling(task: (signal: AbortSignal) => Promise<void>, interval: number, enabled = true, refreshKey?: string) {
  useEffect(() => {
    if (!enabled) return;
    let timer = 0;
    let stopped = false;
    let controller: AbortController | null = null;
    const poll = async () => {
      if (stopped || document.hidden) return;
      const current = new AbortController();
      controller = current;
      try { await task(current.signal); }
      catch { /* The caller owns its visible error state. */ }
      finally {
        if (!stopped && !current.signal.aborted && !document.hidden) timer = window.setTimeout(() => void poll(), interval);
      }
    };
    const visibility = () => {
      window.clearTimeout(timer);
      controller?.abort();
      if (!document.hidden) void poll();
    };
    document.addEventListener("visibilitychange", visibility);
    void poll();
    return () => { stopped = true; window.clearTimeout(timer); controller?.abort(); document.removeEventListener("visibilitychange", visibility); };
  }, [task, interval, enabled, refreshKey]);
}
