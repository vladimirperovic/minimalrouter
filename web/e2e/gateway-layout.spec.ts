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
   '/api/v1/gateway/summary':{...GW_SUMMARY,timestamp:new Date().toISOString()},'/api/v1/gateway/settings':{...GW_SETTINGS,targets:['1.1.1.1','8.8.8.8']},
   '/api/v1/gateway/insights':{available:true,sampled_hours:120,samples:100,up_samples:100,uptime_percent:100,outages:0,public_ip_changes:Array.from({length:20},(_,i)=>({timestamp:new Date(Date.UTC(2026,8,8,i)).toISOString(),old_ip:`198.51.100.${i+1}`,new_ip:`203.0.113.${i+1}`}))},
   '/api/v1/gateway/diagnose':{overall:'healthy',cause:'none',checks:{pppoe:{ok:true,detail:'Connected'},internet:{ok:true,detail:'Public targets replied'},dns:{ok:true,detail:'Resolver replied'},https:{ok:true,detail:'HTTPS reachable'}}},
   '/api/v1/gateway/probe':{...GW_SUMMARY,timestamp:new Date().toISOString(),latency_ms:42},
   '/api/v1/gateway/recovery':{state:'armed',reason:'Watching the PPPoE link',updated_at:new Date().toISOString()},
   '/api/v1/gateway/events':{outages:[],recoveries:[],recovery_history_available:true},
   '/api/v1/snapshots':[],'/api/v1/devices/pauses':{pauses:[]},
  };
  if(p==='/api/v1/gateway/history'){windows.push(url.searchParams.get('window')||'');responses[p]={points:[{timestamp:new Date(Date.now()-60000).toISOString(),latency_ms:8,packet_loss_percent:0},{timestamp:new Date(Date.now()-30000).toISOString(),latency_ms:10,packet_loss_percent:0}]};}
  await route.fulfill({contentType:'application/json',body:JSON.stringify(responses[p]||{})});
 });
 return {writes,windows};
}
for(const design of ['noema','studio'])for(const mode of ['light','dark'])for(const width of [390,1440]){
 test(`${design} ${mode} ${width}: gateway cards pair and keep every control`,async({page})=>{
  await page.setViewportSize({width,height:1000});await page.addInitScript(({design,mode})=>{localStorage.setItem('minimalrouter:design',design);localStorage.setItem('minimalrouter:theme',mode)},{design,mode});
  const state=await stub(page);const errors:string[]=[];page.on('pageerror',e=>errors.push(e.message));await page.goto('/#gateway');
  await expect(page.getByRole('img',{name:'Latency and packet loss history'})).toBeVisible();await expect(page.locator('.gateway-ip-event')).toHaveCount(20);
  const cards=await page.locator('#gateway > article').evaluateAll(nodes=>nodes.map(n=>({label:n.querySelector('h3')?.textContent,rect:n.getBoundingClientRect().toJSON()})));
  expect(cards.map(c=>c.label)).toEqual(['Quality history','Probe targets','Network diagnostics','Automatic recovery']);
  for(let i=0;i<cards.length;i+=2){const a=cards[i].rect,b=cards[i+1].rect;if(width>1100){expect(a.y).toBeCloseTo(b.y,0);expect(b.x).toBeGreaterThanOrEqual(a.right);}else expect(b.y).toBeGreaterThanOrEqual(a.bottom);}
  await page.locator('.gateway-settings-details > summary').click();
  await page.locator('.gateway-service-controls > summary').click();
  for(const name of ['Diagnose connection','Reconnect WAN','Restart DNS & DHCP','Restart WireGuard','Apply monitoring settings'])await expect(page.getByRole('button',{name,exact:name==='Diagnose connection'||name==='Apply monitoring settings'})).toBeVisible();
  await expect(page.getByRole('checkbox',{name:'Enable gateway monitoring and link auto-recovery'})).toBeChecked();
  await expect(page.getByLabel('Primary public IPv4')).toHaveValue('1.1.1.1');await expect(page.getByLabel('Secondary public IPv4')).toHaveValue('8.8.8.8');
  await expect(page.getByLabel('Sample interval').locator('option')).toHaveCount(5);
  for(const period of ['24h','7d','30d','1h']){await page.getByRole('button',{name:period,exact:true}).click();await expect(page.getByRole('button',{name:period,exact:true})).toHaveClass('is-active');await expect.poll(()=>state.windows.includes(period)).toBe(true);}
  await page.getByRole('button',{name:'Diagnose connection',exact:true}).click();await expect(page.getByText('Internet path is healthy', {exact:true})).toBeVisible();
  await expect(page.locator('.diag-check')).toHaveCount(4);
  expect(await page.evaluate(()=>document.documentElement.scrollWidth-innerWidth)).toBeLessThanOrEqual(1);
  expect(errors).toEqual([]);expect(state.writes.map(w=>w.path)).toEqual(['/api/v1/gateway/diagnose']);
 });
}

