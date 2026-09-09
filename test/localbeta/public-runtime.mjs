// SPDX-License-Identifier: Apache-2.0
// Independent Compose project, image and alternate loopback host ports. Existing
// installations are never stopped or selected. Fixture volumes are preserved.
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import crypto from 'node:crypto';
import https from 'node:https';
import http from 'node:http';
import net from 'node:net';
import {spawn,spawnSync} from 'node:child_process';
import {setTimeout as delay} from 'node:timers/promises';
import YAML from 'yaml';
import {chromium,expect} from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import {publicResponse,publicSchema} from './public-contracts.mjs';
import {adminResponse} from './contracts.mjs';
import {newRoom} from '../../apps/admin/src/control-api.js';
import {waitForRuntime} from '../../scripts/runtime-readiness.mjs';
import {browserJoinChecks} from './browser-join.mjs';

assert.equal(process.env.WR_TEST_TRAFFIC_DOCKER,'local');
const project='waiting-room-public-test-'+crypto.randomBytes(4).toString('hex'),temp=fs.mkdtempSync(path.join(os.tmpdir(),project+'-')),file=path.join(temp,'compose.yaml');
const compose=YAML.parse(fs.readFileSync('deploy/compose/local-beta.yaml','utf8'));
delete compose.name;
for(const service of Object.values(compose.services)){if(service.image==='waiting-room-local-control:dev')service.image=process.env.WR_TEST_CANDIDATE_IMAGE||'waiting-room-public-beta-test:local';delete service.build;}
compose.services.control.ports=['127.0.0.1:29473:19443'];compose.services.gateway.ports=['127.0.0.1:30473:20443'];
for(const [name,secret] of Object.entries(compose.secrets)){secret.file=path.join(temp,name);fs.writeFileSync(secret.file,crypto.randomBytes(32).toString('hex'),{mode:0o600});}
fs.writeFileSync(file,YAML.stringify(compose));
function docker(...args){const out=spawnSync('docker',['compose','-p',project,'-f',file,...args],{encoding:'utf8',timeout:180000,maxBuffer:1024*1024});assert.equal(out.status,0,'isolated Compose '+args[0]+' succeeded; credential output suppressed');return out.stdout.trim();}
const children=new Set(),sockets=new Set();let server,browserProxy,browser,cookie='',csrf='';
function request(port,url,method='GET',body,headers={}){return new Promise((resolve,reject)=>{const data=body===undefined?undefined:JSON.stringify(body),logical=port===29474?19444:19443;const requestHeaders={Host:`127.0.0.1:${logical}`,Origin:`https://127.0.0.1:${logical}`,...(data?{'Content-Type':'application/json','Content-Length':Buffer.byteLength(data)}:{}),...(cookie?{Cookie:cookie}:{}),...headers};if(requestHeaders.Cookie==='')delete requestHeaders.Cookie;const req=https.request({hostname:'127.0.0.1',port,path:'/api/admin/v1'+url,method,agent:false,rejectUnauthorized:false,timeout:30000,headers:requestHeaders},res=>{let raw='';res.on('data',part=>raw+=part);res.on('end',()=>{if(res.headers['set-cookie']&&!Object.hasOwn(headers,'Cookie'))cookie=res.headers['set-cookie'].map(v=>v.split(';')[0]).join('; ');let body;try{body=JSON.parse(raw);}catch{reject(Error('expected JSON fixture response'));return;}try{if(process.env.WR_OPERATIONS_CHECK==='1')adminResponse(method,url,res.statusCode,res.headers,body);}catch(error){reject(error);return;}resolve({status:res.statusCode,body,etag:res.headers.etag,headers:res.headers});});});req.on('error',()=>reject(Error('isolated fixture connection failed')));req.on('timeout',()=>req.destroy());req.end(data);});}
async function until(check){for(let i=0;i<40;i++){if(await check())return;await delay(500);}throw Error('isolated runtime did not apply setup');}
async function startApplications(){
  docker('up','-d','control','gateway','coordinator','demo-origin');
  await waitForRuntime(()=>docker('ps','--all','--format','json'),{timeout:240000,interval:2000});
}
try{
  docker('up','-d','--wait','postgres');docker('run','--rm','initialize','init','on');docker('up','-d','--wait','valkey');docker('run','--rm','queue-initialize');docker('up','-d','--wait','control','coordinator','gateway','demo-origin');docker('run','--rm','bootstrap');
  const token=docker('exec','-T','control','/wr-control','token');assert.equal(token.length,43);
  server=net.createServer(socket=>{sockets.add(socket);const child=spawn('docker',['compose','-p',project,'-f',file,'exec','-T','control','/wr-control','tunnel'],{stdio:['pipe','pipe','ignore']});children.add(child);socket.pipe(child.stdin);child.stdout.pipe(socket);socket.on('error',()=>{});child.stdin.on('error',()=>socket.destroy());child.on('error',()=>socket.destroy());child.on('exit',()=>{children.delete(child);socket.destroy();});socket.on('close',()=>{sockets.delete(socket);child.kill('SIGTERM');});});
  await new Promise((resolve,reject)=>{server.once('error',reject);server.listen(29474,'127.0.0.1',resolve);});
  const auth={'X-WR-Auth':'1','X-Bootstrap-Token':token};
  async function setup(step,body={}){const out=await request(29474,'/setup/'+step,'POST',body,auth);assert.equal(out.status,200,'setup '+step);return out.body;}
  const inspected=await setup('inspect'),measured=await setup('calibrate');assert.equal(measured.calibration.targetMet,true,'actual calibration '+JSON.stringify(measured.calibration));
  const input={...inspected.input,regionId:'docker-traffic',limits:{maxActiveAdmissionLeases:7,admissionsPerMinute:23,admissionTtlSeconds:process.env.WR_TEST_KEYS==='1'?300:60},totp:{mode:'configurable',enabled:false}};
  const review=await setup('plan',input),applied=await setup('apply',{input,planDigest:review.planDigest,calibrationDigest:review.calibrationDigest});
  assert.equal(applied.environment.os,'linux');assert.equal(applied.plan.input.regionId,input.regionId);
  console.log('PASS: six-role Docker installation, actual Linux Control calibration, reviewed policy/region/defaults apply');
  docker('restart','postgres','control');docker('up','-d','--wait','control');assert.deepEqual((await setup('inspect')).report,applied);
  console.log('PASS: PostgreSQL and Control restart preserve exact setup report and calibrated parameters before bootstrap');
  const password=crypto.randomBytes(32).toString('base64url');const account=await request(29474,'/bootstrap','POST',{username:'traffic_docker',password},auth);assert.equal(account.status,200);assert.equal(account.body.state,'authenticated');csrf=account.body.csrfToken;
  const draft=await request(29473,'/config/draft');assert.equal(draft.status,200);assert.equal(draft.body.regionId,'docker-traffic');
  const room={...newRoom(),id:'setup_room',name:'Traffic isolation verification',hostname:'127.0.0.1',origin:'https://demo-origin:20445',healthURL:'https://demo-origin:20445/health',limits:applied.plan.input.limits,active:true};
  const saved=await request(29473,'/config/draft','PUT',{...draft.body,rooms:[room]},{'X-CSRF-Token':csrf,'If-Match':draft.etag,'Idempotency-Key':crypto.randomUUID()});assert.equal(saved.status,200);
  const published=await request(29473,'/config/publish','POST',{}, {'X-CSRF-Token':csrf,'If-Match':saved.etag,'Idempotency-Key':crypto.randomUUID()});assert.equal(published.status,202);
  await until(async()=>{const out=await request(29473,'/config/delivery');return out.status===200&&out.body.state==='applied'&&out.body.config.regionId===input.regionId&&out.body.nodes.length===2&&out.body.nodes.every(n=>n.generation===out.body.generation);});
  console.log('PASS: applied installation region and Room defaults reach signed runtime with both Gateway/Coordinator ACK');

  const operations=new Set();
  async function dataRequest(method,url,body,headers={}){return new Promise((resolve,reject)=>{const data=body===undefined?undefined:JSON.stringify(body);const req=https.request({hostname:'127.0.0.1',port:30473,path:url,method,agent:false,rejectUnauthorized:false,timeout:15000,headers:{Host:'127.0.0.1:20443',...(data?{'Content-Type':'application/json','Content-Length':Buffer.byteLength(data)}:{}),...headers}},res=>{let raw='';res.on('data',chunk=>raw+=chunk);res.on('end',()=>{let body;try{body=JSON.parse(raw);}catch{body=null;}resolve({status:res.statusCode,body,headers:res.headers,raw});});});req.on('error',()=>reject(Error('isolated public fixture connection failed')));req.on('timeout',()=>req.destroy());req.end(data);});}
  async function api(method,url,body,headers={}){const out=await dataRequest(method,url,body,headers);operations.add(publicResponse(method,url,out.status,out.headers,out.body));return out;}
  async function allowedStatus(ticket,headers){for(let i=0;i<3;i++){const out=await api('GET',ticket.statusUrl,undefined,headers);if(out.status!==429)return out;await delay(Number(out.headers['retry-after'])*1000+50);}throw Error('status did not become available at advertised deadline');}
  async function command(action){const rt=await request(29473,'/rooms/setup_room/runtime');const out=await request(29473,'/rooms/setup_room/runtime','PATCH',{action},{'X-CSRF-Token':csrf,'If-Match':rt.etag,'Idempotency-Key':crypto.randomUUID()});assert.equal(out.status,200);await until(async()=>{const d=(await request(29473,'/config/delivery')).body;return d.state==='applied';});}
  const key=crypto.randomUUID(),body={target:'/shop/cart'};
  const joined=await api('POST','/_wr/v1/tickets',body,{'Idempotency-Key':key});assert.equal(joined.status,202);const ticket=joined.body,authTicket={Authorization:'Bearer '+ticket.ticketToken};assert.ok(ticket.pollAfterMs>=3000&&ticket.pollAfterMs<=20000);
  const original=await api('POST','/_wr/v1/tickets',body,{'Idempotency-Key':key});assert.deepEqual(original.body,ticket);
  const early=await api('GET',ticket.statusUrl,undefined,authTicket);assert.equal(early.status,429);assert.equal(early.body.code,'API_RATE_LIMITED');assert.ok(Number(early.headers['retry-after'])>=1);
  const simultaneous=await Promise.all(Array.from({length:20},()=>api('GET',ticket.statusUrl,undefined,authTicket)));assert.ok(simultaneous.every(x=>x.status===429));
  await delay(Number(early.headers['retry-after'])*1000+50);
  const visible=await api('GET',ticket.statusUrl,undefined,authTicket);assert.equal(visible.status,202);assert.equal(visible.body.state,'queued');
  assert.equal((await api('POST','/_wr/v1/rooms/'+room.publicId+'/heartbeat',undefined,authTicket)).status,204);
  assert.equal((await api('POST','/_wr/v1/rooms/'+room.publicId+'/admissions',undefined,authTicket)).status,409);
  console.log('PASS: actual HTTPS join retry, shared early-poll 429 and Retry-After, 20 concurrent denied polls, scheduled read, heartbeat and held claim');
  browserProxy=http.createServer((req,res)=>{res.writeHead(403);res.end();});
  browserProxy.on('connect',(req,socket,head)=>{if(req.url!=='127.0.0.1:20443'){socket.destroy();return;}const upstream=net.connect(30473,'127.0.0.1',()=>{socket.write('HTTP/1.1 200 Connection Established\r\n\r\n');if(head.length)upstream.write(head);socket.pipe(upstream);upstream.pipe(socket);});sockets.add(socket);sockets.add(upstream);for(const s of [socket,upstream]){s.on('error',()=>{socket.destroy();upstream.destroy();});s.on('close',()=>sockets.delete(s));}});
  await new Promise((resolve,reject)=>{browserProxy.once('error',reject);browserProxy.listen(39473,'127.0.0.1',resolve);});
  browser=await chromium.launch({headless:true,proxy:{server:'http://127.0.0.1:39473'},args:['--proxy-bypass-list=<-loopback>']});const context=await browser.newContext({ignoreHTTPSErrors:true,viewport:{width:360,height:900}});const origin='https://127.0.0.1:20443';
  await browserJoinChecks({browser,origin,room:room.publicId,dataRequest,api});
  const page=await context.newPage();let limited=false;page.on('response',r=>{if(r.status()>=400)console.log('Browser response',r.status(),new URL(r.url()).pathname);if(r.url().includes('/status')&&r.status()===429)limited=true;});await page.goto(origin+'/shop');await expect(page.locator('main')).toBeVisible();await expect.poll(()=>limited,{timeout:10000}).toBe(true);await expect(page.locator('body')).toHaveAttribute('data-state','queued');assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);
  await page.reload();await expect(page.locator('main')).toBeVisible();await page.goto(origin+'/shop/cart?tab=2');await expect(page.locator('main')).toBeVisible();await expect(page.locator('body')).toHaveAttribute('data-state','queued');await page.screenshot({path:path.join(os.tmpdir(),'wr-beta2-waiting-mobile.png'),fullPage:true});
  console.log('PASS: real mobile browser navigation keeps the waiting page through early-poll throttling');
  for(const method of ['GET','HEAD','PUT','PATCH','DELETE']){const out=await dataRequest(method,'/_wr/v1/tickets');assert.equal(out.status,405);assert.equal(out.headers.allow,'POST');assert.equal(out.headers['cache-control'],'no-store');if(method!=='HEAD')publicSchema('Problem',out.body);}
  for(const [url,method]of [[ticket.statusUrl,'GET'],['/_wr/v1/rooms/'+room.publicId+'/admissions','POST'],['/_wr/v1/rooms/'+room.publicId+'/heartbeat','POST']]){const wrong=await dataRequest(method==='GET'?'HEAD':'GET',url);assert.equal(wrong.status,405);assert.equal(wrong.headers.allow,method);}
  assert.equal((await api('POST','/_wr/v1/tickets',body,{'Idempotency-Key':[crypto.randomUUID(),crypto.randomUUID()]})).status,400);
  assert.equal((await api('GET',ticket.statusUrl,undefined,{Authorization:['Bearer '+ticket.ticketToken,'Bearer '+ticket.ticketToken]})).status,400);
  assert.equal((await api('GET',ticket.statusUrl,undefined,{...authTicket,Cookie:'__Host-wrq_'+room.publicId+'='+ticket.ticketToken})).status,400);
  const malformed=await api('POST','/_wr/v1/tickets',{target:'//evil.test'},{'Idempotency-Key':crypto.randomUUID()});assert.ok([400,404].includes(malformed.status));
  for(const method of ['GET','POST','PUT','PATCH','DELETE']){const out=await dataRequest(method,'/shop/cart',method==='GET'?undefined:{private:'never replay'});assert.equal(out.status,429);publicSchema('Problem',out.body);}
  await command('safe-drain');for(const method of ['GET','POST']){const out=await dataRequest(method,'/shop/cart',undefined,method==='GET'?{Accept:'text/html'}:{});assert.equal(out.status,503);if(method==='GET')assert.ok(out.headers['content-type'].startsWith('text/html'));else assert.equal(out.body.code,'QUEUE_DRAINING');}
  const unavailableContext=await browser.newContext({ignoreHTTPSErrors:true,locale:'ko-KR',viewport:{width:360,height:900}});const unavailablePage=await unavailableContext.newPage();const stopped=await unavailablePage.goto(origin+'/shop');assert.equal(stopped.status(),503);await expect(unavailablePage.getByRole('heading',{name:'잠시 입장을 멈췄어요'})).toBeVisible();await unavailablePage.keyboard.press('Tab');await expect(unavailablePage.getByRole('link',{name:'다시 확인하기'})).toBeFocused();const audit=await new AxeBuilder({page:unavailablePage}).withTags(['wcag2a','wcag2aa','wcag21a','wcag21aa','wcag22aa']).analyze();assert.deepEqual(audit.violations.map(v=>({id:v.id,targets:v.nodes.map(n=>n.target)})),[]);assert.equal(await unavailablePage.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);await unavailablePage.screenshot({path:path.join(os.tmpdir(),'wr-beta2-draining-mobile.png'),fullPage:true});await unavailableContext.close();await page.goto(origin+'/shop/cart?resume=drain');await expect(page.locator('main')).toBeVisible();await command('auto');await until(async()=>{const d=(await request(29473,'/config/delivery')).body;return d.nodes.find(n=>n.id==='coordinator')?.rooms[0]?.ready>=1;});
  const ready=await allowedStatus(ticket,authTicket);assert.equal(ready.status,200);assert.equal(ready.body.state,'ready');
  const claim=await api('POST','/_wr/v1/rooms/'+room.publicId+'/admissions',undefined,authTicket);assert.equal(claim.status,200);const repeated=await api('POST','/_wr/v1/rooms/'+room.publicId+'/admissions',undefined,authTicket);assert.equal(repeated.body.admissionToken,claim.body.admissionToken);
  for(const method of ['GET','POST','PUT','PATCH','DELETE']){const out=await dataRequest(method,'/shop/cart',method==='GET'?undefined:{customer:'body'}, {'X-Waiting-Room-Admission':claim.body.admissionToken,Authorization:'Bearer customer-oauth','X-WR-Source':'spoof','X-Forwarded-For':'spoof'});assert.equal(out.status,200);}
  const admittedStatus=await allowedStatus(ticket,authTicket);assert.equal(admittedStatus.status,200);assert.equal(admittedStatus.body.state,'admitted');assert.equal(admittedStatus.body.admissionToken,undefined);
  console.log('PASS: public method/problem contracts, protected unsafe methods, DRAINING 503, byte-identical claim retry and valid-admission method matrix');

  let legacyReturn=null;
  if(process.env.WR_TEST_KEYS==='1'){
    const oldToken=claim.body.admissionToken;
    const oldClaims=JSON.parse(Buffer.from(oldToken.split('.')[0],'base64url'));assert.equal(oldClaims.kid,'admission-v1');
    const queuedBefore=await api('POST','/_wr/v1/tickets',{target:'/shop/key-rotation'},{'Idempotency-Key':crypto.randomUUID()});
    // Return envelopes must survive both process replacement and key activation.
    const rotationWeb=await browser.newContext({ignoreHTTPSErrors:true});const rotationPage=await rotationWeb.newPage();
    await rotationPage.goto(origin+'/shop/key-rotation?old=return');await expect(rotationPage).toHaveURL(/\/_wr\/wait\//);const oldReturnURL=rotationPage.url();
    assert.ok(oldReturnURL.includes('/_wr/wait/'));
    legacyReturn={path:new URL(oldReturnURL).pathname+new URL(oldReturnURL).search,cookie:(await rotationWeb.cookies()).map(c=>c.name+'='+c.value).join('; ')};
    const keyStatus=()=>JSON.parse(docker('run','--rm','initialize','keys-status'));
    assert.equal(keyStatus().phase,'legacy');
    for(const [operation,phase,generation] of [['stage','staged',1],['activate','active',2]]){
      docker('stop','control','gateway','coordinator');
      const result=JSON.parse(docker('run','--rm','initialize','keys-'+operation));assert.equal(result.phase,phase);assert.equal(result.generation,generation);
      // A repeated owner command repairs files without advancing generation or auditing twice.
      const retried=JSON.parse(docker('run','--rm','initialize','keys-'+operation));assert.equal(retried.generation,generation);assert.equal(retried.digest,result.digest);
      if(operation==='stage'){const denied=spawnSync('docker',['compose','-p',project,'-f',file,'run','--rm','initialize','keys-activate'],{encoding:'utf8',timeout:30000,maxBuffer:1024*1024});assert.notEqual(denied.status,0,'activation without current ACKs rejected');}
      await startApplications();
      await waitForRuntime(()=>docker('ps','--all','--format','json'),{timeout:240000,interval:2000});
      await until(async()=>keyStatus().acknowledged===2);
      const oldAdmission=await dataRequest('GET','/shop/cart',undefined,{'X-Waiting-Room-Admission':oldToken});assert.equal(oldAdmission.status,200,'old admission during '+phase);
      const oldClaim=await api('POST','/_wr/v1/rooms/'+room.publicId+'/admissions',undefined,authTicket);assert.equal(oldClaim.body.admissionToken,oldToken,'claim retry during '+phase);
      const oldJoin=await api('POST','/_wr/v1/tickets',body,{'Idempotency-Key':key});assert.equal(oldJoin.raw,joined.raw,'encrypted join replay during '+phase);
      await rotationPage.goto(oldReturnURL);await expect(rotationPage.locator('main')).toBeVisible();
    }
    const active=keyStatus();assert.equal(active.emergencyReady,false);const deniedRevoke=spawnSync('docker',['compose','-p',project,'-f',file,'run','--rm','initialize','keys-revoke'],{encoding:'utf8',timeout:30000,maxBuffer:1024*1024});assert.notEqual(deniedRevoke.status,0,'revocation without Admin epoch reset rejected');assert.equal(active.retireAfter-active.activateAt,86430000);
    const premature=spawnSync('docker',['compose','-p',project,'-f',file,'run','--rm','initialize','keys-retire'],{encoding:'utf8',timeout:30000,maxBuffer:1024*1024});assert.notEqual(premature.status,0,'premature retirement rejected');
    assert.equal(keyStatus().generation,2);
    const auditCount=docker('exec','-T','postgres','psql','-U','wr_owner','-d','waiting_room','-Atc',"SELECT count(*) FROM waiting_room.control_audit WHERE action IN ('keys.stage','keys.activate')");assert.equal(auditCount,'2');
    await delay(Math.max(0,active.activateAt-Date.now())+1000);
    await command('auto');
    const fresh=await api('POST','/_wr/v1/tickets',{target:'/shop/new-key'},{'Idempotency-Key':crypto.randomUUID()});assert.equal(fresh.status,202);
    const freshAuth={Authorization:'Bearer '+fresh.body.ticketToken};await until(async()=>{const out=await api('POST','/_wr/v1/rooms/'+room.publicId+'/admissions',undefined,freshAuth);if(out.status!==200)return false;const claims=JSON.parse(Buffer.from(out.body.admissionToken.split('.')[0],'base64url'));assert.notEqual(claims.kid,'admission-v1');assert.equal((await dataRequest('GET','/shop/new-key',undefined,{'X-Waiting-Room-Admission':out.body.admissionToken})).status,200);return true;});
    await command('hold');await rotationWeb.close();
    console.log('PASS: actual Docker staged/active key ACKs, no duplicate audit on retries, old admission/join/return preserved, new key admission to origin, premature 24h30s retirement blocked');
  }
  await command('hold');await context.close();
  // Verify the new-source quota without crossing a fixed minute boundary.
  if(new Date().getUTCSeconds()>45)await delay((61-new Date().getUTCSeconds())*1000);
  let blocked=false,success=0;
  for(let i=0;i<602;i++){const out=await api('POST','/_wr/v1/tickets',{target:'/shop'},{'Idempotency-Key':crypto.randomUUID(),'X-WR-Source':crypto.randomBytes(32).toString('hex'),'X-Forwarded-For':'198.51.100.'+(i%250+1)});if(out.status===429){assert.equal(out.body.code,'API_RATE_LIMITED');blocked=true;break;}assert.equal(out.status,202);success++;}
  assert.ok(blocked);assert.ok(success<=600);const retry=await api('POST','/_wr/v1/tickets',body,{'Idempotency-Key':key});assert.equal(retry.status,202);assert.deepEqual(retry.body,ticket);
  console.log('PASS: spoofed source/Forwarded headers cannot bypass actual 600-new-join source quota; original idempotent replay remains available; public operations='+operations.size);
  const epochRuntime=await request(29473,'/rooms/setup_room/runtime');const epochBody={action:'new-epoch',scope:'installation',generation:(await request(29473,'/config/delivery')).body.generation};
  const epochDigest=crypto.createHash('sha256').update('wr-action/v1\nPATCH\nsetup_room\n'+epochRuntime.etag+'\n'+JSON.stringify(epochBody)).digest('hex');
  const epochProof=await request(29473,'/auth/reauth','POST',{password,action:'runtime.new_epoch',targetId:'setup_room',requestDigest:epochDigest},{'X-CSRF-Token':csrf});assert.equal(epochProof.status,200);
  const epochHeaders={'X-CSRF-Token':csrf,'X-Reauth-Token':epochProof.body.reauthToken,'If-Match':epochRuntime.etag,'Idempotency-Key':crypto.randomUUID()};
  const epoch=await request(29473,'/rooms/setup_room/runtime','PATCH',epochBody,epochHeaders);assert.equal(epoch.status,200);assert.equal(epoch.body.epoch,2);assert.deepEqual((await request(29473,'/rooms/setup_room/runtime','PATCH',epochBody,epochHeaders)).body,epoch.body);
  await until(async()=>{const d=(await request(29473,'/config/delivery')).body;const m=d.nodes.find(n=>n.id==='coordinator')?.rooms[0];return d.state==='applied'&&d.nodes.every(n=>n.generation===d.generation)&&m?.epoch===2&&m.mode==='RECOVERY_HOLD'&&m.recoveryUntil>Date.now()+3600000;});
  assert.equal((await api('POST','/_wr/v1/tickets',{target:'/shop'},{'Idempotency-Key':crypto.randomUUID()})).status,503);
  assert.notEqual((await api('GET',ticket.statusUrl,undefined,authTicket)).status,202);
  console.log('PASS: latest public guard candidate acknowledges a generation-bound new epoch and keeps new admissions closed; full safety wait is a separate epoch-runtime run');
  if(process.env.WR_TEST_KEYS==='1'){
    await until(async()=>JSON.parse(docker('run','--rm','initialize','keys-status')).emergencyReady===true);
    docker('stop','control','gateway','coordinator');
    const revoked=JSON.parse(docker('run','--rm','initialize','keys-revoke'));assert.equal(revoked.generation,3);assert.equal(revoked.phase,'stable');
    assert.equal(JSON.parse(docker('run','--rm','initialize','keys-revoke')).generation,3);
    docker('up','-d','control','coordinator','gateway');
    await until(async()=>JSON.parse(docker('run','--rm','initialize','keys-status')).acknowledged===2);
    const oldReturn=await dataRequest('GET',legacyReturn.path,undefined,{Cookie:legacyReturn.cookie,Accept:'text/html'});assert.notEqual(oldReturn.status,200);
    assert.equal((await api('POST','/_wr/v1/tickets',{target:'/shop'},{'Idempotency-Key':crypto.randomUUID()})).status,503);
    console.log('PASS: emergency key revocation requires fresh action-bound Admin epoch reset, rejects old return key, retains real recovery hold, and retries at one generation');
  }
  console.log('Fixture: '+project+'; local public admission validation only');
}finally{if(browser)await browser.close();for(const socket of sockets)socket.destroy();for(const child of children)child.kill('SIGTERM');if(browserProxy)await new Promise(resolve=>browserProxy.close(resolve));if(server)await new Promise(resolve=>server.close(resolve));docker('down');}
