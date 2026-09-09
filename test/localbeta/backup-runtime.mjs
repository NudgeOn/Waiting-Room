// SPDX-License-Identifier: Apache-2.0
// Independent Compose project, image and alternate loopback host ports. Existing
// installations are never stopped or selected. Fixture volumes are preserved.
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import crypto from 'node:crypto';
import https from 'node:https';
import net from 'node:net';
import {spawn,spawnSync} from 'node:child_process';
import {setTimeout as delay} from 'node:timers/promises';
import YAML from 'yaml';
import {chromium,expect} from '@playwright/test';
import {adminResponse} from './contracts.mjs';
import {operationsChecks} from './operations.mjs';
import {newRoom} from '../../apps/admin/src/control-api.js';
import {waitForRuntime} from '../../scripts/runtime-readiness.mjs';

assert.equal(process.env.WR_TEST_TRAFFIC_DOCKER,'local');
const project='waiting-room-recovery-test-'+crypto.randomBytes(4).toString('hex'),temp=fs.mkdtempSync(path.join(os.tmpdir(),project+'-')),file=path.join(temp,'compose.yaml');
const compose=YAML.parse(fs.readFileSync('deploy/compose/local-beta.yaml','utf8'));
delete compose.name;
for(const service of Object.values(compose.services)){if(service.image==='waiting-room-local-control:dev')service.image='waiting-room-operations-test:local';delete service.build;}
compose.services.control.ports=['127.0.0.1:29443:19443'];compose.services.gateway.ports=['127.0.0.1:30443:20443'];
for(const [name,secret] of Object.entries(compose.secrets)){secret.file=path.join(temp,name);fs.writeFileSync(secret.file,crypto.randomBytes(32).toString('hex'),{mode:0o600});}
fs.writeFileSync(file,YAML.stringify(compose));
function docker(...args){const out=spawnSync('docker',['compose','-p',project,'-f',file,...args],{encoding:'utf8',timeout:180000,maxBuffer:1024*1024});assert.equal(out.status,0,'isolated Compose '+args[0]+' succeeded; credential output suppressed');return out.stdout.trim();}
let restoredProject='',restoredFile='';const children=new Set(),sockets=new Set();let server,browser,cookie='',csrf='';
function request(port,url,method='GET',body,headers={}){return new Promise((resolve,reject)=>{const data=body===undefined?undefined:JSON.stringify(body),logical=port===29444?19444:19443;const requestHeaders={Host:`127.0.0.1:${logical}`,Origin:`https://127.0.0.1:${logical}`,...(data?{'Content-Type':'application/json','Content-Length':Buffer.byteLength(data)}:{}),...(cookie?{Cookie:cookie}:{}),...headers};if(requestHeaders.Cookie==='')delete requestHeaders.Cookie;const req=https.request({hostname:'127.0.0.1',port,path:'/api/admin/v1'+url,method,agent:false,rejectUnauthorized:false,timeout:30000,headers:requestHeaders},res=>{let raw='';res.on('data',part=>raw+=part);res.on('end',()=>{if(res.headers['set-cookie']&&!Object.hasOwn(headers,'Cookie'))cookie=res.headers['set-cookie'].map(v=>v.split(';')[0]).join('; ');let body;try{body=JSON.parse(raw);}catch{reject(Error('expected JSON fixture response'));return;}try{if(process.env.WR_OPERATIONS_CHECK==='1')adminResponse(method,url,res.statusCode,res.headers,body);}catch(error){reject(error);return;}resolve({status:res.statusCode,body,etag:res.headers.etag,headers:res.headers});});});req.on('error',()=>reject(Error('isolated fixture connection failed')));req.on('timeout',()=>req.destroy());req.end(data);});}
async function until(check){for(let i=0;i<80;i++){if(await check())return;await delay(500);}throw Error('isolated runtime did not apply setup');}
async function startApplications(){
  docker('up','-d','control','gateway','coordinator','demo-origin');
  await waitForRuntime(()=>docker('ps','--all','--format','json'),{timeout:240000,interval:2000});
}
try{
  docker('up','-d','--wait','postgres');docker('run','--rm','initialize','init','on');docker('up','-d','--wait','valkey');docker('run','--rm','queue-initialize');docker('up','-d','--wait','control','coordinator','gateway','demo-origin');docker('run','--rm','bootstrap');
  const token=docker('exec','-T','control','/wr-control','token');assert.equal(token.length,43);
  server=net.createServer(socket=>{sockets.add(socket);const child=spawn('docker',['compose','-p',project,'-f',file,'exec','-T','control','/wr-control','tunnel'],{stdio:['pipe','pipe','ignore']});children.add(child);socket.pipe(child.stdin);child.stdout.pipe(socket);socket.on('error',()=>{});child.stdin.on('error',()=>socket.destroy());child.on('error',()=>socket.destroy());child.on('exit',()=>{children.delete(child);socket.destroy();});socket.on('close',()=>{sockets.delete(socket);child.kill('SIGTERM');});});
  await new Promise((resolve,reject)=>{server.once('error',reject);server.listen(29444,'127.0.0.1',resolve);});
  const auth={'X-WR-Auth':'1','X-Bootstrap-Token':token};
  async function setup(step,body={}){const out=await request(29444,'/setup/'+step,'POST',body,auth);assert.equal(out.status,200,'setup '+step);return out.body;}
  const inspected=await setup('inspect'),measured=await setup('calibrate');assert.equal(measured.calibration.targetMet,true);
  const input={...inspected.input,regionId:'docker-traffic',limits:{maxActiveAdmissionLeases:7,admissionsPerMinute:23,admissionTtlSeconds:60},totp:{mode:'configurable',enabled:false}};
  const review=await setup('plan',input),applied=await setup('apply',{input,planDigest:review.planDigest,calibrationDigest:review.calibrationDigest});
  assert.equal(applied.environment.os,'linux');assert.equal(applied.plan.input.regionId,input.regionId);
  console.log('PASS: six-role Docker installation, actual Linux Control calibration, reviewed policy/region/defaults apply');
  docker('restart','postgres','control');docker('up','-d','--wait','control');assert.deepEqual((await setup('inspect')).report,applied);
  console.log('PASS: PostgreSQL and Control restart preserve exact setup report and calibrated parameters before bootstrap');
  const password=crypto.randomBytes(32).toString('base64url');const account=await request(29444,'/bootstrap','POST',{username:'traffic_docker',password},auth);assert.equal(account.status,200);assert.equal(account.body.state,'authenticated');csrf=account.body.csrfToken;
  const draft=await request(29443,'/config/draft');assert.equal(draft.status,200);assert.equal(draft.body.regionId,'docker-traffic');
  const room={...newRoom(),id:'setup_room',name:'Traffic isolation verification',hostname:'127.0.0.1',origin:'https://demo-origin:20445',healthURL:'https://demo-origin:20445/health',limits:applied.plan.input.limits,active:true};
  const saved=await request(29443,'/config/draft','PUT',{...draft.body,rooms:[room]},{'X-CSRF-Token':csrf,'If-Match':draft.etag,'Idempotency-Key':crypto.randomUUID()});assert.equal(saved.status,200);
  const published=await request(29443,'/config/publish','POST',{}, {'X-CSRF-Token':csrf,'If-Match':saved.etag,'Idempotency-Key':crypto.randomUUID()});assert.equal(published.status,202);
  await until(async()=>{const out=await request(29443,'/config/delivery');return out.status===200&&out.body.state==='applied'&&out.body.config.regionId===input.regionId&&out.body.nodes.length===2&&out.body.nodes.every(n=>n.generation===out.body.generation);});
  console.log('PASS: applied installation region and Room defaults reach signed runtime with both Gateway/Coordinator ACK');

  async function dataRequest(method,url,body,headers={}){return new Promise((resolve,reject)=>{const data=body===undefined?undefined:JSON.stringify(body);const req=https.request({hostname:'127.0.0.1',port:30443,path:url,method,agent:false,rejectUnauthorized:false,timeout:10000,headers:{Host:'127.0.0.1:20443',...(data?{'Content-Type':'application/json','Content-Length':Buffer.byteLength(data)}:{}),...headers}},res=>{let raw='';res.on('data',chunk=>raw+=chunk);res.on('end',()=>resolve({status:()=>res.statusCode,json:async()=>JSON.parse(raw),raw}));});req.on('error',()=>reject(Error('fresh data-plane connection failed')));req.on('timeout',()=>req.destroy());req.end(data);});}
  function rawDocker(...args){const out=spawnSync('docker',args,{encoding:'utf8',timeout:180000,maxBuffer:1024*1024});if(out.status!==0){fs.writeFileSync(path.join(os.tmpdir(),'wr-restore-docker-error.log'),out.stderr??'',{mode:0o600});}assert.equal(out.status,0,'isolated archive/restore '+args[0]+' / '+args.at(-1)+' succeeded; credential output suppressed');return out.stdout.trim();}
  const volumeNames=['database','state','identities','gateway-data','coordinator-data','queue-config','queue-data'];
  function archive(name,dest,action){const args=['run','--rm','--network','none','--read-only','--user','0:0','--cap-drop','ALL','--cap-add','CHOWN','--cap-add','DAC_OVERRIDE','--security-opt','no-new-privileges','--mount','type=bind,src='+dest+',dst=/backup'];for(const v of volumeNames)args.push('--mount','type=volume,src='+name+'_'+v+',dst=/volumes/'+v+(action==='archive-create'?',readonly':''));return rawDocker(...args,'--entrypoint','/wr-control',(process.env.WR_TEST_CANDIDATE_IMAGE||(process.env.WR_TEST_PUBLIC_CANDIDATE==='1'?'waiting-room-public-beta-test:local':'waiting-room-recovery-test:local')),action);}
  const joinKey=crypto.randomUUID(),payload={target:'/shop/cart?restore=1'};
  const first=await dataRequest('POST','/_wr/v1/tickets',payload,{'Idempotency-Key':joinKey});assert.equal(first.status(),202);const ticket=await first.json();
  const second=await dataRequest('POST','/_wr/v1/tickets',{target:'/shop/cart?restore=2'},{'Idempotency-Key':crypto.randomUUID()});assert.equal(second.status(),202);
  const savedDraft=(await request(29443,'/config/draft')).body;
  const backup=fs.mkdtempSync(path.join(os.tmpdir(),'wr-cold-backup-'));fs.chmodSync(backup,0o700);
  docker('stop');assert.equal(rawDocker('ps','--filter','label=com.docker.compose.project='+project,'--format','{{.ID}}'),'');
  const manifest=JSON.parse(archive(project,backup,'archive-create'));assert.equal(manifest.schema,1);assert.ok(manifest.entries>100);assert.ok(manifest.bytes>1000000);
  assert.deepEqual(JSON.parse(archive(project,backup,'archive-verify')),manifest);
  console.log('PASS: seven stopped volumes archived together; private SHA-256 and full tar structure verified; bytes='+manifest.bytes);
  // Release only this stopped source fixture's six networks; keep every volume.
  docker('down');
  restoredProject='waiting-room-restore-test-'+crypto.randomBytes(4).toString('hex');restoredFile=path.join(temp,'restored.yaml');
  for(const v of volumeNames)rawDocker('volume','create','--label','com.docker.compose.project='+restoredProject,'--label','com.docker.compose.volume='+v,restoredProject+'_'+v);
  archive(restoredProject,backup,'archive-restore');fs.writeFileSync(restoredFile,YAML.stringify(compose));
  const restored=(...args)=>rawDocker('compose','-p',restoredProject,'-f',restoredFile,...args);
  restored('up','-d','--wait','postgres','valkey');restored('up','-d','control','coordinator','gateway','demo-origin');
  await waitForRuntime(()=>restored('ps','--all','--format','json'),{timeout:240000,interval:2000});
  const restoredReplay=await dataRequest('POST','/_wr/v1/tickets',payload,{'Idempotency-Key':joinKey});assert.equal(restoredReplay.status(),202);assert.deepEqual(await restoredReplay.json(),ticket);
  assert.deepEqual((await request(29443,'/config/draft')).body,savedDraft);
  console.log('PASS: cold restore into new volumes preserves PostgreSQL account/session/draft, mTLS identities, signed node cache and exact v4 queue replay after real recovery hold');
  // Capture a second consistent snapshot before upgrading the restored fixture.
  restored('stop');const beforeUpgrade=fs.mkdtempSync(path.join(os.tmpdir(),'wr-pre-upgrade-'));fs.chmodSync(beforeUpgrade,0o700);archive(restoredProject,beforeUpgrade,'archive-create');
  for(const service of Object.values(compose.services)){if(service.image==='waiting-room-operations-test:local')service.image=(process.env.WR_TEST_CANDIDATE_IMAGE||(process.env.WR_TEST_PUBLIC_CANDIDATE==='1'?'waiting-room-public-beta-test:local':'waiting-room-recovery-test:local'));}
  fs.writeFileSync(restoredFile,YAML.stringify(compose));restored('up','-d','--wait','postgres');restored('run','--rm','--no-deps','initialize','upgrade');restored('up','-d','--wait','valkey');
  const migrated=JSON.parse(restored('run','--rm','--no-deps','queue-initialize','queue-upgrade'));assert.equal(migrated.from,4);assert.equal(migrated.to,5);assert.equal(migrated.state,'recovery_hold');assert.ok(migrated.retainedVisitors>=2);assert.ok(migrated.unsafeUntil-Date.now()>85000);
  restored('up','-d','control','coordinator','gateway','demo-origin');
  await waitForRuntime(()=>restored('ps','--all','--format','json'),{timeout:240000,interval:2000});
  await until(async()=>{const d=(await request(29443,'/config/delivery')).body;return d.state==='applied'&&d.nodes.find(n=>n.id==='coordinator')?.rooms[0]?.mode==='HOLD';});
  const upgraded=await dataRequest('POST','/_wr/v1/tickets',payload,{'Idempotency-Key':joinKey});assert.equal(upgraded.status(),202);assert.deepEqual(await upgraded.json(),ticket);assert.deepEqual((await request(29443,'/config/draft')).body,savedDraft);
  console.log('PASS: backed-up real v4 → v5 upgrade, original ACL credentials retained, actual safety wait and bounded retained-row validation, exact HTTP replay and account/draft retained');
  if(process.env.WR_TEST_KEYS==='1'){
    for(const operation of ['stage','activate']){
      restored('stop','control','gateway','coordinator');restored('run','--rm','initialize','keys-'+operation);
      restored('up','-d','control','coordinator','gateway');
      await until(async()=>JSON.parse(restored('run','--rm','initialize','keys-status')).acknowledged===2);
    }
    const keyState=JSON.parse(restored('run','--rm','initialize','keys-status'));assert.equal(keyState.phase,'active');
    const rotationJoinKey=crypto.randomUUID(),rotationPayload={target:'/shop/rotated-backup'};
    const rotationJoin=await dataRequest('POST','/_wr/v1/tickets',rotationPayload,{'Idempotency-Key':rotationJoinKey});assert.equal(rotationJoin.status(),202);
    const rotationBackup=fs.mkdtempSync(path.join(os.tmpdir(),'wr-rotated-backup-'));fs.chmodSync(rotationBackup,0o700);
    restored('stop');archive(restoredProject,rotationBackup,'archive-create');archive(restoredProject,rotationBackup,'archive-verify');restored('down');
    restoredProject='waiting-room-rotated-restore-'+crypto.randomBytes(4).toString('hex');
    for(const v of volumeNames)rawDocker('volume','create','--label','com.docker.compose.project='+restoredProject,'--label','com.docker.compose.volume='+v,restoredProject+'_'+v);
    archive(restoredProject,rotationBackup,'archive-restore');
    restored('up','-d','--wait','postgres','valkey');restored('up','-d','control','coordinator','gateway','demo-origin');
    await waitForRuntime(()=>restored('ps','--all','--format','json'),{timeout:240000,interval:2000});
    await until(async()=>JSON.parse(restored('run','--rm','initialize','keys-status')).acknowledged===2);
    const restoredKeys=JSON.parse(restored('run','--rm','initialize','keys-status'));assert.equal(restoredKeys.digest,keyState.digest);assert.equal(restoredKeys.retireAfter,keyState.retireAfter);
    const newReplay=await dataRequest('POST','/_wr/v1/tickets',rotationPayload,{'Idempotency-Key':rotationJoinKey});assert.equal(newReplay.status(),202);assert.equal(newReplay.raw,rotationJoin.raw);
    assert.equal((await dataRequest('POST','/_wr/v1/tickets',payload,{'Idempotency-Key':joinKey})).raw,first.raw);
    console.log('PASS: third cold restore preserves rotated role keys, private owner journal, original retirement deadline, both key ACKs and old/new encrypted join responses; backup='+rotationBackup);
  }
  browser=await chromium.launch({headless:true});const context=await browser.newContext({ignoreHTTPSErrors:true,viewport:{width:360,height:900}});await context.addCookies([{name:'__Host-wrs',value:cookie.split('; ').find(v=>v.startsWith('__Host-wrs=')).slice('__Host-wrs='.length),url:'https://127.0.0.1:29443',secure:true,httpOnly:true,sameSite:'Strict'}]);await context.addInitScript(value=>sessionStorage.setItem('wr.admin.csrf.v1',value),csrf);
  await context.route('https://127.0.0.1:29443/**',async route=>{const headers={...route.request().headers(),host:'127.0.0.1:19443'};if(headers.origin==='https://127.0.0.1:29443')headers.origin='https://127.0.0.1:19443';try{const response=await route.fetch({headers});await route.fulfill({response});}catch{await route.abort();}});
  const page=await context.newPage();await page.goto('https://127.0.0.1:29443/rooms/setup_room/operations');await page.getByRole('button',{name:'재인증 후 설치 전체 새 epoch 복구',exact:true}).click();await expect(page.getByRole('dialog')).toContainText('최소 60분 30초');await expect(page.getByRole('dialog')).toContainText('설치 전체 1개 Room');assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);const screens=path.join(os.tmpdir(),'wr-beta-recovery-qa');fs.mkdirSync(screens,{recursive:true});await page.screenshot({path:path.join(screens,'new-epoch-review-mobile.png'),fullPage:true});await page.keyboard.press('Escape');await expect(page.getByRole('dialog')).not.toBeVisible();await context.unrouteAll({behavior:'wait'});await browser.close();browser=null;
  console.log('PASS: restored/upgraded Admin UI reviews installation-wide impact and safety wait; 360px and Escape work without issuing another reset');
  const current=await request(29443,'/rooms/setup_room/runtime');assert.equal((await request(29443,'/rooms/setup_room/runtime','PATCH',{action:'auto'},{'X-CSRF-Token':csrf,'If-Match':current.etag,'Idempotency-Key':crypto.randomUUID()})).status,200);
  await until(async()=>{const out=await dataRequest('GET',ticket.statusUrl,undefined,{Authorization:'Bearer '+ticket.ticketToken});return [200,202].includes(out.status())&&(await out.json()).state==='ready';});
  const claim=await dataRequest('POST','/_wr/v1/rooms/'+room.publicId+'/admissions',undefined,{Authorization:'Bearer '+ticket.ticketToken});assert.equal(claim.status(),200);const admitted=await claim.json();assert.equal((await dataRequest('GET','/shop/cart',undefined,{'X-Waiting-Room-Admission':admitted.admissionToken})).status(),200);
  console.log('PASS: pre-upgrade ticket retains FIFO position and claims admission through restored mTLS origin');
  console.log('Fixture: '+restoredProject+'; cold backup retained at '+backup+'; pre-upgrade backup '+beforeUpgrade);
}finally{if(browser)await browser.close();for(const socket of sockets)socket.destroy();for(const child of children)child.kill('SIGTERM');if(server)await new Promise(resolve=>server.close(resolve));docker('down');if(restoredProject){const out=spawnSync('docker',['compose','-p',restoredProject,'-f',restoredFile,'down'],{stdio:'ignore',timeout:60000});if(out.status!==0)process.exitCode=1;}}
