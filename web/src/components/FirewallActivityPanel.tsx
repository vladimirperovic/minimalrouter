import { useCallback, useState } from 'react';
import { apiFetch } from '../lib/api';
import { useVisiblePolling } from '../lib/useVisiblePolling';
import { HistoryBars } from './TrafficInsightsPanel';

type Activity = {available:boolean;collected_at:string|null;allowed:number;blocked:number;points:Array<{start:string;allowed:number;blocked:number;samples:number}>};
const packets=(n:number)=>new Intl.NumberFormat(undefined,{notation:'compact',maximumFractionDigits:2}).format(n);
export default function FirewallActivityPanel(){
 const [data,setData]=useState<Activity|null>(null);const [connections,setConnections]=useState<number|null>(null);const [error,setError]=useState('');
 const load=useCallback(async(signal:AbortSignal)=>{
  const results=await Promise.allSettled([
   apiFetch('/api/v1/firewall/activity',{signal}).then(async r=>{if(!r.ok)throw Error('Firewall activity is unavailable');const body=await r.json() as Activity;if(!Array.isArray(body.points))throw Error('Firewall history is not available on this firmware');return body;}),
   apiFetch('/api/v1/system',{signal}).then(async r=>{if(!r.ok)throw Error();return r.json();}),
  ]);
  if(signal.aborted)return;
  const activity=results[0];if(activity.status==='fulfilled'){setData(activity.value);setError('');}else{setData(null);setError('Firewall activity is unavailable.');}
  const system=results[1];setConnections(system.status==='fulfilled'&&system.value.runtime?.available!==false&&typeof system.value.runtime?.conntrack_count==='number'?system.value.runtime.conntrack_count:null);
 },[]);
 useVisiblePolling(load,30000);
 const observed=data?.available&&data.points.some(p=>p.samples>0);
 return <article className="insights-card firewall-activity"><header className="insights-heading"><div><p className="insights-eyebrow">LAST 24 HOURS</p><h3>Your firewall, at work.</h3></div></header>
  <dl className="insights-stats"><div><dt>Allowed packets</dt><dd>{observed?packets(data!.allowed):'—'}</dd></div><div><dt>Blocked packets</dt><dd>{observed?packets(data!.blocked):'—'}</dd></div><div><dt>Active connections</dt><dd>{connections===null?'—':packets(connections)}</dd><small>right now</small></div></dl>
  {error?<p role="alert" className="insights-empty">{error} Packet counts are unknown.</p>:!observed?<p className="insights-empty">{data?.available?'Collecting the first activity samples…':data?.collected_at?'Collection is delayed. Last sample: '+new Date(data.collected_at).toLocaleString():'Waiting for measured firewall activity…'}</p>:null}
  {data&&<HistoryBars points={data.points.map(p=>({value:p.allowed+p.blocked,observed:data.available&&p.samples>0}))} labels={data.points.map(p=>new Date(p.start).toLocaleTimeString([],{hour:'2-digit',minute:'2-digit',hour12:false,timeZone:'UTC'}))} format={n=>`${packets(n)} packets`}/>}
  <p className="insights-note">UTC · allowed + blocked input and forwarded packets · sampled every minute. Blank bars indicate missing samples. History starts after installation; reset and interrupted intervals are excluded.</p>
 </article>;
}
