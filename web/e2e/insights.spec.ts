import {test,expect,type Page} from '@playwright/test';
import {CONFIG,SYSTEM,HEALTH,GW_SUMMARY,GW_SETTINGS} from './fixtures/router';

async function fixture(page:Page){
 const config=structuredClone(CONFIG) as typeof CONFIG & {wireguard:{peers:unknown[]};adguard:{device_profiles:unknown[]}};
 config.accounting.enabled=true;config.dhcp.dns_servers=['1.1.1.1','9.9.9.9','8.8.8.8'];
 Object.assign(config.wireguard,{peers:[{id:'test-peer',name:'Travel laptop',public_key:'PUBLIC_TEST_KEY',allowed_ips:['10.8.0.2/32'],enabled:true}]});
 Object.assign(config.adguard,{device_profiles:[{id:'kids',name:'Kids',enabled:true,ip_addresses:['192.168.1.100'],services:['youtube'],schedule:{days:[1,2,3],start:'08:00',end:'20:00'}}]});
 let fail=false;const mutations:string[]=[];
 await page.addInitScript(()=>localStorage.setItem('minimalrouter:wan-speed-estimate-attempt',String(Date.now())));
 await page.route('**/api/v1/**',route=>{
  const url=new URL(route.request().url()),path=url.pathname;
  if(!['GET','HEAD'].includes(route.request().method()))mutations.push(path);
  const responses:Record<string,unknown>={
   '/api/v1/auth/session':{authenticated:true,csrf_token:'test'},'/api/v1/config':config,'/api/v1/system':SYSTEM,'/api/v1/health':HEALTH,
   '/api/v1/gateway/summary':GW_SUMMARY,'/api/v1/gateway/settings':GW_SETTINGS,'/api/v1/gateway/history':{points:[]},'/api/v1/snapshots':[],
   '/api/v1/devices/pauses':{pauses:[]},'/api/v1/audit/events':{events:[]},'/api/v1/accounting':{available:true,enabled:true,months:[]},
   '/api/v1/firewall/activity':{available:true,collected_at:new Date().toISOString(),allowed:1000,blocked:25,points:[{start:'2026-09-08T00:00:00Z',allowed:1000,blocked:25,samples:30}]},
  };
  if(path==='/api/v1/accounting/insights'){
   if(fail)return route.fulfill({status:503,body:'unavailable'});
   const period=url.searchParams.get('period');const total=period==='7d'?8192:4096;
   return route.fulfill({contentType:'application/json',body:JSON.stringify({available:true,enabled:true,period,from:'2026-09-08T00:00:00Z',until:'2026-09-08T12:00:00Z',points:[{start:'2026-09-08T00:00:00Z',total_bytes:total,samples:12}],devices:[{hostname:'Travel laptop',address:'192.0.2.1',total_bytes:total}],rx_bytes:total*.75,tx_bytes:total*.25,total_bytes:total,peak_sample_mbps:2.5})});
  }
  return route.fulfill({contentType:'application/json',body:JSON.stringify(responses[path]??{})});
 });
 return {mutations,fail:()=>{fail=true}};
}
for(const design of ['noema','studio'])for(const mode of ['light','dark'])for(const width of [390,1440]){
 test(`${design} ${mode} ${width}: cards retain controls and large tables`,async({page})=>{
  await page.setViewportSize({width,height:1000});await page.addInitScript(({design,mode})=>{localStorage.setItem('minimalrouter:design',design);localStorage.setItem('minimalrouter:theme',mode)},{design,mode});
  const state=await fixture(page);const errors:string[]=[];page.on('pageerror',e=>errors.push(e.message));
  await page.route('**/', async route => {
   const response=await route.fetch();
   await route.fulfill({response,headers:{...response.headers(),'content-security-policy':"default-src 'self'; script-src 'self'; style-src 'self'; style-src-attr 'none'; img-src 'self' data:; font-src 'self'; connect-src 'self'"}});
  });
  for(const route of ['traffic','firewall','security','dns-filter','wireguard']){
   await page.goto('/#'+route);await expect(page.locator('.dashboard-app')).toBeVisible();
   if(route==='traffic'){
    await expect(page.getByRole('heading',{name:'4 KB transferred'})).toBeVisible();
    await expect(page.getByRole('heading',{name:'Most active devices'})).toBeVisible();
    await expect(page.getByRole('heading',{name:'Traffic distribution'})).toBeVisible();
    await page.getByLabel('Traffic period').selectOption('7d');await expect(page.getByRole('heading',{name:'8 KB transferred'})).toBeVisible();
    await expect(page.locator('.insights-donut')).toHaveAttribute('aria-label','Travel laptop: 100.0%');
    const bar=page.locator('.insights-bar-column rect').first();
    expect(await bar.evaluate(e=>e.getBoundingClientRect().height)).toBeGreaterThan(10);
    expect(await page.locator('.insights-donut circle.insight-color-1').evaluate(e=>getComputedStyle(e).stroke)).not.toBe('none');
    expect(await page.locator('.traffic-insights [style]').count()).toBe(0);
    await page.screenshot({path:test.info().outputPath('traffic.png'),fullPage:true});
   }
   if(route==='firewall')await expect(page.locator('.firewall-activity')).toContainText('1K');
   if(route==='security'){
    await expect(page.getByRole('region',{name:'Security configuration'})).toBeVisible();
    await expect(page.getByRole('button',{name:'Add network',exact:true})).toBeVisible();
    await expect(page.getByRole('button',{name:'Start 2FA enrollment',exact:true})).toBeVisible();
    const columns=(await page.locator('.security-settings-columns').boundingBox())!;
    const full=(await page.locator('.security-full-width').boundingBox())!;expect(Math.abs(full.width-columns.width)).toBeLessThan(2);
   }
   if(route==='dns-filter'){
    await expect(page.getByRole('textbox',{name:'Upstream DNS resolvers'})).toHaveValue('1.1.1.1\n9.9.9.9\n8.8.8.8');
    const table=page.getByRole('table',{name:'DNS Filter device profiles'});await expect(table).toBeVisible();
    await expect(table.getByRole('button',{name:'Edit',exact:true})).toBeVisible();
    await table.getByRole('button',{name:'Edit',exact:true}).click();await expect(page.getByRole('heading',{name:'Edit device profile',exact:true})).toBeVisible();
    await page.getByRole('button',{name:'Cancel',exact:true}).click();
   }
   if(route==='wireguard'){
    const card=page.locator('.wg-peer-row');await expect(card).toContainText('Travel laptop');
    for(const name of ['More info','Rename','QR code','Download settings','Disable','Delete peer Travel laptop'])await expect(card.getByRole('button',{name,exact:true})).toBeVisible();
    await card.getByRole('button',{name:'Rename',exact:true}).click();await expect(page.getByLabel('Peer name')).toHaveValue('Travel laptop');await card.getByRole('button',{name:'Cancel',exact:true}).click();
   }
   expect(await page.evaluate(()=>document.documentElement.scrollWidth-innerWidth),route).toBeLessThanOrEqual(1);
  }
  expect(errors).toEqual([]);expect(state.mutations).toEqual([]);
 });
}
test('traffic failure clears the old values and chart',async({page})=>{
 await page.clock.install();const state=await fixture(page);await page.goto('/#traffic');await expect(page.getByRole('heading',{name:'4 KB transferred'})).toBeVisible();state.fail();await page.clock.fastForward(31000);await expect(page.locator('.traffic-insights [role="alert"]')).toContainText('unknown');await expect(page.locator('.insights-ranking progress')).toHaveCount(0);await expect(page.getByRole('heading',{name:'4 KB transferred'})).toHaveCount(0);
});
