import { expect, test, type Page, type Route } from "@playwright/test";
import { CONFIG, SYSTEM, HEALTH, GW_SUMMARY, GW_SETTINGS } from "./fixtures/router";
import type { DNSFilterPolicy } from "../src/api-types";
import type { DNSOperation } from "../src/lib/dnsProtection";

async function fixture(page: Page) {
  const state = { policy: { revision:7,categories:{},exceptions:[] } as DNSFilterPolicy, operation: undefined as DNSOperation | undefined, failStatus:false, checks:[] as Route[], holdChecks:false, statusReads:0 };
  const profile = { id:"kids",name:"Kids tablet",enabled:true,ip_addresses:["192.168.1.50"],services:["youtube"],schedule:{day_windows:{monday:[{start:"19:30",end:"22:30"}],tuesday:[{start:"07:15",end:"08:45"}]}} };
  const config = {...structuredClone(CONFIG),adguard:{...CONFIG.adguard,device_profiles:[profile]}};
  const writes: {path:string;body:unknown}[] = [];
  await page.addInitScript(() => localStorage.setItem("minimalrouter:wan-speed-estimate-attempt",String(Date.now())));
  await page.route("**/api/v1/**",async route => {
    const request = route.request(), path = new URL(request.url()).pathname;
    const json = (body:unknown,status=200) => route.fulfill({status,contentType:"application/json",body:JSON.stringify(body)});
    if (request.method() !== "GET") writes.push({path,body:request.postData() ? request.postDataJSON() : null});
    if (path === "/api/v1/dns-filter/check") {
      if (state.holdChecks) { state.checks.push(route); return; }
      return json({domain:new URL(request.url()).searchParams.get("domain"),action:"No category block",matches:[],healthy:true,policy_revision:7,config_revision:42});
    }
    if (path === "/api/v1/dns-filter/operations") return json({operations:state.operation ? [state.operation] : [],retention:20});
    if (path === "/api/v1/dns-filter") {
      if (request.method() === "PUT") {
        const now = new Date().toISOString(); state.operation = {id:"test-dns-operation",kind:"policy",state:"running",phase:"downloading",base_revision:7,target_revision:8,started_at:now,updated_at:now};
        return json({updating:true,operation:state.operation},202);
      }
      state.statusReads++;
      if (state.failStatus) return json({error:"Resolver status temporarily unavailable"},503);
      return json({policy:state.policy,domains:12000,healthy:true,applied_at:1791417600,updating:state.operation?.state === "running",operation:state.operation,error:state.operation?.error,lists:[],next_refresh_at:0,router_time:"2026-10-09T12:00:00+02:00",timezone:"Europe/Podgorica",config_revision:42,profiles:[],devices:[],blockers:[]});
    }
    if (path === "/api/v1/config/preview") return json({changes:["Update profile"],risk:"low"});
    if (path === "/api/v1/config") return json(request.method() === "PUT" ? {state:"Committed"} : config);
    const responses:Record<string,unknown> = {"/api/v1/auth/session":{authenticated:true,csrf_token:"test"},"/api/v1/system":SYSTEM,"/api/v1/health":HEALTH,"/api/v1/gateway/summary":GW_SUMMARY,"/api/v1/gateway/settings":GW_SETTINGS,"/api/v1/snapshots":[],"/api/v1/devices/pauses":{pauses:[]}};
    return json(responses[path] ?? {});
  });
  page.on("dialog",dialog => void dialog.accept());
  return {state,writes};
}

test("retains draft through navigation, failed status and failed asynchronous apply",async ({page}) => {
  const {state} = await fixture(page); await page.goto("/#dns-filter");
  await page.getByLabel("Adult content",{exact:true}).check();
  await page.evaluate(() => { location.hash = "#logs"; });
  await expect(page.locator("#adguard")).toBeHidden();
  await page.evaluate(() => { location.hash = "#dns-filter"; });
  await expect(page.getByLabel("Adult content",{exact:true})).toBeChecked();
  await page.getByRole("button",{name:"Apply protection",exact:true}).click();
  await expect(page.getByText(/Request accepted\. Existing protection remains active/)).toBeVisible();
  state.failStatus = true;
  await page.getByRole("button",{name:"Refresh status",exact:true}).click();
  await expect(page.getByText("Status unavailable",{exact:true})).toBeVisible();
  await expect(page.getByText(/outcome is not confirmed/)).toBeVisible();
  await expect(page.getByLabel("Adult content",{exact:true})).toBeChecked();
  await expect(page.getByText("Policy revision 7",{exact:true})).toBeVisible();
  await expect(page.getByText(/protection applied|Update finished/)).toHaveCount(0);
  state.failStatus = false; state.operation = {...state.operation!,state:"failed",error:"Download failed; previous policy retained"};
  await page.getByRole("button",{name:"Refresh status",exact:true}).click();
  await expect(page.getByText("Download failed; previous policy retained",{exact:true})).toBeVisible();
  await expect(page.getByRole("button",{name:"Apply protection",exact:true})).toBeEnabled();
  await expect(page.getByLabel("Adult content",{exact:true})).toBeChecked();
});

