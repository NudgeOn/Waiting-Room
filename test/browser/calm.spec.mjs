// SPDX-License-Identifier: Apache-2.0
import {test,expect} from '@playwright/test';
import fs from 'node:fs';
import path from 'node:path';
import os from 'node:os';
const hold = 'http://127.0.0.1:18082', auto = 'http://127.0.0.1:18084';
const room = 'abcdefghijklmnopqrst', statusPath = `/_wr/v1/rooms/${room}/status`;
const pictures = process.env.WR_SCREENSHOTS || fs.mkdtempSync(path.join(os.tmpdir(),'wr-calm-'));
test.beforeAll(() => { fs.mkdirSync(pictures,{recursive:true}); console.log('Screenshots: '+pictures); });

test('queued template: identity, copy, refresh, KR/EN and keyboard focus', async({page,context})=>{
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
  for(const text of ['Waiting Room','순서를 기다리고 있어요','접속이 많아 잠시 대기 중이에요.','창을 닫지 않으면 입장 기회를 확인할 수 있어요.','입장 상태','대기 중','예상 대기 시간은 아직 계산 중이에요.','새로고침해도 순서는 유지돼요.','Powered by Waiting Room'])expect(visible).toContain(text);
  expect(visible).not.toMatch(/Vite|Webpack|Internal Server Error|\d+명|\d+분/);
  const initial = (await context.cookies()).find(c=>c.name.startsWith('wr_dev_q_'));
  expect(initial.httpOnly).toBe(true); expect(initial.sameSite).toBe('Lax'); expect(initial.secure).toBe(false);
  expect(await page.evaluate(()=>document.cookie)).not.toContain('wr_dev_');
  await page.reload(); await expect(page.locator('body')).toHaveAttribute('data-state','queued');
  expect((await context.cookies()).find(c=>c.name===initial.name).value===initial.value).toBe(true);
  await page.screenshot({path:path.join(pictures,'desktop.png')});
  await page.keyboard.press('Tab'); await expect(page.getByRole('combobox')).toBeFocused();
  expect(await page.getByRole('combobox').evaluate(e=>getComputedStyle(e).outlineStyle)).not.toBe('none');
  await page.getByRole('combobox').selectOption('en');
  await expect(page.getByRole('heading')).toHaveText('You’re in line');
  await expect(page.locator('html')).toHaveAttribute('lang','en');
  await page.getByRole('combobox').selectOption('ko');
  await expect(page.getByRole('heading')).toHaveText('순서를 기다리고 있어요');
  expect(errors).toEqual([]);
});

test('responsive mobile: no horizontal overflow, readable content and touch control',async({page})=>{
  await page.setViewportSize({width:360,height:800});
  await page.emulateMedia({reducedMotion:'reduce'});
  await page.goto(hold+'/shop/mobile');
  await expect(page.locator('body')).toHaveAttribute('data-state','queued');
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
  const box=await page.getByRole('combobox').boundingBox(); expect(box.height).toBeGreaterThanOrEqual(44);
  await expect(page.getByText('예상 대기 시간은 아직 계산 중이에요.')).toBeVisible();
  await page.screenshot({path:path.join(pictures,'mobile.png')});
  await page.getByRole('combobox').selectOption('en');
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
  await expect(page.getByRole('heading')).toHaveText('You’re in line');
});

test('ready -> explicit claim -> original tab target; repeated claim stable',async({page,context})=>{
  const errors=[]; page.on('pageerror',e=>errors.push(e.message));
  await page.goto(auto+'/shop/first?item=one');
  await expect(page.getByRole('button',{name:'입장하기'})).toBeVisible({timeout:10000});
  const firstAction=await page.locator('#claim').getAttribute('action');
  const second=await context.newPage();
  await second.goto(auto+'/shop/second?item=two');
  await expect(second.getByRole('button',{name:'입장하기'})).toBeVisible();
  await page.screenshot({path:path.join(pictures,'ready.png')});
  const submission = page.waitForRequest(r=>r.method()==='POST' && new URL(r.url()).pathname.endsWith('/admissions'));
  await page.getByRole('button',{name:'입장하기'}).click();
  const headers = await (await submission).allHeaders();
  expect(headers.origin).toBe(auto);
  expect(headers.referer).toBe(auto+'/');
  await expect(page).toHaveURL(auto+'/shop/first?item=one');
  const admitted=(await context.cookies()).find(c=>c.name.startsWith('wr_dev_a_'));
  expect(admitted.httpOnly).toBe(true);
  await second.getByRole('button',{name:'입장하기'}).click();
  await expect(second).toHaveURL(auto+'/shop/second?item=two');
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
  const url=page.url(); const response=await page.goto(url+'x');
  expect(response.status()).toBe(400);
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
