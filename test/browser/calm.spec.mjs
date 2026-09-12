// SPDX-License-Identifier: Apache-2.0
import {test,expect} from '@playwright/test';
import fs from 'node:fs';
import path from 'node:path';
import os from 'node:os';
const hold = 'http://127.0.0.1:18082', auto = 'http://127.0.0.1:18084';
const room = 'abcdefghijklmnopqrst', statusPath = `/_wr/v1/rooms/${room}/status`;
const pictures = process.env.WR_SCREENSHOTS || fs.mkdtempSync(path.join(os.tmpdir(),'wr-calm-'));
test.beforeAll(() => { fs.mkdirSync(pictures,{recursive:true}); console.log('Screenshots: '+pictures); });

test('queued template: identity, copy, refresh, KR/EN and keyboard focus', async({page,context,browserName})=>{
  const errors=[];
  page.on('pageerror',e=>errors.push(e.message));
  page.on('console',m=>{if(['error','warning'].includes(m.type()))errors.push(m.text());});
  const response = await page.goto(hold+'/shop');
  await expect(page.locator('body')).toHaveAttribute('data-state','queued');
  await expect(page).toHaveTitle('Waiting Room');
  expect(new URL(page.url()).pathname).toBe(`/_wr/wait/${room}`);
  expect(response.headers()['content-security-policy']).toContain("frame-ancestors 'none'");
  expect(response.headers()['cache-control']).toBe('no-store');
  await expect(page.getByRole('heading',{name:'순서를 기다리고 있어요'})).toBeVisible();
  await expect(page.getByRole('button',{name:'입장하기'})).toBeHidden();
  const visible = await page.locator('body').innerText();
  for(const text of ['Waiting Room','순서를 기다리고 있어요','접속이 많아 잠시 대기 중이에요.','순서가 되면 자동으로 입장해요.','입장 상태','대기 중','새로고침해도 순서는 유지돼요.','Powered by Waiting Room'])expect(visible).toContain(text);
  expect(visible).not.toMatch(/Vite|Webpack|Internal Server Error/);
  await expect(page.locator('#position-value')).toHaveText(/약 [\d,]+번째/);
  await expect(page.locator('#estimate')).toHaveText('입장 재개 대기 중이에요.');
  const initial = (await context.cookies()).find(c=>c.name.startsWith('wr_dev_q_'));
  expect(initial.httpOnly).toBe(true); expect(initial.sameSite).toBe('Lax'); expect(initial.secure).toBe(false);
  expect(await page.evaluate(()=>document.cookie)).not.toContain('wr_dev_');
  await page.reload(); await expect(page.locator('body')).toHaveAttribute('data-state','queued');
  expect((await context.cookies()).find(c=>c.name===initial.name).value===initial.value).toBe(true);
  await page.keyboard.press('Tab'); await expect(page.getByRole('combobox')).toBeFocused();
  expect(await page.getByRole('combobox').evaluate(e=>getComputedStyle(e).outlineStyle)).not.toBe('none');
  await page.getByRole('combobox').selectOption('en');
  await expect(page.getByRole('heading')).toHaveText('You’re in line');
  await expect(page.locator('html')).toHaveAttribute('lang','en');
  await page.getByRole('combobox').selectOption('ko');
  await expect(page.getByRole('heading')).toHaveText('순서를 기다리고 있어요');
  expect(errors).toEqual([]);
  // Runtime interactions must be warning-free before screenshot tooling runs.
  // Playwright's WebKit animation sync inserts `body {}` even with caret initial;
  // the strict CSP correctly rejects it. Assert this one tool-only warning at
  // this isolated step rather than weakening CSP or filtering application logs.
  await page.screenshot({path:path.join(pictures,'desktop.png'),caret:'initial'});
  expect(errors).toEqual(browserName==='webkit'?["Refused to apply a stylesheet because its hash, its nonce, or 'unsafe-inline' does not appear in the style-src directive of the Content Security Policy."]:[]);
});

