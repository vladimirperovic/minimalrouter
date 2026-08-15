import { useCallback, useEffect, useState } from "react";
import { apiFetch } from "../lib/api";

type Boot = { id:string; started_at:string; completed:boolean; readiness:{management_seconds?:number;pppoe_seconds?:number;dns_seconds?:number;internet_seconds?:number;wireguard_seconds?:number}; events:{offset_seconds:number;kind:string;message:string}[]; samples:{offset_seconds:number;cpu_percent:number;memory_used_mb:number;memory_total_mb:number}[] };
const ready=(v?:number)=>v===undefined?"—":`+${v}s`;

export default function StartupTimelinePanel(){
 const [boots,setBoots]=useState<Boot[]>([]); const [selected,setSelected]=useState(0); const [error,setError]=useState("");
 const load=useCallback(async()=>{try{const r=await apiFetch("/api/v1/startup/boots");if(!r.ok)throw new Error(`Startup timeline unavailable (${r.status})`);const b=await r.json();setBoots(Array.isArray(b.boots)?b.boots:[])}catch(e){setError(e instanceof Error?e.message:"Startup timeline unavailable")}},[]);
 useEffect(()=>{void load()},[load]); const boot=boots[selected]||boots[0];
 return <article className="card table-card startup-timeline"><div className="card-title-row"><div><h3>Startup Timeline</h3><p>First 10 minutes of the last five boots: readiness, CPU, memory and important milestones.</p></div><button className="button secondary small" onClick={()=>void load()} type="button">Refresh</button></div>
 {error&&<div className="dashboard-alert is-error">{error}</div>}
 {boots.length===0?<div className="empty-state">No startup captures yet. The next routerd start will create one.</div>:<><div className="filter-buttons">{boots.map((b,i)=><button className={i===selected?"button primary small":"button secondary small"} key={b.id} onClick={()=>setSelected(i)} type="button">{i===0?"Latest":new Date(b.started_at).toLocaleString()}</button>)}</div>
 {boot&&<><dl className="subpage-hero-facts"><div><dt>Management</dt><dd>{ready(boot.readiness.management_seconds)}</dd><small>dashboard/API ready</small></div><div><dt>PPPoE</dt><dd>{ready(boot.readiness.pppoe_seconds)}</dd><small>ppp0 appeared</small></div><div><dt>DNS</dt><dd>{ready(boot.readiness.dns_seconds)}</dd><small>resolver succeeded</small></div><div><dt>Internet</dt><dd>{ready(boot.readiness.internet_seconds)}</dd><small>HTTPS path reachable</small></div><div><dt>WireGuard</dt><dd>{ready(boot.readiness.wireguard_seconds)}</dd><small>interface ready</small></div></dl>
 <div className="table-scroll"><table><thead><tr><th>Time</th><th>Type</th><th>Detail</th></tr></thead><tbody>{boot.events.map((e,i)=><tr key={`${e.offset_seconds}-${i}`}><td>+{e.offset_seconds}s</td><td><code>{e.kind}</code></td><td>{e.message}</td></tr>)}</tbody></table></div>
 <div className="table-scroll"><table><thead><tr><th>Sample</th><th>CPU</th><th>Memory</th></tr></thead><tbody>{boot.samples.map((s,i)=><tr key={i}><td>+{s.offset_seconds}s</td><td>{s.cpu_percent.toFixed(1)}%</td><td>{s.memory_used_mb.toFixed(0)} / {s.memory_total_mb.toFixed(0)} MB</td></tr>)}</tbody></table></div></>}</>}
 </article>
}
