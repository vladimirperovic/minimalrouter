import { expect, test, type Page } from '@playwright/test';
import { CONFIG, SYSTEM, HEALTH, GW_SUMMARY, GW_SETTINGS } from './fixtures/router';
test.setTimeout(90_000);

async function stub(page: Page) {
 const writes: string[] = [];
 const leases = Array.from({length:20},(_,i)=>({hostname:i===0?'Example-Notebook-Pro':'Device-'+i,mac:`02:00:00:00:00:${String(i).padStart(2,'0')}`,ip_address:`192.168.1.${i+10}`,expires_at:Math.floor(Date.now()/1000)+3600}));
 await page.route('**/api/v1/**', async route => {
  const req=route.request(),p=new URL(req.url()).pathname;
  if(req.method()!=='GET')writes.push(p);
  const data:Record<string,unknown>={
   '/api/v1/auth/session':{authenticated:true,csrf_token:'test'},
   '/api/v1/config':{...CONFIG,accounting:{enabled:true,retention_months:13},dhcp:{...CONFIG.dhcp,static_leases:leases.slice(0,4).map((x,i)=>({...x,id:'lease-'+i}))}},
   '/api/v1/system':{...SYSTEM,runtime:{...SYSTEM.runtime,dhcp_leases:leases}},
   '/api/v1/health':HEALTH,'/api/v1/gateway/summary':GW_SUMMARY,'/api/v1/gateway/settings':GW_SETTINGS,
   '/api/v1/gateway/history':{points:[{timestamp:new Date().toISOString(),latency_ms:5,packet_loss_percent:0}]},
   '/api/v1/gateway/insights':{public_ip_changes:Array.from({length:40},(_,i)=>({timestamp:new Date(Date.now()-i*60000).toISOString(),old_ip:`198.51.100.${i}`,new_ip:`203.0.113.${i}`}))},
   '/api/v1/startup/boots':{boots:[{id:'boot',started_at:new Date().toISOString(),completed:true,readiness:{management_seconds:2,pppoe_seconds:4,dns_seconds:5,internet_seconds:6,wireguard_seconds:7},events:[{offset_seconds:1,kind:'routerd',message:'Started'}],samples:[]}]},
   '/api/v1/audit/events':{events:Array.from({length:70},(_,i)=>({id:'event-'+i,timestamp:new Date().toISOString(),event_type:'auth.login_succeeded',actor:'192.168.1.14',details:{mode:'administrator'}}))},
   '/api/v1/snapshots':[], '/api/v1/devices/pauses':{pauses:[]},
  };
  await route.fulfill({contentType:'application/json',body:JSON.stringify(data[p]??{})});
 });
 return writes;
}

