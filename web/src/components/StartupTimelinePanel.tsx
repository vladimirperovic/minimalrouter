import { useCallback, useEffect, useState } from "react";
import { apiFetch } from "../lib/api";
import { isDemoMode } from "../lib/demoApi";

type Boot = {
  id: string;
  started_at: string;
  completed: boolean;
  readiness: {
    management_seconds?: number;
    pppoe_seconds?: number;
    dns_seconds?: number;
    internet_seconds?: number;
    wireguard_seconds?: number;
  };
  events: { offset_seconds: number; kind: string; message: string }[];
  samples: { offset_seconds: number; cpu_percent: number; memory_used_mb: number; memory_total_mb: number }[];
};

const MILESTONES: { key: keyof Boot["readiness"]; label: string; detail: string; tone: string }[] = [
  { key: "management_seconds", label: "Management", detail: "dashboard / API ready", tone: "tl-azure" },
  { key: "pppoe_seconds", label: "PPPoE", detail: "ppp0 connected", tone: "tl-green" },
  { key: "dns_seconds", label: "DNS", detail: "resolver succeeded", tone: "tl-amber" },
  { key: "internet_seconds", label: "Internet", detail: "HTTPS path reachable", tone: "tl-emerald" },
  { key: "wireguard_seconds", label: "WireGuard", detail: "interface ready", tone: "tl-violet" },
];

const DEMO_BOOT: Boot = {
  id: "demo-latest",
  started_at: new Date(Date.now() - 14_000).toISOString(),
  completed: true,
  readiness: {
    management_seconds: 1,
    pppoe_seconds: 4,
    dns_seconds: 5,
    internet_seconds: 6,
    wireguard_seconds: 7,
  },
  events: [{ offset_seconds: 3, kind: "routerd", message: "configuration reconciled" }],
  samples: [{ offset_seconds: 7, cpu_percent: 4.2, memory_used_mb: 178, memory_total_mb: 512 }],
};

type TimelineItem = {
  offset: number;
  tone: string;
  label: string;
  detail: string;
  kind?: string;
};

export default function StartupTimelinePanel() {
  const [boots, setBoots] = useState<Boot[]>([]);
  const [selected, setSelected] = useState(0);
  const [error, setError] = useState("");
  const [diskPct, setDiskPct] = useState<number | null>(null);

  const load = useCallback(async () => {
    try {
      const r = await apiFetch("/api/v1/startup/boots");
      if (!r.ok) throw new Error(`Startup timeline unavailable (${r.status})`);
      const b = await r.json();
      const nextBoots = Array.isArray(b.boots) && b.boots.length > 0
        ? b.boots as Boot[]
        : isDemoMode
          ? [DEMO_BOOT]
          : [];
      setBoots(nextBoots);
      setSelected((current) => Math.min(current, Math.max(0, nextBoots.length - 1)));
      setError("");
    } catch (e) {
      if (isDemoMode) {
        setBoots([DEMO_BOOT]);
        setSelected(0);
        setError("");
      } else {
        setError(e instanceof Error ? e.message : "Startup timeline unavailable");
      }
    }
    try {
      const r = await apiFetch("/api/v1/health");
      if (r.ok) {
        const h = await r.json();
        const st = (h.checks || []).find((c: { id: string }) => c.id === "storage");
        const m = st?.summary ? String(st.summary).match(/([\d.]+)%/) : null;
        if (m) setDiskPct(parseFloat(m[1]));
      }
    } catch { /* disk podatak nije kritican */ }
  }, []);

  useEffect(() => { void load(); }, [load]);

  const boot = boots[selected] || boots[0];

  const items: TimelineItem[] = [];
  if (boot) {
    items.push({ offset: 0, tone: "tl-ink", label: "VM started", detail: `boot ${new Date(boot.started_at).toLocaleString()}` });
    for (const m of MILESTONES) {
      const v = boot.readiness[m.key];
      if (v !== undefined) items.push({ offset: v, tone: m.tone, label: m.label, detail: m.detail });
    }
    for (const e of boot.events) {
      items.push({ offset: e.offset_seconds, tone: "tl-event", label: e.kind, detail: e.message, kind: e.kind });
    }
    items.sort((a, b) => a.offset - b.offset || a.label.localeCompare(b.label));
  }

  return (
    <article className="card table-card startup-timeline">
      <div className="card-title-row">
        <div>
          <h3>Startup Timeline</h3>
          <p>Boot milestones from VM start to a fully ready router: management, PPPoE, DNS, internet and WireGuard.</p>
        </div>
        <button className="button secondary small" disabled={boots.length === 0} onClick={() => void load()} type="button">Refresh</button>
      </div>
      {error && <div className="dashboard-alert is-error">{error}</div>}
      {boots.length === 0 ? (
        <div className="empty-state">No startup captures yet. The next routerd start will create one.</div>
      ) : (
        <>
          <div className="tl-boots">
            {boots.map((b, i) => (
              <button
                className={i === selected ? "tl-boot is-active" : "tl-boot"}
                key={b.id}
                onClick={() => setSelected(i)}
                type="button"
              >
                {i === 0 ? "Latest" : new Date(b.started_at).toLocaleString()}
              </button>
            ))}
          </div>
          {boot && (
            <div className="tl-wrap">
              <ol className="tl">
                {items.map((it, i) => (
                  <li key={`${it.offset}-${it.label}-${i}`} className={`tl-item ${it.tone}`}>
                    <span className="tl-dot" aria-hidden="true" />
                    <span className="tl-time">+{it.offset}s</span>
                    <span className="tl-body">
                      <b>{it.label}</b>
                      <small>{it.detail}</small>
                    </span>
                  </li>
                ))}
              </ol>
              <div className="tl-summary">
                <span>Boot completed in <b>+{Math.max(0, ...items.filter((x) => x.offset > 0).map((x) => x.offset))}s</b></span>
                {boot.samples.length > 0 && (() => {
                  const last = boot.samples[boot.samples.length - 1];
                  return (
                    <span>
                      RAM <b>{last.memory_used_mb.toFixed(0)} / {last.memory_total_mb.toFixed(0)} MB</b>
                      {" · "}CPU <b>{last.cpu_percent.toFixed(1)}%</b>
                      {diskPct !== null ? ` · Disk ${diskPct.toFixed(0)}%` : ""}
                    </span>
                  );
                })()}
                <span>{items.length} milestones</span>
              </div>
            </div>
          )}
        </>
      )}
    </article>
  );
}