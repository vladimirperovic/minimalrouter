import { useEffect, useId, useRef, useState, type KeyboardEvent, type PointerEvent } from "react";
import type { BootSample } from "../lib/logs";

const TOP = 18;
const BASELINE = 184;
const LEFT = 44;
const RIGHT = 16;
const memoryPercent = (sample: BootSample) => sample.memory_total_mb > 0 ? sample.memory_used_mb / sample.memory_total_mb * 100 : null;
const percent = (value: number | null) => value === null ? "Unavailable" : `${value.toFixed(1)}%`;

export default function StartupResourceChart({ samples, loading }: { samples: BootSample[]; loading: boolean }) {
  const id = useId();
  const plot = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(800);
  const [active, setActive] = useState<number | null>(null);
  const hasSamples = samples.length > 0;

  useEffect(() => {
    if (!plot.current) return;
    const observer = new ResizeObserver(([entry]) => setWidth(Math.max(240, entry.contentRect.width)));
    observer.observe(plot.current);
    return () => observer.disconnect();
  }, [hasSamples]);

  if (!samples.length) return <div className="startup-chart-empty">{loading ? "Loading resource samples…" : "No resource samples in this capture."}</div>;

  const index = Math.min(active ?? samples.length - 1, samples.length - 1);
  const selected = samples[index];
  const end = Math.max(1, samples[samples.length - 1].offset_seconds);
  const peak = Math.max(...samples.map(sample => Math.max(sample.cpu_percent, memoryPercent(sample) ?? 0)));
  const ceiling = Math.min(100, Math.max(25, Math.ceil(peak / 25) * 25));
  const x = (seconds: number) => LEFT + seconds / end * (width - LEFT - RIGHT);
  const y = (value: number) => BASELINE - Math.max(0, Math.min(ceiling, value)) / ceiling * (BASELINE - TOP);
  const cpuLine = samples.map((sample, i) => `${i ? "L" : "M"}${x(sample.offset_seconds)},${y(sample.cpu_percent)}`).join(" ");
  const cpuArea = `${cpuLine} L${x(samples[samples.length - 1].offset_seconds)},${BASELINE} L${x(samples[0].offset_seconds)},${BASELINE} Z`;
  // A missing RAM measurement breaks the line; it must not look like zero usage.
  const memoryLine = samples.map((sample, i) => {
    const value = memoryPercent(sample);
    return value === null ? "" : `${i && memoryPercent(samples[i - 1]) !== null ? "L" : "M"}${x(sample.offset_seconds)},${y(value)}`;
  }).join(" ");
  const tickCount = Math.min(Math.floor(end), width < 420 ? 3 : 5);
  const ticks = Array.from({ length: tickCount + 1 }, (_, i) => Math.round(end * i / tickCount));
  const memory = memoryPercent(selected);
  const readout = `At +${selected.offset_seconds}s, CPU ${percent(selected.cpu_percent)}, RAM ${percent(memory)}${memory === null ? "" : ` (${selected.memory_used_mb.toFixed(0)} of ${selected.memory_total_mb.toFixed(0)} MB)`}`;

  function selectAtPointer(event: PointerEvent<HTMLDivElement>) {
    const bounds = event.currentTarget.getBoundingClientRect();
    const seconds = Math.max(0, Math.min(end, (event.clientX - bounds.left - LEFT) / (width - LEFT - RIGHT) * end));
    const closest = samples.reduce((best, sample, i) => Math.abs(sample.offset_seconds - seconds) < Math.abs(samples[best].offset_seconds - seconds) ? i : best, 0);
    setActive(closest);
  }

  function selectWithKeyboard(event: KeyboardEvent<HTMLDivElement>) {
    const next = { ArrowLeft: index - 1, ArrowDown: index - 1, ArrowRight: index + 1, ArrowUp: index + 1, Home: 0, End: samples.length - 1 }[event.key];
    if (next === undefined) return;
    event.preventDefault();
    setActive(Math.max(0, Math.min(samples.length - 1, next)));
  }

  return <section className="startup-chart" aria-labelledby={`${id}-title`}>
    <div className="startup-chart-heading">
      <div><h4 id={`${id}-title`}>Resource usage</h4><p>{samples.length} samples <span aria-hidden="true">·</span> <span className="startup-sample-time">+{selected.offset_seconds}s</span> {active === null ? "latest sample" : "selected sample"}</p></div>
      <div className="startup-chart-readings" aria-hidden="true">
        <div className="startup-reading is-cpu"><span>CPU</span><strong>{percent(selected.cpu_percent)}</strong></div>
        <div className="startup-reading is-memory"><span>RAM</span><strong>{percent(memory)}</strong></div>
      </div>
    </div>
    <div ref={plot} className="startup-plot" role="slider" tabIndex={0} aria-label="Startup resource sample"
      aria-valuemin={0} aria-valuemax={samples.length - 1} aria-valuenow={index} aria-valuetext={readout} aria-describedby={`${id}-hint`}
      onPointerMove={selectAtPointer} onPointerDown={selectAtPointer} onPointerLeave={event => { if (event.pointerType === "mouse") setActive(null); }} onKeyDown={selectWithKeyboard}>
      <svg viewBox={`0 0 ${width} 218`} aria-hidden="true">
        <defs><linearGradient id={`${id}-fill`} x1="0" y1="0" x2="0" y2="1"><stop offset="0%" stopColor="var(--classic-azure)" stopOpacity=".22" /><stop offset="100%" stopColor="var(--classic-azure)" stopOpacity=".02" /></linearGradient></defs>
        {[0, 1, 2, 3, 4].map(tick => {
          const value = ceiling * tick / 4, height = y(value);
          return <g key={tick}><line className="startup-chart-grid" x1={LEFT} x2={width - RIGHT} y1={height} y2={height} /><text className="startup-chart-axis" x={LEFT - 10} y={height + 4} textAnchor="end">{Number(value.toFixed(1))}%</text></g>;
        })}
        {ticks.map(seconds => <text className="startup-chart-axis" key={seconds} x={x(seconds)} y={207} textAnchor={seconds === 0 ? "start" : seconds === end ? "end" : "middle"}>+{seconds}s</text>)}
        <path d={cpuArea} fill={`url(#${id}-fill)`} />
        <path className="startup-cpu-line" d={cpuLine} />
        <path className="startup-memory-line" d={memoryLine} />
        {samples.length <= 30 && samples.map((sample, i) => <circle key={i} className="startup-cpu-point" cx={x(sample.offset_seconds)} cy={y(sample.cpu_percent)} r="2.5" />)}
        <line className="startup-chart-cursor" x1={x(selected.offset_seconds)} x2={x(selected.offset_seconds)} y1={TOP} y2={BASELINE} />
        <circle className="startup-active-point is-cpu" cx={x(selected.offset_seconds)} cy={y(selected.cpu_percent)} r="5" />
        {memory !== null && <circle className="startup-active-point is-memory" cx={x(selected.offset_seconds)} cy={y(memory)} r="5" />}
      </svg>
    </div>
    <div className="startup-chart-footer"><span>Time since boot</span><span id={`${id}-hint`}>Hover, tap or use ← → to inspect</span></div>
  </section>;
}
