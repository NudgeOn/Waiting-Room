// SPDX-License-Identifier: Apache-2.0
// Real Docker/AOF restart; never edits server clocks, leases, or counters.
import assert from 'node:assert/strict';
import crypto from 'node:crypto';
import path from 'node:path';
import {setTimeout as delay} from 'node:timers/promises';
import {expect} from '@playwright/test';

export async function recoveryChecks({page,app,room,publicOrigin,admission,headers,mark,screens,docker}){
 console.log('CHECK: recovery request HOLD');
 await page.getByRole('button',{name:'입장 잠시 멈춤',exact:true}).click();
 async function observed(){return page.evaluate(async()=>{const d=await(await fetch('/api/admin/v1/config/delivery')).json();return d.nodes.find(n=>n.id==='coordinator')?.rooms[0];});}
 await expect.poll(async()=>(await observed())?.mode,{timeout:20000}).toBe('HOLD');
 console.log('CHECK: recovery HOLD applied');
 const joined=await app.request.post(publicOrigin+'/_wr/v1/tickets',{headers:{'Idempotency-Key':crypto.randomUUID()},data:{target:'/shop/recovery'}});assert.equal(joined.status(),202);const waiting=await joined.json(),waitingHeaders={Authorization:'Bearer '+waiting.ticketToken};
 const before=await observed();docker('restart','valkey');
 console.log('CHECK: recovery Valkey restarted');
 assert.match(docker('exec','-T','valkey','valkey-cli','PING'),/NOAUTH/);
 assert.match(docker('exec','-T','valkey','valkey-cli','AUTH','default','not-a-valid-password'),/WRONGPASS/);
 try{await expect.poll(async()=>(await observed())?.mode,{timeout:25000}).toBe('RECOVERY_HOLD');}catch(e){const m=await observed();console.log('RECOVERY diagnostic: '+JSON.stringify({mode:m?.mode,fence:m?.recoveryFence,until:m?.recoveryUntil,reason:m?.recoveryReason,validation:m?.recoveryValidation}));throw e;}
 const held=await observed();assert.ok(held.recoveryFence>before.recoveryFence);assert.equal(held.recoveryReason,'primary_changed');assert.equal(held.recoveryValidation,'');
 assert.ok(held.recoveryUntil-Date.now()>60000,'must observe a real safety interval');
 await expect(page.getByText('입장 중지 · 복구 안전 대기',{exact:true})).toBeVisible({timeout:10000});
 await page.setViewportSize({width:360,height:900});assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);await page.screenshot({path:path.join(screens,'runtime-recovery-mobile.png'),fullPage:true});
 let probes=0;
 while(Date.now()<held.recoveryUntil-1000){
  assert.equal((await app.request.post(publicOrigin+`/_wr/v1/rooms/${room.publicId}/admissions`,{headers:waitingHeaders})).status(),503);
  assert.equal((await app.request.post(publicOrigin+`/_wr/v1/rooms/${room.publicId}/admissions`,{headers})).status(),503);
  assert.equal((await observed()).recoveryUntil,held.recoveryUntil,'polling must not extend the shared hold');
  probes++;await delay(2000);
 }
 assert.ok(probes>=20,'safety interval must be repeatedly exercised');
 console.log('CHECK: recovery safety interval probes '+probes);
 await expect.poll(async()=>(await observed())?.mode,{timeout:25000}).toBe('HOLD');
 console.log('CHECK: recovery resumed HOLD');
 const state=await app.request.get(publicOrigin+waiting.statusUrl,{headers:waitingHeaders});assert.equal(state.status(),202);assert.equal((await state.json()).state,'queued');
 console.log('CHECK: recovery original WAITING preserved');
 assert.equal((await app.request.get(publicOrigin+'/shop/recovery',{headers:{'X-Waiting-Room-Admission':admission.admissionToken}})).status(),429,'old admission cannot outlive original expiry plus leeway');
 console.log('CHECK: recovery expired admission rejected');
 await page.getByRole('button',{name:'입장 시작',exact:true}).click();
 await expect.poll(async()=>{const r=await app.request.get(publicOrigin+waiting.statusUrl,{headers:waitingHeaders});return (await r.json()).state;},{timeout:25000}).toBe('ready');
 const claim=await app.request.post(publicOrigin+`/_wr/v1/rooms/${room.publicId}/admissions`,{headers:waitingHeaders});assert.equal(claim.status(),200);const fresh=await claim.json();
 assert.equal((await app.request.get(publicOrigin+'/shop/recovery',{headers:{'X-Waiting-Room-Admission':fresh.admissionToken}})).status(),200);
 const events=await page.evaluate(async()=>(await(await fetch('/api/admin/v1/audit-events')).json()).items);
 for(const result of ['held','resumed'])assert.equal(events.filter(e=>e.action==='runtime.recovery'&&e.targetId===room.id&&e.result===result).length,1);
 mark('real Docker Valkey restart: shared safety hold blocks claims, mobile recovery UI, original waiting ticket retained, expired admission denied, exactly one held/resumed audit');
}
