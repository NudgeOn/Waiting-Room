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
import {syncFaultChecks} from './sync-faults.mjs';

assert.equal(process.env.WR_TEST_TRAFFIC_DOCKER,'local');
const project='waiting-room-traffic-test-'+crypto.randomBytes(4).toString('hex'),temp=fs.mkdtempSync(path.join(os.tmpdir(),project+'-')),file=path.join(temp,'compose.yaml');
const compose=YAML.parse(fs.readFileSync('deploy/compose/local-beta.yaml','utf8'));
delete compose.name;
for(const service of Object.values(compose.services)){if(service.image==='waiting-room-local-control:dev')service.image=process.env.WR_TEST_CANDIDATE_IMAGE||(process.env.WR_RECOVERY_RUNTIME==='1'?'waiting-room-recovery-test:local':process.env.WR_OPERATIONS_CHECK==='1'?'waiting-room-operations-test:local':'waiting-room-traffic-test:local');delete service.build;}
compose.services.control.ports=['127.0.0.1:29443:19443'];compose.services.gateway.ports=['127.0.0.1:30443:20443'];
for(const [name,secret] of Object.entries(compose.secrets)){secret.file=path.join(temp,name);fs.writeFileSync(secret.file,crypto.randomBytes(32).toString('hex'),{mode:0o600});}
fs.writeFileSync(file,YAML.stringify(compose));
function docker(...args){const out=spawnSync('docker',['compose','-p',project,'-f',file,...args],{encoding:'utf8',timeout:180000,maxBuffer:1024*1024});assert.equal(out.status,0,'isolated Compose '+args[0]+' succeeded; credential output suppressed');return out.stdout.trim();}
const children=new Set(),sockets=new Set();let server,browser,cookie='',csrf='';
function request(port,url,method='GET',body,headers={}){return new Promise((resolve,reject)=>{const data=body===undefined?undefined:JSON.stringify(body),logical=port===29444?19444:19443;const requestHeaders={Host:`127.0.0.1:${logical}`,Origin:`https://127.0.0.1:${logical}`,...(data?{'Content-Type':'application/json','Content-Length':Buffer.byteLength(data)}:{}),...(cookie?{Cookie:cookie}:{}),...headers};if(requestHeaders.Cookie==='')delete requestHeaders.Cookie;const req=https.request({hostname:'127.0.0.1',port,path:'/api/admin/v1'+url,method,agent:false,rejectUnauthorized:false,timeout:30000,headers:requestHeaders},res=>{let raw='';res.on('data',part=>raw+=part);res.on('end',()=>{if(res.headers['set-cookie']&&!Object.hasOwn(headers,'Cookie'))cookie=res.headers['set-cookie'].map(v=>v.split(';')[0]).join('; ');let body;try{body=JSON.parse(raw);}catch{reject(Error('expected JSON fixture response'));return;}try{if(process.env.WR_OPERATIONS_CHECK==='1')adminResponse(method,url,res.statusCode,res.headers,body);}catch(error){reject(error);return;}resolve({status:res.statusCode,body,etag:res.headers.etag,headers:res.headers});});});req.on('error',()=>reject(Error('isolated fixture connection failed')));req.on('timeout',()=>req.destroy());req.end(data);});}
async function until(check){for(let i=0;i<40;i++){if(await check())return;await delay(500);}throw Error('isolated runtime did not apply setup');}
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
  const inspected=await setup('inspect');let measured;
  // Shared development hosts can change load between measurements. Exercise
  // the wizard's explicit remeasure action, retaining every result and the same
  // 250-500 ms target; never apply an unsuccessful calibration.
  for(let attempt=1;attempt<=3;attempt++){
    measured=await setup('calibrate');
    console.log('CALIBRATION: '+JSON.stringify({attempt,...measured.calibration}));
    if(measured.calibration.targetMet)break;
    if(attempt<3)await delay(2000);
  }
  assert.equal(measured.calibration.targetMet,true,'actual Control calibration must meet the unchanged target before apply');
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
  if(process.env.WR_SYNC_FAULTS==='1')await syncFaultChecks({docker,request,csrf});
  const before=(await request(29443,'/config/draft')).body;
  const runHeaders={'X-CSRF-Token':csrf,'Idempotency-Key':crypto.randomUUID()};
  const savedRuns=[];
  for(const preset of ['quick-20','smoke-1k']){
    const started=await request(29443,'/lab/runs','POST',{preset},{...runHeaders,'Idempotency-Key':crypto.randomUUID()});assert.equal(started.status,202,'start '+preset);
    let result;await until(async()=>{result=await request(29443,'/lab/runs/'+started.body.id);return result.status===200&&!['queued','running','cancelling'].includes(result.body.state);});
    assert.equal(result.body.state,'passed',JSON.stringify(result.body.report));assert.equal(result.body.report.joined,preset==='quick-20'?20:1000);assert.equal(result.body.report.admitted,3);assert.equal(result.body.report.unexpectedErrors,0);assert.equal(result.body.report.checks.length,7);savedRuns.push(result.body);
    console.log('PASS: '+preset+' via real Control/Coordinator mTLS, wr_traffic ACL runtime-key denial, isolated runtime-v4 HTTP fixture; requests='+result.body.report.requests);
  }
  assert.deepEqual((await request(29443,'/config/draft')).body,before);
  browser=await chromium.launch({headless:true});
  const context=await browser.newContext({ignoreHTTPSErrors:true,viewport:{width:1586,height:992}});
  await context.addCookies([{name:'__Host-wrs',value:cookie.split('; ').find(value=>value.startsWith('__Host-wrs=')).slice('__Host-wrs='.length),url:'https://127.0.0.1:29443',secure:true,httpOnly:true,sameSite:'Strict'}]);
  await context.addInitScript(value=>sessionStorage.setItem('wr.admin.csrf.v1',value),csrf);
  // The fixture publishes an alternate host port. Forward every request to the
  // real service with its fixed logical Host/Origin; never synthesize API data.
  await context.route('https://127.0.0.1:29443/**',async route=>{
    const headers={...route.request().headers(),host:'127.0.0.1:19443'};
    if(headers.origin==='https://127.0.0.1:29443')headers.origin='https://127.0.0.1:19443';
    try{const response=await route.fetch({headers});await route.fulfill({response});}catch{try{await route.abort();}catch{/* Fixture context closed. */}}
  });
  const page=await context.newPage(),errors=[];page.on('pageerror',()=>errors.push('pageerror'));
  page.on('console',message=>{if(['error','warning'].includes(message.type()))errors.push(message.type());});
  await page.goto('https://127.0.0.1:29443/rooms/setup_room/verification');
  await expect(page.getByRole('heading',{name:'Room 검증',exact:true})).toBeVisible();
  const lab=page.getByRole('region',{name:'Traffic Lab',exact:true});
  await expect(lab.getByRole('button',{name:'Quick 20 실행',exact:true})).toBeEnabled();
  await lab.getByRole('button',{name:'Quick 20 실행',exact:true}).click();
  await expect(lab.getByRole('heading',{name:'Quick 20 결과',exact:true})).toBeVisible();
  await expect(lab.locator('.tl-result .tl-status')).toHaveText('통과',{timeout:30000});
  assert.deepEqual(errors,[]);
  const screens=process.env.WR_TRAFFIC_SCREEN_DIR??fs.mkdtempSync(path.join(os.tmpdir(),'wr-traffic-qa-'));
  fs.mkdirSync(screens,{recursive:true});
  await lab.locator('.tl-result').screenshot({path:path.join(screens,'traffic-lab-room-result.png')});
  await context.unrouteAll({behavior:'wait'});await browser.close();browser=null;
  assert.deepEqual((await request(29443,'/config/draft')).body,before);
  console.log('PASS: real published Room verification tab starts Quick 20, renders its saved result, and preserves the Room draft');
  if(process.env.WR_TEST_KEYS==='1')for(const operation of ['stage','activate']){
    const beforeRotation=(await request(29443,'/config/delivery')).body.nodes.find(n=>n.id==='coordinator')?.rooms[0];
    assert.ok(beforeRotation?.recoveryFence>=1);
    const rotationStarted=Date.now();
    console.log('KEYS: '+operation+' and wait for both role ACKs');
    docker('stop','control','gateway','coordinator');docker('run','--rm','initialize','keys-'+operation);docker('up','-d','control','coordinator','gateway');
    await waitForRuntime(()=>docker('ps','--all','--format','json'),{timeout:240000,interval:2000});
    let keyStatus;
    try{await until(async()=>{keyStatus=JSON.parse(docker('run','--rm','initialize','keys-status'));return keyStatus.acknowledged===2;});}
    catch(error){console.log('Key ACK diagnostics: '+JSON.stringify({operation,phase:keyStatus?.phase,generation:keyStatus?.generation,acknowledged:keyStatus?.acknowledged}));throw error;}
    const afterRotation=(await request(29443,'/config/delivery')).body.nodes.find(n=>n.id==='coordinator')?.rooms[0];
    assert.equal(afterRotation?.recoveryFence,beforeRotation.recoveryFence,'normal drained role shutdown must not create a recovery fence');
    console.log('PASS: '+operation+' key generation '+keyStatus.generation+' acknowledged by both roles; elapsed '+(Date.now()-rotationStarted)+'ms; recovery fence unchanged');
  }
  if(process.env.WR_OPERATIONS_CHECK==='1')await operationsChecks({request,password,cookie,csrf,screens});
  const delivery=await request(29443,'/config/delivery');assert.equal(delivery.body.state,'applied');
  docker('restart','postgres','control');docker('up','-d','--wait','control');
  for(const run of savedRuns)assert.deepEqual((await request(29443,'/lab/runs/'+run.id)).body,run);
  const auditItems=[];let auditCursor='';do{const audit=await request(29443,'/audit-events'+(auditCursor?'?cursor='+encodeURIComponent(auditCursor):''));auditItems.push(...audit.body.items);auditCursor=audit.body.nextCursor;assert.ok(auditItems.length<1000,'bounded fixture audit history');}while(auditCursor);assert.equal(auditItems.filter(item=>item.action==='lab.result').length,process.env.WR_OPERATIONS_CHECK==='1'?4:3);
  for(const secret of [token,password])assert.equal(JSON.stringify(savedRuns).includes(secret),false);
  console.log('PASS: same saved reports after PostgreSQL/Control restart, one terminal audit per Lab run, published Room and draft preserved');
  console.log('Fixture: '+project+'; private state retained at '+temp);
}catch(error){try{for(const line of docker('logs','--no-color','--tail','200','gateway','coordinator').split('\n'))if(/runtime_sync node=(gateway|coordinator) state=(pending|recovered) /.test(line))console.log(line);}catch{/* Keep the original failure if diagnostic collection fails. */}throw error;}finally{if(browser)await browser.close();for(const socket of sockets)socket.destroy();for(const child of children)child.kill('SIGTERM');if(server)await new Promise(resolve=>server.close(resolve));docker('down');}
