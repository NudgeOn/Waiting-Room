// SPDX-License-Identifier: Apache-2.0
// Real alternate-port Compose fixture only. No mocked API responses or secrets in artifacts.
import assert from 'node:assert/strict';
import crypto from 'node:crypto';
import path from 'node:path';
import {setTimeout as delay} from 'node:timers/promises';
import {chromium,firefox,webkit,expect} from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import {adminResponse} from './contracts.mjs';

const origin='https://127.0.0.1:29443';
export async function operationsChecks({request,password,cookie,csrf,screens}){
 const checked=new Set(),accessibilityFailures=[];
 const call=async(url,method='GET',body,headers={})=>{const out=await request(29443,url,method,body,headers);checked.add(adminResponse(method,url,out.status,out.headers,out.body));return out;};
 const actors=[{role:'admin',cookie,csrf}];
 async function proof(action,target,method,etag,body){
  const requestDigest=crypto.createHash('sha256').update(`wr-action/v1\n${method}\n${target}\n${etag}\n${body===undefined?'':JSON.stringify(body)}`).digest('hex');
  const out=await call('/auth/reauth','POST',{password,action,targetId:target,requestDigest},{'X-CSRF-Token':csrf});assert.equal(out.status,200,'real action-bound reauthentication');return out.body.reauthToken;
 }
 for(const role of ['operator','viewer']){
  const id='ops_'+role,secret=crypto.randomBytes(32).toString('base64url'),body={id,role,password:secret};
  const token=await proof('users.write',id,'POST','',body),headers={'X-CSRF-Token':csrf,'X-Reauth-Token':token,'Idempotency-Key':crypto.randomUUID()};
  const first=await call('/users','POST',body,headers);assert.equal(first.status,201);const replay=await call('/users','POST',body,headers);assert.deepEqual(replay.body,first.body);assert.equal(replay.headers['idempotency-replayed'],'true');
  const login=await call('/auth/login','POST',{username:id,password:secret},{Cookie:'','X-WR-Auth':'1'});assert.equal(login.status,200);assert.equal(login.body.session.role,role);
  actors.push({role,csrf:login.body.csrfToken,cookie:login.headers['set-cookie'].map(v=>v.split(';')[0]).join('; ')});
 }
 const draft=await call('/config/draft');
 const reads=['/auth/me','/capabilities','/installation','/config/draft','/audit-events','/config/delivery','/rooms/setup_room/runtime','/rooms/setup_room/events','/lab/runs'];
 const adminOnly=[['/config/draft','PUT',draft.body,draft.etag],['/config/publish','POST',{},draft.etag],['/users','POST',{id:'denied',role:'viewer',password:'never stored password fixture'}],['/users/ops_viewer','PATCH',{role:'operator',enabled:true},'"user-1"'],['/users/ops_viewer','DELETE',undefined,'"user-1"'],['/users/ops_viewer/totp-reset','POST',{},'"user-1"'],['/security/totp','PUT',{enabled:true},'"policy-1"'],['/security/totp/enrollment','POST',{},'"policy-1"'],['/security/totp/enrollment/start','POST',{challengeToken:'A'.repeat(43)}],['/security/totp/enrollment/verify','POST',{challengeToken:'A'.repeat(43),code:'123456'}],['/rooms/setup_room/runtime','PATCH',{action:'instant-off'},'"runtime-1"'],['/rooms/setup_room/runtime','PATCH',{action:'new-epoch',scope:'installation'},'"runtime-1"'],['/auth/reauth','POST',{password,action:'users.write',targetId:'denied',requestDigest:'a'.repeat(64)}]];
 for(const actor of actors){
  const headers={Cookie:actor.cookie,'X-CSRF-Token':actor.csrf,'Idempotency-Key':crypto.randomUUID()};
  for(const url of reads)assert.equal((await call(url,'GET',undefined,{Cookie:actor.cookie})).status,200,actor.role+' read '+url);
  assert.equal((await call('/config/route-check','POST',{source:'draft',url:'https://127.0.0.1/shop'},headers)).status,200);
  for(const url of ['/users','/security/totp'])assert.equal((await call(url,'GET',undefined,{Cookie:actor.cookie})).status,actor.role==='admin'?200:403);
  if(actor.role!=='admin')for(const [url,method,body,etag]of adminOnly){const out=await call(url,method,body,{...headers,'If-Match':etag??''});assert.equal(out.status,403,actor.role+' denied '+method+' '+url);}
  if(actor.role==='viewer')for(const [url,method,body]of [['/rooms/setup_room/runtime','PATCH',{action:'hold'}],['/rooms/setup_room/events','POST',{prequeueAt:new Date(Date.now()+3600000).toISOString(),admitAt:new Date(Date.now()+7200000).toISOString(),drainAt:new Date(Date.now()+10800000).toISOString()}],['/lab/runs','POST',{preset:'quick-20'}]])assert.equal((await call(url,method,body,{...headers,'If-Match':'"runtime-1"'})).status,403,'Viewer mutation denied');
 }
 const operator=actors[1],operatorHeaders={Cookie:operator.cookie,'X-CSRF-Token':operator.csrf};
 async function command(url,method,body,etag){const headers={...operatorHeaders,'Idempotency-Key':crypto.randomUUID(),...(etag?{'If-Match':etag}:{})};const first=await call(url,method,body,headers);assert.ok(first.status>=200&&first.status<300,method+' '+url+' succeeds');const again=await call(url,method,body,headers);assert.deepEqual(again.body,first.body);assert.equal(again.headers['idempotency-replayed'],'true');return first;}
 for(const action of ['auto','hold','safe-drain','set-limits']){const rt=await call('/rooms/setup_room/runtime');await command('/rooms/setup_room/runtime','PATCH',{action,...(action==='set-limits'?{limits:rt.body.limits}:{})},rt.etag);}
 const schedule={prequeueAt:new Date(Date.now()+3600000).toISOString(),admitAt:new Date(Date.now()+7200000).toISOString(),drainAt:new Date(Date.now()+10800000).toISOString()};
 let rt=await call('/rooms/setup_room/runtime');const event=await command('/rooms/setup_room/events','POST',schedule,rt.etag);
 for(const [path,method,body]of [['/events/'+event.body.id,'PUT',schedule],['/events/'+event.body.id,'DELETE',undefined],['/events/'+event.body.id+'/resume','POST',{}]])assert.equal((await call(path,method,body,{Cookie:actors[2].cookie,'X-CSRF-Token':actors[2].csrf,'Idempotency-Key':crypto.randomUUID(),'If-Match':event.etag})).status,403,'Viewer event mutation denied');
 await command('/events/'+event.body.id,'PUT',schedule,event.etag);
 rt=await call('/rooms/setup_room/runtime');await command('/rooms/setup_room/runtime','PATCH',{action:'hold'},rt.etag);
 rt=await call('/rooms/setup_room/runtime');const resumed=await command('/events/'+event.body.id+'/resume','POST',{},rt.etag);await command('/events/'+event.body.id,'DELETE',undefined,resumed.etag);
 // Generate >50 real, accepted commands so UI pagination verifies server cursor behavior.
 for(let i=0;i<40;i++){rt=await call('/rooms/setup_room/runtime');await command('/rooms/setup_room/runtime','PATCH',{action:'hold'},rt.etag);}
 let audit=await call('/audit-events');assert.equal(audit.body.items.length,50);assert.ok(audit.body.nextCursor);const older=await call('/audit-events?cursor='+encodeURIComponent(audit.body.nextCursor));assert.ok(older.body.items.length>0);assert.ok(older.body.items.every(x=>!audit.body.items.some(y=>y.id===x.id)));
 const lab=await command('/lab/runs','POST',{preset:'smoke-1k'});assert.equal((await call('/lab/runs/'+lab.body.id+'/cancel','POST',{}, {Cookie:actors[2].cookie,'X-CSRF-Token':actors[2].csrf,'Idempotency-Key':crypto.randomUUID()})).status,403);await command('/lab/runs/'+lab.body.id+'/cancel','POST',{});
 console.log('PASS: real Admin/Operator/Viewer read-write matrix, operator runtime/event CRUD and Lab cancel, durable retries and audit pagination');
 for(const [engineName,engine]of Object.entries({chromium,firefox,webkit})){
  if(process.env.WR_OPERATIONS_ENGINE&&process.env.WR_OPERATIONS_ENGINE!==engineName)continue;
  const browser=await engine.launch({headless:true});
  try{for(const actor of actors){
   console.log('ACTOR: '+engineName+' '+actor.role);
   const context=await browser.newContext({ignoreHTTPSErrors:true,viewport:{width:1586,height:992}});
   const value=actor.cookie.split('; ').find(v=>v.startsWith('__Host-wrs=')).slice('__Host-wrs='.length);
   await context.addCookies([{name:'__Host-wrs',value,url:origin,secure:true,httpOnly:true,sameSite:'Strict'}]);await context.addInitScript(value=>sessionStorage.setItem('wr.admin.csrf.v1',value),actor.csrf);
   await context.route(origin+'/**',async route=>{try{const headers={...route.request().headers(),host:'127.0.0.1:19443'};if(headers.origin===origin)headers.origin='https://127.0.0.1:19443';await route.fulfill({response:await route.fetch({headers,timeout:15000})});}catch{try{await route.abort();}catch{/* Context closed during a poll. */}}});
   context.setDefaultTimeout(30000);context.setDefaultNavigationTimeout(30000);
   const page=await context.newPage(),errors=[];page.on('pageerror',()=>errors.push('pageerror'));page.on('console',message=>{if(['warning','error'].includes(message.type())&&!((engineName==='webkit'&&message.text()==="Refused to apply a stylesheet because its hash, its nonce, or 'unsafe-inline' does not appear in the style-src directive of the Content Security Policy.")))errors.push(message.type()+': '+message.text());});
   const routes=['/','/rooms','/rooms/setup_room/settings','/rooms/setup_room/operations','/rooms/setup_room/schedule','/rooms/setup_room/verification','/traffic-lab','/settings','/rooms/new'];
   for(const url of routes){
    console.log('CHECKING: '+engineName+' '+actor.role+' '+url);
    await page.goto(origin+url);await expect(page.locator('main h2').first()).toBeVisible();await expect(page).toHaveTitle('Waiting Room · 관리자 콘솔');await expect(page.locator('main')).not.toBeEmpty();
    if(url==='/settings'&&actor.role!=='admin')await expect(page.getByRole('heading',{name:'보안 설정 권한이 필요합니다'})).toBeVisible();
    if(url==='/rooms'){
     await expect(page.getByRole('button',{name:'이전 감사 기록 더 보기'})).toBeVisible();await page.getByRole('button',{name:'이전 감사 기록 더 보기'}).click();await expect.poll(()=>page.locator('.audit-list>li').count()).toBeGreaterThan(50);
    }
    await page.setViewportSize({width:320,height:900});if(!await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth))accessibilityFailures.push({engineName,role:actor.role,url,overflow:await page.locator('main').evaluate(el=>[...el.querySelectorAll('*')].filter(n=>n.getBoundingClientRect().right>innerWidth).slice(0,10).map(n=>n.tagName+'.'+n.className))});
    console.log('AXE: '+engineName+' '+actor.role+' '+url);
    const result=await new AxeBuilder({page}).withTags(['wcag2a','wcag2aa','wcag21a','wcag21aa','wcag22aa']).analyze();
    if(result.violations.length)accessibilityFailures.push({engineName,role:actor.role,url,violations:result.violations.map(v=>({id:v.id,targets:v.nodes.map(n=>n.target)}))});
    await page.setViewportSize({width:1586,height:992});
   }
   if(accessibilityFailures.length)console.log('A11Y diagnostics: '+JSON.stringify(accessibilityFailures));
   await page.goto(origin+'/rooms');await expect(page.getByRole('heading',{name:'Room 초안 관리',exact:true})).toBeFocused();
   await page.keyboard.press('Shift+Tab'); // Route heading receives focus; keyboard remains usable.
   await page.getByRole('link',{name:'본문으로 건너뛰기'}).focus();await page.keyboard.press('Enter');await expect(page.locator('#console-content')).toBeFocused();
   if(actor.role==='admin'){
    await page.goto(origin+'/settings');await expect(page.getByRole('heading',{name:'사용자 · 보안 설정',exact:true})).toBeFocused();
    const trigger=page.getByRole('button',{name:'ON 전 현재 Admin TOTP 등록',exact:true});await trigger.focus();await page.keyboard.press('Enter');const dialog=page.getByRole('dialog');await expect(dialog.getByLabel('현재 비밀번호')).toBeFocused();
    await page.keyboard.press('Shift+Tab');assert.equal(await page.evaluate(()=>Boolean(document.activeElement.closest('dialog'))),true);await page.keyboard.press('Tab');await expect(dialog.getByLabel('현재 비밀번호')).toBeFocused();
    const a11y=await new AxeBuilder({page}).withTags(['wcag2a','wcag2aa','wcag21a','wcag21aa','wcag22aa']).analyze();assert.deepEqual(a11y.violations.map(v=>({id:v.id,targets:v.nodes.map(n=>n.target)})),[],'empty reauth dialog accessibility');
    await page.keyboard.press('Escape');await expect(dialog).not.toBeVisible();await expect(trigger).toBeFocused();
    if(engineName==='chromium'){await page.setViewportSize({width:360,height:900});await page.screenshot({path:path.join(screens,'operations-security-mobile.png'),fullPage:true});}
    if(engineName==='chromium'){
     let lost=false;const sent=[];
     await context.route(origin+'/api/admin/v1/users',async route=>{
      if(route.request().method()!=='POST'){await route.fallback();return;}
      const headers={...route.request().headers(),host:'127.0.0.1:19443',origin:'https://127.0.0.1:19443'};
      const response=await route.fetch({headers});sent.push({key:headers['idempotency-key'],status:response.status(),replay:response.headers()['idempotency-replayed']});
      if(!lost){lost=true;assert.equal(response.status(),201);await route.abort('failed');}else await route.fulfill({response});
     });
     await page.getByLabel('새 사용자 ID',{exact:true}).fill('ui_response_loss');await page.getByLabel('새 사용자 비밀번호',{exact:true}).fill(crypto.randomBytes(32).toString('base64url'));
     await page.getByRole('button',{name:'계정 생성 준비',exact:true}).click();await page.getByRole('dialog').getByLabel('현재 비밀번호').fill(password);await page.getByRole('button',{name:'확인하고 실행',exact:true}).click();
     const retry=page.getByRole('button',{name:'같은 명령 다시 확인',exact:true});await expect(retry).toBeFocused({timeout:30000});await page.keyboard.press('Enter');await expect(page.getByRole('dialog')).not.toBeVisible({timeout:30000});
     assert.equal(sent.length,2);assert.equal(sent[0].key,sent[1].key);assert.equal(sent[1].replay,'true');await expect(page.locator('.user-list h4').filter({hasText:'ui_response_loss'})).toHaveCount(1);
     const audit=await call('/audit-events');assert.equal(audit.body.items.filter(x=>x.action==='users.write'&&x.targetId==='ui_response_loss').length,1);
     const expected='error: Failed to load resource: net::ERR_FAILED';assert.equal(errors.filter(value=>value===expected).length,1);errors.splice(errors.indexOf(expected),1);
     console.log('PASS: real committed UI user creation with lost HTTP response; keyboard retry reuses proof/key, one account and one audit');
     if(process.env.WR_RECOVERY_RUNTIME==='1'){
      await page.goto(origin+'/rooms/setup_room/operations');
      await page.getByRole('button',{name:'재인증 후 설치 전체 새 epoch 복구',exact:true}).click();
      const resetDialog=page.getByRole('dialog');await expect(resetDialog).toContainText('최소 60분 30초');await expect(resetDialog).not.toContainText('인증 앱의 다음 코드');
      await page.setViewportSize({width:360,height:900});await page.screenshot({path:path.join(screens,'new-epoch-review-mobile.png'),fullPage:true});
      await resetDialog.getByLabel('현재 비밀번호').fill(password);await resetDialog.getByRole('button',{name:'확인하고 실행',exact:true}).click();await expect(resetDialog).not.toBeVisible({timeout:30000});
      await expect.poll(async()=>{const d=(await call('/config/delivery')).body;const m=d.nodes.find(n=>n.id==='coordinator')?.rooms[0];return d.state==='applied'&&m?.epoch===2&&m.mode==='RECOVERY_HOLD';},{timeout:30000}).toBe(true);
      console.log('PASS: actual Admin UI submits generation-bound epoch reset; both nodes ACK epoch 2 and enforce safety hold');
     }

    }

   }
   await context.unrouteAll({behavior:'wait'});assert.deepEqual(errors,[]);await context.close();console.log('CHECKED: '+engineName+' '+actor.role+' 9 real pages, 320px reflow, WCAG axe checks, keyboard focus');
  }}finally{await browser.close();}
 }
 assert.deepEqual(accessibilityFailures,[],'all role/browser accessibility checks');
 console.log('PASS: all observed roles and browser pages have zero automated WCAG violations or mobile overflow');
 // A response lost after logout is acknowledged with original credential hashes,
 // never reactivates the session, and cannot change its bound CSRF/key.
 const viewer=actors[2],logoutHeaders={Cookie:viewer.cookie,'X-CSRF-Token':viewer.csrf,'Idempotency-Key':crypto.randomUUID()};
 assert.equal((await call('/auth/logout','POST',undefined,logoutHeaders)).status,200);const replay=await call('/auth/logout','POST',undefined,logoutHeaders);assert.equal(replay.status,200);assert.equal(replay.headers['idempotency-replayed'],'true');assert.equal((await call('/auth/me','GET',undefined,{Cookie:viewer.cookie})).status,401);
 console.log('PASS: '+checked.size+' observed API operation contracts; keyed logout response-loss retry and session revocation');
}
