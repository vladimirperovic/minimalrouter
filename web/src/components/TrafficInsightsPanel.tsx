import { useCallback, useState } from 'react';
import type { TrafficInsights } from '../api-types';
import { apiFetch } from '../lib/api';
import { useVisiblePolling } from '../lib/useVisiblePolling';

export function usageBytes(value: number) {
 const units = ['B','KB','MB','GB','TB']; let size = Math.max(0,value),i=0;
 while(size>=1024 && i<4){size/=1024;i++;}
 return `${size.toFixed(i<2?0:1)} ${units[i]}`;
}
export function HistoryBars({ points, labels, format = usageBytes }: { points: Array<{ value: number; observed: boolean }>; labels: string[]; format?: (n: number) => string }) {
 const max = Math.max(1,...points.map(p=>p.value));
 return <div className="insights-history"><div className="insights-bars" role="img" aria-label="Measured activity by time; missing samples are shown as gaps">{points.map((p,i)=><div className={`insights-bar-column ${p.observed?'':'is-gap'}`} key={i} tabIndex={0} aria-label={`${labels[i]}: ${p.observed?format(p.value):'No samples'}`}><svg viewBox="0 0 16 100" preserveAspectRatio="none" aria-hidden="true"><rect x="0" y={100-(p.observed?Math.max(1,p.value/max*100):0)} width="16" height={p.observed?Math.max(1,p.value/max*100):0} rx="2"/></svg><small>{labels[i]}<b>{p.observed?format(p.value):'No samples'}</b></small></div>)}</div><div className="insights-axis">{labels.filter((_,i)=>i===0 || i===labels.length-1 || (labels.length>6 && i%Math.ceil(labels.length/4)===0)).map((label,i)=><span key={i}>{label}</span>)}</div></div>;
}
const periods = [['today','Today'],['yesterday','Yesterday'],['7d','Last 7 days'],['30d','Last 30 days']] as const;
export default function TrafficInsightsPanel({ enabled }: { enabled: boolean }) {
 const [period,setPeriod]=useState('today');const [data,setData]=useState<TrafficInsights|null>(null);const [error,setError]=useState('');
 const load=useCallback(async(signal:AbortSignal)=>{
  try {const response=await apiFetch(`/api/v1/accounting/insights?period=${period}`,{signal});if(!response.ok)throw Error('Traffic history is unavailable');const body=await response.json() as TrafficInsights;if(!Array.isArray(body.points)||!Array.isArray(body.devices))throw Error('Traffic history is not available on this firmware');if(!signal.aborted){setData(body);setError('');}}
  catch(e){if(!signal.aborted){setData(null);setError(e instanceof Error?e.message:'Traffic history unavailable');}}
 },[period]);
 useVisiblePolling(load,30000,enabled);
 const current=enabled&&data?.enabled&&data.period===period?data:null;
 const delayed=current?.collected_at&&period!=='yesterday'&&Date.now()-Date.parse(current.collected_at)>900000;
 const observed=current?.points.some(p=>p.samples>0)??false;
 const total=current?.total_bytes??0;
 const top=current?.devices.slice(0,5)??[];
 const other=Math.max(0,total-top.reduce((sum,d)=>sum+d.total_bytes,0));
 const shares=[...top.map(d=>({name:d.hostname||d.address,value:d.total_bytes})),...(other>0?[{name:'Other devices',value:other}]:[])];
 let offset=0;const segments=shares.map((d,i)=>{const start=offset;const share=total>0?d.value/total*100:0;offset+=share;return <circle key={i} className={`insight-color-${i+1}`} cx="50" cy="50" r="43" pathLength="100" strokeDasharray={`${share} ${100-share}`} strokeDashoffset={-start}/>;});
 const labels=current?.points.map(p=>new Date(p.start).toLocaleString([],period==='today'||period==='yesterday'?{hour:'2-digit',minute:'2-digit',timeZone:'UTC',hour12:false}:{day:'numeric',month:'short',timeZone:'UTC'}))??[];
 const days=current?Math.max(1,Math.ceil((Date.parse(current.until)-Date.parse(current.from))/86400000)):1;
 return <div className="traffic-insights">
  <article className="insights-card insights-primary"><header className="insights-heading"><div><p className="insights-eyebrow">A CLEARER PICTURE</p><h3>{observed?`${usageBytes(total)} transferred`:'Your traffic, over time.'}</h3></div><label className="insights-period"><span className="sr-only">Traffic period</span><select value={period} onChange={e=>{setPeriod(e.target.value);setData(null);}}>{periods.map(([value,label])=><option key={value} value={value}>{label}</option>)}</select></label></header>
   {!enabled?<p className="insights-empty">Enable per-device accounting below to start collecting traffic history.</p>:error?<p role="alert" className="insights-empty">{error}. Usage is unknown.</p>:!observed?<p className="insights-empty">{current?.available===false?'Traffic storage is unavailable.':current?'No samples for this period yet. History begins after two collection rounds, five minutes apart.':'Loading measured traffic…'}</p>:null}
   {current && <HistoryBars points={current.points.map(p=>({value:p.total_bytes,observed:p.samples>0}))} labels={labels}/>}
   <dl className="insights-stats"><div><dt>Downloaded</dt><dd>{observed?usageBytes(current!.rx_bytes):'—'}</dd></div><div><dt>Uploaded</dt><dd>{observed?usageBytes(current!.tx_bytes):'—'}</dd></div><div><dt>Peak measured rate</dt><dd>{observed && current?.peak_sample_mbps!=null?`${current.peak_sample_mbps.toFixed(1)} Mbps`:'—'}</dd><small>average over a collection interval</small></div><div><dt>Daily average</dt><dd>{observed?usageBytes(total/days):'—'}</dd><small>over selected calendar days</small></div></dl>
   <p className="insights-note">{delayed?`Collection is delayed. Last sample: ${new Date(current!.collected_at!).toLocaleString()}. `:""}UTC · bytes grouped by collection time · sampled every 5 minutes{current?.history_started_at?` · history since ${new Date(current.history_started_at).toLocaleString([], {timeZone:'UTC'})} UTC`:''}. Blank bars indicate missing samples; pre-upgrade and interrupted intervals cannot be reconstructed.</p>
  </article>
  <div className="insights-two-columns"><article className="insights-card"><header className="insights-heading"><div><h3>Most active devices</h3><p>Your bandwidth, by connection.</p></div></header><div className="insights-ranking">{top.map((d,i)=><div key={d.address}><div><span>{d.hostname||d.address}<small>{d.hostname?d.address:''}</small></span><strong>{usageBytes(d.total_bytes)}</strong></div><progress max={Math.max(1,total)} value={d.total_bytes} className={`insight-color-${i+1}`} aria-label={`${d.hostname||d.address} share of traffic`}/></div>)}{top.length===0&&<p className="insights-empty">{!enabled?'Accounting is disabled.':error?'Device usage is unavailable.':'No device usage measured for this period.'}</p>}</div></article>
   <article className="insights-card"><header className="insights-heading"><div><h3>Traffic distribution</h3><p>Share of transferred bytes by device.</p></div></header><div className="insights-distribution"><div className="insights-donut" role="img" aria-label={shares.length?shares.map(d=>`${d.name}: ${(d.value/total*100).toFixed(1)}%`).join(', '):'No measured traffic distribution'}><svg viewBox="0 0 100 100" aria-hidden="true"><circle className="insights-donut-track" cx="50" cy="50" r="43"/>{segments}</svg><div><strong>{observed?usageBytes(total):'—'}</strong><span>Total transferred</span></div></div><ul>{shares.map((d,i)=><li key={d.name+i}><i className={`insight-color-${i+1}`}/><span>{d.name}</span><strong>{(d.value/total*100).toFixed(1)}%</strong></li>)}</ul></div></article></div>
 </div>;
}