test('invalid diagnostics stays on the page with a useful error',async({page})=>{
 await stub(page);await page.route('**/api/v1/gateway/diagnose',r=>r.fulfill({json:{state:'Committed'}}));
 const errors:string[]=[];page.on('pageerror',e=>errors.push(e.message));await page.goto('/#gateway');
 await page.getByRole('button',{name:'Diagnose connection',exact:true}).click();
 await expect(page.getByRole('alert')).toContainText('Diagnostics returned an invalid response');
 await expect(page.getByRole('button',{name:'Diagnose connection',exact:true})).toBeEnabled();expect(errors).toEqual([]);
});

test('missing and stale summaries never claim disabled or healthy',async({page})=>{
 await stub(page);await page.route('**/api/v1/gateway/summary',r=>r.fulfill({status:503,body:'unavailable'}));await page.goto('/#gateway');
 await expect(page.locator('#gateway .classic-status-chip')).toHaveText('Unavailable');
 await page.route('**/api/v1/gateway/summary',r=>r.fulfill({json:{...GW_SUMMARY,available:true,timestamp:new Date(Date.now()-3600000).toISOString()}}));await page.reload();
 await expect(page.locator('#gateway .classic-status-chip')).toHaveText('Stale');
 await expect(page.locator('.gateway-target-list')).toContainText('unavailable');
});

test('failed history window clears old chart and reports unavailable',async({page})=>{
 await stub(page);await page.goto('/#gateway');await expect(page.getByRole('img',{name:'Latency and packet loss history'})).toBeVisible();
 await page.route('**/api/v1/gateway/history?window=24h',r=>r.fulfill({status:503,body:'unavailable'}));await page.getByRole('button',{name:'24h',exact:true}).click();
 await expect(page.locator('.gateway-history-card')).toContainText('History unavailable');await expect(page.getByRole('img',{name:'Latency and packet loss history'})).toHaveCount(0);
});

test('manual probe uses POST with CSRF and updates the measurement',async({page})=>{
 const state=await stub(page);await page.goto('/#gateway');const request=page.waitForRequest(r=>r.url().endsWith('/gateway/probe'));
 await page.getByRole('button',{name:'Check now',exact:true}).click();const sent=await request;
 expect(sent.method()).toBe('POST');expect(sent.headers()['x-csrf-token']).toBe('test');
 await expect(page.locator('#gateway .subpage-hero-facts')).toContainText('42 ms');expect(state.writes.map(w=>w.path)).toEqual(['/api/v1/gateway/probe']);
});

