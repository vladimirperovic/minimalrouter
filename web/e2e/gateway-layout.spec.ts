import {expect,test,type Page} from '@playwright/test';
import {CONFIG,SYSTEM,HEALTH,GW_SUMMARY,GW_SETTINGS} from './fixtures/router';

async function stub(page:Page){
 const writes:Array<{path:string;body:unknown}>=[];const windows:string[]=[];
 await page.addInitScript(()=>localStorage.setItem('minimalrouter:wan-speed-estimate-attempt',String(Date.now())));
 await page.route('**/api/v1/**',async route=>{
  const req=route.request(),url=new URL(req.url()),p=url.pathname;
  if(req.method()!=='GET')writes.push({path:p,body:req.postData()?req.postDataJSON():null});
  const responses:Record<string,unknown>={
   '/api/v1/auth/session':{authenticated:true,csrf_token:'test'},'/api/v1/config':CONFIG,'/api/v1/system':SYSTEM,'/api/v1/health':HEALTH,
   '/api/v1/gateway/summary':GW_SUMMARY,'/api/v1/gateway/settings':{...GW_SETTINGS,targets:['1.1.1.1','8.8.8.8']},
   '/api/v1/gateway/insights':{available:true,sampled_hours:120,samples:100,up_samples:100,uptime_percent:100,outages:0,public_ip_changes:Array.from({length:20},(_,i)=>({timestamp:new Date(Date.UTC(2026,8,8,i)).toISOString(),old_ip:`198.51.100.${i+1}`,new_ip:`203.0.113.${i+1}`}))},
   '/api/v1/gateway/diagnose':{overall:'healthy',cause:'none',checks:{pppoe:{ok:true,detail:'Connected'},dns:{ok:true,detail:'Resolver replied'},https:{ok:true,detail:'HTTPS reachable'}}},
   '/api/v1/snapshots':[],'/api/v1/devices/pauses':{pauses:[]},
  };
  if(p==='/api/v1/gateway/history'){windows.push(url.searchParams.get('window')||'');responses[p]={points:[{timestamp:'2026-09-08T07:00:00Z',latency_ms:8,packet_loss_percent:0},{timestamp:'2026-09-08T07:01:00Z',latency_ms:10,packet_loss_percent:0}]};}
  await route.fulfill({contentType:'application/json',body:JSON.stringify(responses[p]||{})});
 });
 return {writes,windows};
}
for(const design of ['noema','studio'])for(const mode of ['light','dark'])for(const width of [390,1440]){
 test(`${design} ${mode} ${width}: gateway cards pair and keep every control`,async({page})=>{
  await page.setViewportSize({width,height:1000});await page.addInitScript(({design,mode})=>{localStorage.setItem('minimalrouter:design',design);localStorage.setItem('minimalrouter:theme',mode)},{design,mode});
  const state=await stub(page);const errors:string[]=[];page.on('pageerror',e=>errors.push(e.message));await page.goto('/#gateway');
  await expect(page.getByRole('img',{name:'Latency and packet loss history'})).toBeVisible();await expect(page.locator('.gateway-ip-event')).toHaveCount(20);
  const cards=await page.locator('#gateway > :is(article,form)').evaluateAll(nodes=>nodes.map(n=>({label:n.querySelector('h3,.fieldset-title')?.textContent,rect:n.getBoundingClientRect().toJSON()})));
  expect(cards.map(c=>c.label)).toEqual(['Quality history','Public IP history','Network diagnostics','Service recovery','Monitoring targets','Automatic recovery']);
  for(let i=0;i<cards.length;i+=2){const a=cards[i].rect,b=cards[i+1].rect;if(width>1100){expect(a.y).toBeCloseTo(b.y,0);expect(b.x).toBeGreaterThanOrEqual(a.right);expect(a.height).toBeCloseTo(b.height,0);}else expect(b.y).toBeGreaterThanOrEqual(a.bottom);}
  for(const name of ['Diagnose connection','Reconnect WAN','Restart DNS & DHCP','Restart WireGuard','Apply monitoring settings'])await expect(page.getByRole('button',{name,exact:name==='Diagnose connection'||name==='Apply monitoring settings'})).toBeVisible();
  await expect(page.getByRole('checkbox',{name:'Enable gateway monitoring and link auto-recovery'})).toBeChecked();
  await expect(page.getByLabel('Primary public IPv4')).toHaveValue('1.1.1.1');await expect(page.getByLabel('Secondary public IPv4')).toHaveValue('8.8.8.8');
  await expect(page.getByLabel('Sample interval').locator('option')).toHaveCount(5);
  for(const period of ['24h','7d','30d','1h']){await page.getByRole('button',{name:period,exact:true}).click();await expect(page.getByRole('button',{name:period,exact:true})).toHaveClass('is-active');await expect.poll(()=>state.windows.includes(period)).toBe(true);}
  await page.getByRole('button',{name:'Diagnose connection',exact:true}).click();await expect(page.getByText('Internet path is healthy', {exact:true})).toBeVisible();
  await expect(page.locator('.diag-check')).toHaveCount(3);
  expect(await page.evaluate(()=>document.documentElement.scrollWidth-innerWidth)).toBeLessThanOrEqual(1);
  expect(errors).toEqual([]);expect(state.writes.map(w=>w.path)).toEqual(['/api/v1/gateway/diagnose']);
 });
}