test('responsive mobile: no horizontal overflow, readable content and touch control',async({page})=>{
  await page.setViewportSize({width:360,height:800});
  await page.emulateMedia({reducedMotion:'reduce'});
  await page.goto(hold+'/shop/mobile');
  await expect(page.locator('body')).toHaveAttribute('data-state','queued');
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
  const box=await page.getByRole('combobox').boundingBox(); expect(box.height).toBeGreaterThanOrEqual(44);
  await expect(page.locator('#position-value')).toHaveText(/약 [\d,]+번째/);
  await expect(page.locator('#estimate')).toHaveText('입장 재개 대기 중이에요.');
  await page.screenshot({path:path.join(pictures,'mobile.png'),caret:'initial'});
  await page.getByRole('combobox').selectOption('en');
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
  await expect(page.getByRole('heading')).toHaveText('You’re in line');
});

test('heartbeat follows server interval without being postponed by frequent polls',async({page})=>{
  // Client scheduler test: virtual browser time, explicit server-response fixture.
  // Real Coordinator interval bounds are tested in Go; no server clock is changed.
  await page.clock.install();let heartbeats=0;
  await page.addInitScript(()=>{const original=setTimeout;globalThis.setTimeout=(fn,ms,...args)=>{if(ms===30000)globalThis.heartbeatIntervalObserved=true;return original(fn,ms,...args);};});
  await page.route('**'+statusPath,route=>route.fulfill({status:202,contentType:'application/json',body:JSON.stringify({state:'queued',pollAfterMs:3000,heartbeatAfterMs:30000})}));
  await page.route('**'+`/_wr/v1/rooms/${room}/heartbeat`,route=>{heartbeats++;return route.fulfill({status:204});});
  const response=page.waitForResponse(r=>new URL(r.url()).pathname===statusPath);
  await page.goto(hold+'/shop/heartbeat');await response;
  await expect.poll(()=>page.evaluate(()=>globalThis.heartbeatIntervalObserved===true)).toBe(true);
  await page.clock.runFor(31000);await expect.poll(()=>heartbeats).toBe(1);
  await page.clock.runFor(30000);await expect.poll(()=>heartbeats).toBe(2);
  await expect(page.locator('body')).toHaveAttribute('data-state','queued');
});

test('ready -> automatic claim -> original tab target; admitted revisit and replay stable',async({page,context})=>{
  const errors=[]; page.on('pageerror',e=>errors.push(e.message));
  const submission = page.waitForRequest(r=>r.method()==='POST' && new URL(r.url()).pathname.endsWith('/admissions'));
  await page.goto(auto+'/shop/first?item=one');
  await expect(page.getByRole('heading',{name:'자동으로 입장하고 있어요'})).toBeVisible({timeout:10000});
  await expect(page.getByRole('button',{name:'입장하기'})).toBeHidden();
  const waitingURL=page.url();
  const firstAction=await page.locator('#claim').getAttribute('action');
  const second=await context.newPage();
  await second.goto(auto+'/shop/second?item=two');
  await page.screenshot({path:path.join(pictures,'ready.png'),caret:'initial'});
  const headers = await (await submission).allHeaders();
  expect(headers.origin).toBe(auto);
  expect(headers.referer).toBe(auto+'/');
  await expect(page).toHaveURL(auto+'/shop/first?item=one');
  const admitted=(await context.cookies()).find(c=>c.name.startsWith('wr_dev_a_'));
  expect(admitted.httpOnly).toBe(true);
  await expect(second).toHaveURL(auto+'/shop/second?item=two');
  expect((await context.cookies()).find(c=>c.name===admitted.name).value===admitted.value).toBe(true);
  await page.goto(waitingURL);
  await expect(page.getByRole('heading',{name:'원래 페이지로 이동하고 있어요'})).toBeVisible();
  await expect(page.getByRole('button',{name:'입장하기'})).toBeHidden();
  await expect(page).toHaveURL(auto+'/shop/first?item=one');
  expect((await context.cookies()).find(c=>c.name===admitted.name).value===admitted.value).toBe(true);
  const retry=await context.request.post(auto+firstAction,{headers:{Origin:auto},maxRedirects:0});
  expect(retry.status()).toBe(303);
  expect((await context.cookies()).find(c=>c.name===admitted.name).value===admitted.value).toBe(true);
  expect(errors).toEqual([]);
});

