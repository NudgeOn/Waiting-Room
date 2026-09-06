// SPDX-License-Identifier: Apache-2.0
import assert from 'node:assert/strict';
import crypto from 'node:crypto';
import path from 'node:path';
import {expect} from '@playwright/test';

export async function runtimeChecks({page,browser,mark,screens,docker}){
 const publicOrigin='https://127.0.0.1:20443';
 await page.getByRole('button',{name:'Room 초안 관리',exact:true}).click();
 await page.getByRole('button',{name:/Docker 재시작 검증/}).click();
 for(const [label,value] of [['고객 호스트','127.0.0.1'],['원본 HTTPS 주소','https://demo-origin:20445'],['원본 상태 확인 URL','https://demo-origin:20445/health'],['최대 활성 입장권 수','3'],['입장권 유효 시간 (초)','60']])await page.getByLabel(label,{exact:true}).fill(value);
 await page.getByLabel('배포 시 이 Room 보호 활성화 (첫 배포는 HOLD)',{exact:true}).check();
 await page.getByRole('button',{name:'초안 저장',exact:true}).click();await expect(page.getByRole('status')).toContainText('초안을 저장했습니다.');
 await page.getByRole('button',{name:'실시간 운영',exact:true}).click();await expect(page.getByRole('heading',{name:'실시간 운영',exact:true})).toBeFocused();
 await page.getByRole('button',{name:'저장 초안 배포',exact:true}).click();
 await expect.poll(async()=>page.evaluate(async()=>{const r=await fetch('/api/admin/v1/config/delivery');const d=await r.json();return {state:d.state,count:d.config?.rooms.length};}),{timeout:25000}).toEqual({state:'applied',count:1});
 await expect(page.getByRole('button',{name:/Docker 재시작 검증/})).toBeVisible();await page.getByRole('button',{name:/Docker 재시작 검증/}).click();
 const delivery=await page.evaluate(async()=> (await fetch('/api/admin/v1/config/delivery')).json());const room=delivery.config.rooms[0];
 assert.equal(delivery.runtimes[0].runtime.mode,'HOLD');mark('saved active draft signed, both isolated roles ACK exact generation, first activation HOLD');
 const app=await browser.newContext({ignoreHTTPSErrors:true});const web=await browser.newContext({ignoreHTTPSErrors:true,viewport:{width:360,height:900}});
 try{
  const forbidden=await app.request.get(publicOrigin+'/shop');assert.equal(forbidden.status(),429);
  console.log('CHECK: runtime app initial admission denied');
  const key=crypto.randomUUID();const joined=await app.request.post(publicOrigin+'/_wr/v1/tickets',{headers:{'Idempotency-Key':key},data:{target:'/shop/cart?source=app'}});assert.equal(joined.status(),202);const ticket=await joined.json();
  const replay=await app.request.post(publicOrigin+'/_wr/v1/tickets',{headers:{'Idempotency-Key':key},data:{target:'/shop/cart?source=app'}});assert.deepEqual(await replay.json(),ticket);
  const headers={Authorization:'Bearer '+ticket.ticketToken};assert.equal((await app.request.get(publicOrigin+ticket.statusUrl,{headers})).status(),202);
  console.log('CHECK: runtime app join/replay/status');
  const visitor=await web.newPage();const navigation=await visitor.goto(publicOrigin+'/shop/cart?source=browser');console.log('CHECK: browser navigation status '+navigation.status()+' pathname '+new URL(visitor.url()).pathname);await expect(visitor).toHaveURL(new RegExp('/_wr/wait/'+room.publicId));
  const cookie=(await web.cookies()).find(c=>c.name==='__Host-wrq_'+room.publicId);assert.ok(cookie?.secure&&cookie.httpOnly&&cookie.sameSite==='Lax');
  await visitor.screenshot({path:path.join(screens,'runtime-waiting-mobile.png'),fullPage:true});assert.equal(await visitor.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);
  mark('HOLD blocks origin, app join replay stable, mobile web sealed return and Secure HttpOnly cookie');
  await page.getByRole('button',{name:'AUTO 시작',exact:true}).click();
  await expect.poll(async()=>{const r=await app.request.get(publicOrigin+ticket.statusUrl,{headers});return (await r.json()).state;},{timeout:25000}).toBe('ready');
  const claim=await app.request.post(publicOrigin+`/_wr/v1/rooms/${room.publicId}/admissions`,{headers});assert.equal(claim.status(),200);const admission=await claim.json();
  const admitted=await app.request.get(publicOrigin+'/shop/cart',{headers:{'X-Waiting-Room-Admission':admission.admissionToken}});assert.equal(admitted.status(),200);assert.equal((await admitted.json()).service,'protected-demo-origin');
  const rejected=await app.request.get(publicOrigin+'/shop',{headers:{'X-Waiting-Room-Admission':admission.admissionToken+'x'}});assert.equal(rejected.status(),429);
  await expect(visitor.getByRole('button',{name:'입장하기',exact:true})).toBeVisible({timeout:20000});await visitor.getByRole('button',{name:'입장하기',exact:true}).click();await expect(visitor).toHaveURL(publicOrigin+'/shop/cart?source=browser');await expect(visitor.locator('body')).toContainText('protected-demo-origin');
  mark('AUTO promotion, deterministic app claim, admission verification and browser original-target return reach mTLS origin');
  docker('restart','coordinator','gateway');docker('up','-d','--wait','coordinator','gateway');
  const after=await app.request.post(publicOrigin+'/_wr/v1/tickets',{headers:{'Idempotency-Key':key},data:{target:'/shop/cart?source=app'}});assert.deepEqual(await after.json(),ticket);
  const reclaimed=await app.request.post(publicOrigin+`/_wr/v1/rooms/${room.publicId}/admissions`,{headers});assert.deepEqual(await reclaimed.json(),admission);
  mark('Gateway/Coordinator restart retains encrypted join replay and exact signed admission');
  await page.setViewportSize({width:1586,height:992});await page.screenshot({path:path.join(screens,'runtime-dashboard-desktop.png'),fullPage:true});await page.setViewportSize({width:360,height:900});assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);await page.screenshot({path:path.join(screens,'runtime-dashboard-mobile.png'),fullPage:true});
  await page.getByRole('button',{name:'초안으로 돌아가기',exact:true}).click();await page.getByRole('button',{name:'세션으로 돌아가기',exact:true}).click();
 }finally{await app.close();await web.close();}
}