test('target failures and diagnostic cause expose matching settings',async({page})=>{
 await stub(page);await page.route('**/api/v1/gateway/summary',r=>r.fulfill({json:{...GW_SUMMARY,timestamp:new Date().toISOString(),targets:[{target:'1.1.1.1',reachable:false,packets_sent:4,packets_received:0,packet_loss_percent:100}]}}));
 await page.route('**/api/v1/gateway/diagnose',r=>r.fulfill({json:{overall:'degraded',cause:'dns',checks:{pppoe:{ok:true,detail:'Connected'},internet:{ok:true,detail:'Reachable'},dns:{ok:false,detail:'DNS failed'},https:{ok:false,detail:'HTTPS failed'}}}}));
 await page.goto('/#gateway');await expect(page.locator('.gateway-target-list')).toContainText('No reply');await page.getByRole('button',{name:'Edit targets →'}).click();await expect(page.getByLabel('Primary public IPv4')).toBeVisible();
 await page.getByRole('button',{name:'Diagnose connection',exact:true}).click();await expect(page.locator('.gateway-advice a')).toHaveAttribute('href','#network');
});

test('availability uses hours and histories distinguish failed loads',async({page})=>{
 await stub(page);await page.route('**/api/v1/gateway/insights',r=>r.fulfill({json:{available:true,sampled_hours:2,samples:120,uptime_percent:100,outages:0,public_ip_changes:[]}}));
 await page.route('**/api/v1/gateway/events',r=>r.fulfill({status:503,body:'unavailable'}));await page.goto('/#gateway');
 await expect(page.locator('.subpage-hero-facts')).toContainText('2h 0m sampled');
 await page.locator('.gateway-events > summary').click();await expect(page.locator('.gateway-events')).toContainText('Event history unavailable');
 await page.route('**/api/v1/gateway/insights',r=>r.fulfill({json:{available:true,sampled_hours:2,uptime_percent:null}}));await page.reload();
 await page.locator('.gateway-ip-history > summary').click();await expect(page.locator('.gateway-ip-history')).toContainText('Public-IP history unavailable');
});

test('chart places samples by timestamp and breaks lines across collection gaps',async({page})=>{
 await stub(page);const now=Date.now();await page.route('**/api/v1/gateway/history?window=1h',r=>r.fulfill({json:{points:[{timestamp:new Date(now-1200000).toISOString(),latency_ms:8,packet_loss_percent:0},{timestamp:new Date(now-1140000).toISOString(),latency_ms:10,packet_loss_percent:0},{timestamp:new Date(now-60000).toISOString(),latency_ms:30,packet_loss_percent:25}]}}));
 await page.goto('/#gateway');await expect(page.locator('.gateway-chart-point')).toHaveCount(3);
 const positions=await page.locator('.gateway-chart-point').evaluateAll(nodes=>nodes.map(n=>Number(n.getAttribute('cx'))));
 expect((positions[2]-positions[1])/(positions[1]-positions[0])).toBeCloseTo(18,1);
 const path=await page.locator('.gateway-chart-latency').getAttribute('d');expect(path?.match(/M/g)).toHaveLength(2);
 await page.getByLabel('Inspect history sample').press('Home');await expect(page.locator('.gateway-sample-value')).toContainText('8 ms');
});

test('outage outcomes and recovery failure durations remain visible',async({page})=>{
 await stub(page);const now=new Date().toISOString();await page.route('**/api/v1/gateway/events',r=>r.fulfill({json:{outages:[{started_at:now,last_observed_at:now,duration_seconds:0,outcome:'unknown',partial:true}],recovery_history_available:true,recoveries:[{id:'failure',timestamp:now,event_type:'gateway.auto_recovery_failed',details:{duration_seconds:'12'}}]}}));
 await page.route('**/api/v1/gateway/recovery',r=>r.fulfill({json:{state:'blocked',reason:'Configuration confirmation pending',updated_at:now}}));
 await page.goto('/#gateway');await expect(page.locator('.rec-card')).toContainText('Configuration confirmation pending');
 await page.locator('.gateway-events > summary').click();await expect(page.locator('.gateway-events')).toContainText('Outcome unknown');await expect(page.locator('.gateway-events')).toContainText('Automatic · Failed');await expect(page.locator('.gateway-events')).toContainText('Action duration 12s');
});