test('tampered return, mixed credentials and cross-origin heartbeat fail closed',async({page,context})=>{
  await page.goto(hold+'/shop');
  const action=await page.locator('#claim').getAttribute('action');
  const csrf=await page.locator('body').getAttribute('data-return');
  const mixed=await context.request.get(hold+statusPath,{headers:{Authorization:'Bearer invalid'}});
  expect(mixed.status()).toBe(400);
  const cross=await context.request.post(hold+`/_wr/v1/rooms/${room}/heartbeat`,{headers:{Origin:'https://evil.test','X-Waiting-Room-CSRF':csrf}});
  expect(cross.status()).toBe(403);
  const claim=await context.request.post(hold+action+'x',{headers:{Origin:hold},maxRedirects:0});
  expect(claim.status()).toBe(403);
  const url=page.url(); const rejected=page.waitForResponse(r=>r.url()===url+'x');
  try{await page.goto(url+'x');}catch(e){
    // Firefox reports an empty HTTP 400 navigation as a network error. Check
    // the actual response independently; never print the sealed return URL.
    if(!e.message.includes('NS_ERROR_NET_ERROR_RESPONSE'))throw Error('Unexpected invalid-return navigation failure');
  }
  const response=await rejected;
  expect(response.status()).toBe(400);
});

test('automatic claim requires a valid admission state and submits once across repeated events',async({page,context})=>{
  // Client-only response fixtures keep the real HOLD queue unchanged.
  await page.clock.install();
  let responseStatus=202,responseState='queued',claims=0;
  await page.route('**'+statusPath,route=>route.fulfill({status:responseStatus,headers:{'Retry-After':'5'},contentType:'application/json',body:JSON.stringify({state:responseState,pollAfterMs:3000})}));
  await page.route('**/admissions?*',route=>{claims++;return route.fulfill({status:204});});
  await page.goto(hold+'/shop/automatic-guard');
  await expect(page.locator('body')).toHaveAttribute('data-state','queued');
  const ticket=(await context.cookies()).find(c=>c.name.startsWith('wr_dev_q_')).value;
  for(const [code,state,visible] of [[202,'queued','queued'],[429,'ready','queued'],[503,'ready','unavailable'],[410,'ready','expired'],[401,'admitted','expired'],[200,'unknown','unavailable']]){
    responseStatus=code;responseState=state;
    const received=page.waitForResponse(r=>new URL(r.url()).pathname===statusPath);
    await page.reload();await received;
    await expect(page.locator('body')).toHaveAttribute('data-state',visible);
    await page.clock.runFor(4000);
    expect(claims).toBe(0);
    await expect(page.locator('#claim')).toBeHidden();
  }
  responseStatus=202;responseState='ready';
  await page.reload();
  await expect(page.getByRole('heading')).toHaveText('자동으로 입장하고 있어요');
  await page.getByRole('combobox').selectOption('en');
  await expect(page.getByRole('heading')).toHaveText('Entering automatically');
  await page.clock.runFor(2999);expect(claims).toBe(0);
  await page.clock.runFor(1);await expect.poll(()=>claims).toBe(1);
  await page.evaluate(()=>document.querySelector('#claim').requestSubmit());
  await page.clock.runFor(20000);expect(claims).toBe(1);
  // Exercise the lifecycle handlers without assuming an engine enables BFCache.
  responseState='queued';
  await page.evaluate(()=>{dispatchEvent(new PageTransitionEvent('pagehide',{persisted:true}));dispatchEvent(new PageTransitionEvent('pageshow',{persisted:true}));});
  await expect(page.locator('body')).toHaveAttribute('data-state','queued');
  await page.clock.runFor(4000);expect(claims).toBe(1);
  responseState='admitted';
  await page.clock.runFor(4000);
  await expect(page.getByRole('heading')).toHaveText('Returning to your page');
  await page.clock.runFor(3000);await expect.poll(()=>claims).toBe(2);
  expect((await context.cookies()).find(c=>c.name.startsWith('wr_dev_q_')).value===ticket).toBe(true);
});