test("clears draft only after the accepted operation reports a verified outcome",async ({page}) => {
  const {state} = await fixture(page); await page.goto("/#dns-filter");
  await page.getByLabel("Adult content",{exact:true}).check();
  await page.getByRole("button",{name:"Apply protection",exact:true}).click();
  await expect(page.getByText(/Request accepted\. Existing protection remains active/)).toBeVisible();
  await expect(page.getByText("Unapplied changes",{exact:true})).toBeVisible();
  state.policy = {revision:8,categories:{adult:true},exceptions:[]};
  state.operation = {...state.operation!,state:"completed",phase:"verified",applied_revision:8};
  await page.getByRole("button",{name:"Refresh status",exact:true}).click();
  await expect(page.getByText(/DNS protection applied. Revision 8 verified/)).toBeVisible();
  await expect(page.getByText("Unapplied changes",{exact:true})).toHaveCount(0);
});

test("ignores a late domain answer after input changes and invalidates prior results",async ({page}) => {
  const {state} = await fixture(page); state.holdChecks = true; await page.goto("/#dns-filter");
  await page.getByLabel("Domain name",{exact:true}).fill("old.example.com");
  await page.getByRole("button",{name:"Check domain",exact:true}).click();
  await expect.poll(() => state.checks.length).toBe(1);
  await page.getByLabel("Domain name",{exact:true}).fill("new.example.com");
  await state.checks[0].fulfill({contentType:"application/json",body:JSON.stringify({domain:"old.example.com",action:"Block",matches:[]})}).catch(() => {});
  await expect(page.locator(".dns-domain-result")).toHaveCount(0);
  state.holdChecks = false;
  await page.getByRole("button",{name:"Check domain",exact:true}).click();
  await expect(page.locator(".dns-domain-result")).toContainText("new.example.com");
  await page.getByLabel("Domain name",{exact:true}).fill("other.example.com");
  await expect(page.locator(".dns-domain-result")).toHaveCount(0);
});

test("keyboard schedule edit preserves other minutes and modal supports Escape",async ({page}) => {
  const {writes} = await fixture(page); await page.goto("/#dns-filter");
  await page.getByRole("button",{name:"Edit",exact:true}).click();
  const cell = page.getByRole("button",{name:"Mon 21:00 allowed",exact:true});
  await cell.focus(); await cell.press("Space");
  await page.getByRole("button",{name:"Save profile",exact:true}).click();
  await expect(page.getByRole("dialog")).toBeHidden();
  const body = writes.find(write => write.path === "/api/v1/config")!.body as {adguard:{device_profiles:{schedule:{day_windows:unknown}}[]}};
  expect(body.adguard.device_profiles[0].schedule.day_windows).toMatchObject({monday:[{start:"19:30",end:"21:00"},{start:"22:00",end:"22:30"}],tuesday:[{start:"07:15",end:"08:45"}]});
  await page.getByRole("button",{name:"Edit",exact:true}).click();
  await page.getByLabel("Profile name",{exact:true}).press("Escape");
  await expect(page.getByRole("dialog")).toBeHidden();
});

test("DNS polling stops on other routes and resumes on return",async ({page}) => {
  const {state} = await fixture(page); await page.clock.install(); await page.goto("/#dns-filter");
  await expect(page.getByLabel("Adult content",{exact:true})).toBeEnabled();
  await page.evaluate(() => { location.hash = "#logs"; });
  await expect(page.locator("#adguard")).toBeHidden();
  const before = state.statusReads;
  await page.clock.fastForward(65000);
  expect(state.statusReads).toBe(before);
  await page.evaluate(() => { location.hash = "#dns-filter"; });
  await expect.poll(() => state.statusReads).toBeGreaterThan(before);
});

test("profile Pause action preserves its schedule and global filter setting",async ({page}) => {
  const {writes} = await fixture(page); await page.goto("/#dns-filter");
  await page.getByRole("button",{name:"Pause profile Kids tablet",exact:true}).click();
  await expect.poll(() => writes.some(write => write.path === "/api/v1/config")).toBe(true);
  const body = writes.find(write => write.path === "/api/v1/config")!.body as {adguard:{enabled:boolean;device_profiles:{enabled:boolean;schedule:unknown}[]}};
  expect(body.adguard.enabled).toBe(false);
  expect(body.adguard.device_profiles[0]).toMatchObject({enabled:false,schedule:{day_windows:{monday:[{start:"19:30",end:"22:30"}],tuesday:[{start:"07:15",end:"08:45"}]}}});
});