for(const design of ['noema','studio'])for(const mode of ['light','dark'])for(const width of [390,1440,2560]) {
 test(`${design} ${mode} ${width}: scroll surfaces, compact panels and page alignment`,async({page})=>{
  await page.setViewportSize({width,height:1000});
  await page.addInitScript(({design,mode})=>{localStorage.setItem('minimalrouter:design',design);localStorage.setItem('minimalrouter:theme',mode);localStorage.setItem('minimalrouter:wan-speed-estimate-attempt',String(Date.now()));},{design,mode});
  const writes=await stub(page);const errors:string[]=[];page.on('pageerror',e=>errors.push(e.message));
  await page.goto('/#gateway');await expect(page.locator('.gateway-ip-event')).toHaveCount(40);
  const scroll=page.getByRole('region',{name:'Public IP address changes'});
  expect(await scroll.evaluate(e=>e.scrollHeight>e.clientHeight)).toBe(true);
  expect((await page.locator('.gateway-ip-history').boundingBox())!.height).toBeLessThan(650);
  await scroll.focus();await page.keyboard.press('End');await expect.poll(()=>scroll.evaluate(e=>e.scrollTop)).toBeGreaterThan(0);
  for(const route of ['overview','gateway','network','firewall','security','dns-filter','qos','wireguard','cloudflare','wifi','traffic','squid','recovery','logs']) {
   await page.goto('/#'+route);await expect(page.locator('.dashboard-app')).toBeVisible();
   expect(await page.evaluate(()=>document.documentElement.scrollWidth-innerWidth),route+' overflow').toBeLessThanOrEqual(1);
   if(width>900){
    const boxes=await page.locator('.dashboard-main').evaluate(e=>{const top=e.querySelector('.classic-topbar')!.getBoundingClientRect();const content=[...e.children].find(n=>n.matches('.dashboard-section,.dns-filter,.classic-dashboard-overview,.studio-overview'))!.getBoundingClientRect();return {left:top.x-content.x,right:top.right-content.right};});
    expect(Math.abs(boxes.left),route+' left').toBeLessThanOrEqual(1);expect(Math.abs(boxes.right),route+' right').toBeLessThanOrEqual(1);
   }
   const chips=await page.locator('.classic-status-chip').evaluateAll(nodes=>nodes.map(el=>{
    const s=getComputedStyle(el);const rgb=(v:string)=>{const c=document.createElement('canvas').getContext('2d')!;c.fillStyle=v;c.fillRect(0,0,1,1);return [...c.getImageData(0,0,1,1).data];};const lum=(v:number[])=>v.slice(0,3).map(x=>{x/=255;return x<=.04045?x/12.92:((x+.055)/1.055)**2.4}).reduce((a,x,i)=>a+x*[.2126,.7152,.0722][i],0);const a=lum(rgb(s.color)),b=lum(rgb(s.backgroundColor));return {text:el.textContent,contrast:(Math.max(a,b)+.05)/(Math.min(a,b)+.05)};
   }));
   for(const c of chips)expect(c.contrast,route+' '+c.text).toBeGreaterThanOrEqual(4.5);
  }
  await expect(page.locator('.tl-item')).toHaveCount(7);
  expect(await page.locator('.tl').evaluate(e=>getComputedStyle(e,'::before').display)).toBe('none');
  expect(await page.evaluate(()=>getComputedStyle(document.body).backgroundColor)).not.toBe('rgb(221, 225, 233)');
  const geometry=await page.locator('.tl-item').evaluateAll(nodes=>nodes.map(n=>{const r=n.getBoundingClientRect(),dot=n.querySelector('.tl-dot')!.getBoundingClientRect(),line=getComputedStyle(n,'::after');return {x:r.x,width:r.width,cy:dot.y+dot.height/2-r.y,lineY:parseFloat(line.top)+parseFloat(line.height)/2,lineX:line.left.endsWith("%")?parseFloat(line.left)*r.width/100:parseFloat(line.left),lineWidth:line.width.endsWith("%")?parseFloat(line.width)*r.width/100:parseFloat(line.width)};}));
  for(const g of geometry){expect(Math.abs(g.cy-g.lineY)).toBeLessThanOrEqual(1);expect(g.lineX).toBeCloseTo(g.width/2,0);expect(g.lineWidth).toBeCloseTo(g.width,0);}
  for(const [route,selector] of [['logs','.audit-table-scroll'],['firewall','.firewall-presets .elegant-table-container']]){
   await page.goto('/#'+route);const scroller=page.locator(selector);await scroller.scrollIntoViewIfNeeded();
   await scroller.evaluate(e=>{e.scrollTop=180;});
   const cells=await scroller.locator('thead th').evaluateAll(nodes=>nodes.map(el=>{const s=getComputedStyle(el);return {background:s.backgroundColor,position:s.position,top:el.getBoundingClientRect().top};}));
   for(const c of cells){expect(c.background).not.toBe('rgba(0, 0, 0, 0)');expect(c.background).not.toContain('rgba');expect(c.position).toBe('sticky');}
   await page.screenshot({path:test.info().outputPath(route+'.png'),fullPage:true});
  }
  const note=await page.locator('.firewall-presets-note').evaluate(e=>getComputedStyle(e).paddingLeft);expect(parseFloat(note)).toBeGreaterThanOrEqual(16);
  await page.goto('/#security');const totp=page.locator('.security-totp-card .card-title-row');
  expect(await totp.evaluate(e=>getComputedStyle(e).backgroundImage)).toBe('none');
  expect(await totp.locator('h3').evaluate(e=>parseFloat(getComputedStyle(e).fontSize))).toBeLessThanOrEqual(20);
  await expect(page.getByRole('button',{name:'Start 2FA enrollment'})).toBeVisible();
  await page.screenshot({path:test.info().outputPath('security.png'),fullPage:true});
  await page.goto('/#network');await expect(page.locator('.modern-device-section tbody tr')).toHaveCount(20);
  if(width>=1440){expect(await page.locator('.modern-device-section .elegant-table-container').evaluate(e=>e.scrollWidth-e.clientWidth)).toBeLessThanOrEqual(1);const first=page.locator('.modern-device-section tbody tr').first();expect(await first.locator('.elegant-device-identity').evaluate(e=>getComputedStyle(e).flexWrap)).toBe('nowrap');await expect(first.locator('.device-hostname')).toHaveAttribute('title','Example-Notebook-Pro');}
  await page.screenshot({path:test.info().outputPath('network.png'),fullPage:true});
  expect(writes).toEqual([]);expect(errors).toEqual([]);
 });
}