test('connection failure keeps ticket and retry recovers; expired shows explicit rejoin',async({page,context})=>{
  await page.goto(hold+'/shop');
  await expect(page.locator('body')).toHaveAttribute('data-state','queued');
  const before=(await context.cookies()).find(c=>c.name.startsWith('wr_dev_q_')).value;
  // UI-only fault injection; real Coordinator failure is separately covered by Go integration.
  await page.route('**'+statusPath,route=>route.fulfill({status:503,contentType:'application/problem+json',body:'{"code":"QUEUE_UNAVAILABLE"}'}));
  await expect(page.getByRole('heading')).toHaveText('연결을 확인하고 있어요',{timeout:10000});
  expect((await context.cookies()).find(c=>c.name.startsWith('wr_dev_q_')).value===before).toBe(true);
  await page.unroute('**'+statusPath);
  await page.getByRole('button',{name:'다시 확인하기'}).click();
  await expect(page.getByRole('heading')).toHaveText('순서를 기다리고 있어요');
  await page.route('**'+statusPath,route=>route.fulfill({status:410,contentType:'application/problem+json',body:'{"code":"TICKET_EXPIRED"}'}));
  await expect(page.getByRole('heading')).toHaveText('대기표가 만료됐어요',{timeout:10000});
  await expect(page.getByRole('link',{name:'다시 대기하기'})).toHaveAttribute('href','/shop');
  await expect(page.getByText('새로고침해도 순서는 유지돼요.')).toBeHidden();
  await page.screenshot({path:path.join(pictures,'expired.png')});
});

test('approximate position and wait range update, localize, and clear after failure',async({page})=>{
  let payload={state:'queued',usersAhead:23,estimatedWaitSeconds:{min:120,max:240},admissionPaused:false,pollAfterMs:3000};
  let failed=false;
  await page.route('**'+statusPath,route=>route.fulfill({status:failed?503:202,contentType:'application/json',body:JSON.stringify(payload)}));
  await page.goto(hold+'/shop/progress');
  await expect(page.locator('#position-value')).toHaveText('약 24번째');
  await expect(page.locator('#estimate')).toHaveText('예상 대기시간: 약 2~4분');
  await page.getByRole('combobox').selectOption('en');await expect(page.locator('#estimate')).toHaveText('Estimated wait: about 2–4 min');
  payload={...payload,usersAhead:9,admissionPaused:true,estimatedWaitSeconds:null};
  await expect(page.locator('#position-value')).toHaveText('About #10',{timeout:10000});await expect(page.locator('#estimate')).toContainText('Admissions are paused');
  failed=true;await expect(page.locator('body')).toHaveAttribute('data-state','unavailable',{timeout:10000});await expect(page.locator('#queue-position')).toBeHidden();await expect(page.locator('#estimate')).not.toContainText('2–4');
  failed=false;payload={state:'queued',usersAhead:null,estimatedWaitSeconds:null,pollAfterMs:3000};await page.getByRole('button',{name:'Check again'}).click();
  await expect(page.locator('#position-value')).toHaveText('Checking…');await expect(page.locator('#estimate')).toContainText('calculating');
});
