import { useMemo, useState } from "react";
import type { GatewayHistoryPoint } from "../api-types";
import { localTime, metric } from "../lib/gatewayQuality";

export default function GatewayHistoryChart({ points, hours, now, interval }: { points: GatewayHistoryPoint[]; hours: number; now: number; interval: number }) {
  const [selected, select] = useState<number | null>(null);
  const start = now - hours * 3600_000;
  const ordered = useMemo(() => points.filter(p => Date.parse(p.timestamp) >= start && Date.parse(p.timestamp) <= now).sort((a,b) => Date.parse(a.timestamp)-Date.parse(b.timestamp)), [points,start,now]);
  const max = Math.max(50,...ordered.map(p => p.latency_ms || 0));
  const x = (at: number) => 45 + (at-start)/(now-start)*670;
  const y = (v: number, ceiling: number) => 170-Math.min(1,v/ceiling)*140;
  const gap = Math.max(interval * 2000, hours * 3600_000 / (hours > 24 ? 168 : 144));
  const path = (key: "latency_ms" | "packet_loss_percent", ceiling: number) => {
    let previous = 0;
    return ordered.map(p => {
      const at=Date.parse(p.timestamp), value=p[key];
      if (value === undefined) {previous=0; return "";}
      const command=!previous || at-previous>gap ? "M" : "L"; previous=at;
      return `${command}${x(at)},${y(value,ceiling)}`;
    }).join(" ");
  };
  const active=selected === null ? undefined : ordered[selected];
  if (!ordered.length) return <p className="gateway-empty-copy">No measured samples in this period.</p>;
  return <div className="gateway-chart-wrap">
    <div className="gateway-axis-labels"><span>Latency · 0–{Math.round(max)} ms</span><span>Loss · 0–100%</span></div>
    <svg role="img" aria-label="Latency and packet loss history" className="gateway-chart" viewBox="0 0 760 180">
      <title>Time-based quality history. Select a sample below for exact values.</title>
      {[0,.5,1].map(r=><line key={r} className="gateway-chart-grid" x1="45" x2="715" y1={y(r,1)} y2={y(r,1)}/>)}
      {ordered.filter(p=>p.state==="offline" || p.state==="flapping").map(p=><line key={p.timestamp} className="gateway-outage-marker" x1={x(Date.parse(p.timestamp))} x2={x(Date.parse(p.timestamp))} y1="25" y2="170"><title>{localTime(p.timestamp)} · {p.state}</title></line>)}
      <path className="gateway-chart-latency" d={path("latency_ms",max)} fill="none"/><path className="gateway-chart-loss" d={path("packet_loss_percent",100)} fill="none"/>
      {ordered.map((p,i)=><circle key={`${p.timestamp}-${i}`} cx={x(Date.parse(p.timestamp))} cy={y(p.latency_ms || 0,max)} r="5" className="gateway-chart-point" onPointerEnter={()=>select(i)} onClick={()=>select(i)}><title>{localTime(p.timestamp)} · {metric(p.latency_ms," ms")} · loss {metric(p.packet_loss_percent,"%")}</title></circle>)}
    </svg>
    <div className="gateway-time-axis">{[0,.5,1].map(r=><time key={r} dateTime={new Date(start+(now-start)*r).toISOString()}>{new Date(start+(now-start)*r).toLocaleString([],hours>24?{month:"short",day:"numeric",hour:"2-digit"}:{hour:"2-digit",minute:"2-digit"})}</time>)}</div>
    <div className="gateway-chart-legend"><span className="is-latency">Latency</span><span className="is-loss">Packet loss</span><span className="is-outage">Outage / flapping</span></div>
    <label className="gateway-sample-picker">Inspect sample <input aria-label="Inspect history sample" type="range" min="0" max={ordered.length-1} value={selected ?? ordered.length-1} onChange={e=>select(Number(e.target.value))}/></label>
    <p className="gateway-sample-value" aria-live="polite">{active ? `${localTime(active.timestamp)} · ${metric(active.latency_ms," ms")} · jitter ${metric(active.jitter_ms," ms")} · loss ${metric(active.packet_loss_percent,"%")}` : "Touch a point or use the slider to inspect. Gaps indicate missing samples."}</p>
  </div>;
}
