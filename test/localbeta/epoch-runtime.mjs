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
const project='waiting-room-epoch-test-'+crypto.randomBytes(4).toString('hex'),temp=fs.mkdtempSync(path.join(os.tmpdir(),project+'-')),file=path.join(temp,'compose.yaml');
const compose=YAML.parse(fs.readFileSync('deploy/compose/local-beta.yaml','utf8'));
delete compose.name;
for(const service of Object.values(compose.services)){if(service.image==='waiting-room-local-control:dev')service.image=process.env.WR_TEST_CANDIDATE_IMAGE||'waiting-room-recovery-test:local';delete service.build;}
compose.services.control.ports=['127.0.0.1:29453:19443'];compose.services.gateway.ports=['127.0.0.1:30453:20443'];
for(const [name,secret] of Object.entries(compose.secrets)){secret.file=path.join(temp,name);fs.writeFileSync(secret.file,crypto.randomBytes(32).toString('hex'),{mode:0o600});}
fs.writeFileSync(file,YAML.stringify(compose));
function docker(...args){const out=spawnSync('docker',['compose','-p',project,'-f',file,...args],{encoding:'utf8',timeout:180000,maxBuffer:1024*1024});assert.equal(out.status,0,'isolated Compose '+args[0]+' succeeded; credential output suppressed');return out.stdout.trim();}
const children=new Set(),sockets=new Set();let server,browser,cookie='',csrf='';
function request(port,url,method='GET',body,headers={}){return new Promise((resolve,reject)=>{const data=body===undefined?undefined:JSON.stringify(body),logical=port===29454?19444:19443;const requestHeaders={Host:`127.0.0.1:${logical}`,Origin:`https://127.0.0.1:${logical}`,...(data?{'Content-Type':'application/json','Content-Length':Buffer.byteLength(data)}:{}),...(cookie?{Cookie:cookie}:{}),...headers};if(requestHeaders.Cookie==='')delete requestHeaders.Cookie;const req=https.request({hostname:'127.0.0.1',port,path:'/api/admin/v1'+url,method,agent:false,rejectUnauthorized:false,timeout:30000,headers:requestHeaders},res=>{let raw='';res.on('data',part=>raw+=part);res.on('end',()=>{if(res.headers['set-cookie']&&!Object.hasOwn(headers,'Cookie'))cookie=res.headers['set-cookie'].map(v=>v.split(';')[0]).join('; ');let body;try{body=JSON.parse(raw);}catch{reject(Error('expected JSON fixture response'));return;}try{if(process.env.WR_OPERATIONS_CHECK==='1')adminResponse(method,url,res.statusCode,res.headers,body);}catch(error){reject(error);return;}resolve({status:res.statusCode,body,etag:res.headers.etag,headers:res.headers});});});req.on('error',()=>reject(Error('isolated fixture connection failed')));req.on('timeout',()=>req.destroy());req.end(data);});}
async function until(check){for(let i=0;i<40;i++){if(await check())return;await delay(500);}throw Error('isolated runtime did not apply setup');}
async function startApplications(){
  docker('up','-d','control','gateway','coordinator','demo-origin');
  await waitForRuntime(()=>docker('ps','--all','--format','json'),{timeout:240000,interval:2000});
}
try{
  docker('up','-d','--wait','postgres');docker('run','--rm','initialize','init','on');docker('up','-d','--wait','valkey');docker('run','--rm','queue-initialize');docker('up','-d','--wait','control','coordinator','gateway','demo-origin');docker('run','--rm','bootstrap');
  const token=docker('exec','-T','control','/wr-control','token');assert.equal(token.length,43);
  server=net.createServer(socket=>{sockets.add(socket);const child=spawn('docker',['compose','-p',project,'-f',file,'exec','-T','control','/wr-control','tunnel'],{stdio:['pipe','pipe','ignore']});children.add(child);socket.pipe(child.stdin);child.stdout.pipe(socket);socket.on('error',()=>{});child.stdin.on('error',()=>socket.destroy());child.on('error',()=>socket.destroy());child.on('exit',()=>{children.delete(child);socket.destroy();});socket.on('close',()=>{sockets.delete(socket);child.kill('SIGTERM');});});
  await new Promise((resolve,reject)=>{server.once('error',reject);server.listen(29454,'127.0.0.1',resolve);});
  const auth={'X-WR-Auth':'1','X-Bootstrap-Token':token};
  async function setup(step,body={}){const out=await request(29454,'/setup/'+step,'POST',body,auth);assert.equal(out.status,200,'setup '+step);return out.body;}
  const inspected=await setup('inspect'),measured=await setup('calibrate');assert.equal(measured.calibration.targetMet,true);
  const input={...inspected.input,regionId:'docker-traffic',limits:{maxActiveAdmissionLeases:7,admissionsPerMinute:23,admissionTtlSeconds:60},totp:{mode:'configurable',enabled:false}};
  const review=await setup('plan',input),applied=await setup('apply',{input,planDigest:review.planDigest,calibrationDigest:review.calibrationDigest});
  assert.equal(applied.environment.os,'linux');assert.equal(applied.plan.input.regionId,input.regionId);
  console.log('PASS: six-role Docker installation, actual Linux Control calibration, reviewed policy/region/defaults apply');
  docker('restart','postgres','control');docker('up','-d','--wait','control');assert.deepEqual((await setup('inspect')).report,applied);
  console.log('PASS: PostgreSQL and Control restart preserve exact setup report and calibrated parameters before bootstrap');
  const password=crypto.randomBytes(32).toString('base64url');const account=await request(29454,'/bootstrap','POST',{username:'traffic_docker',password},auth);assert.equal(account.status,200);assert.equal(account.body.state,'authenticated');csrf=account.body.csrfToken;
  const draft=await request(29453,'/config/draft');assert.equal(draft.status,200);assert.equal(draft.body.regionId,'docker-traffic');
  const room={...newRoom(),id:'setup_room',name:'Traffic isolation verification',hostname:'127.0.0.1',origin:'https://demo-origin:20445',healthURL:'https://demo-origin:20445/health',limits:applied.plan.input.limits,active:true};
  const saved=await request(29453,'/config/draft','PUT',{...draft.body,rooms:[room]},{'X-CSRF-Token':csrf,'If-Match':draft.etag,'Idempotency-Key':crypto.randomUUID()});assert.equal(saved.status,200);
  const published=await request(29453,'/config/publish','POST',{}, {'X-CSRF-Token':csrf,'If-Match':saved.etag,'Idempotency-Key':crypto.randomUUID()});assert.equal(published.status,202);
  await until(async()=>{const out=await request(29453,'/config/delivery');return out.status===200&&out.body.state==='applied'&&out.body.config.regionId===input.regionId&&out.body.nodes.length===2&&out.body.nodes.every(n=>n.generation===out.body.generation);});
  console.log('PASS: applied installation region and Room defaults reach signed runtime with both Gateway/Coordinator ACK');

  const app=await chromium.launch({headless:true});browser=app;
  const visitor=await app.newContext({ignoreHTTPSErrors:true});
  async function dataRequest(method,url,body,headers={}){return new Promise((resolve,reject)=>{const data=body===undefined?undefined:JSON.stringify(body);const req=https.request({hostname:'127.0.0.1',port:30453,path:url,method,agent:false,rejectUnauthorized:false,timeout:10000,headers:{Host:'127.0.0.1:20443',...(data?{'Content-Type':'application/json','Content-Length':Buffer.byteLength(data)}:{}),...headers}},res=>{let raw='';res.on('data',chunk=>raw+=chunk);res.on('end',()=>resolve({status:()=>res.statusCode,json:async()=>JSON.parse(raw)}));});req.on('error',()=>reject(Error('fresh data-plane connection failed')));req.on('timeout',()=>req.destroy());req.end(data);});}
  const key=crypto.randomUUID(),join=await dataRequest('POST','/_wr/v1/tickets',{target:'/shop/cart'},{'Idempotency-Key':key});assert.equal(join.status(),202);const old=await join.json();
  const cycles=Number(process.env.WR_EPOCH_STRESS||1);assert.ok(Number.isInteger(cycles)&&cycles>=1&&cycles<=100);
  let untilTime;const ackTimes=[];
  for(let cycle=1;cycle<=cycles;cycle++){
    // Reauthentication shares the real five-attempts/minute account budget.
    // Keep the safety limit intact instead of clearing its DB bucket.
    if(cycle>1)await delay(13000);
    if(cycles>1)for(let command=0;command<40;command++){
      const current=await request(29453,'/rooms/setup_room/runtime');
      const out=await request(29453,'/rooms/setup_room/runtime','PATCH',{action:'hold'},{'X-CSRF-Token':csrf,'If-Match':current.etag,'Idempotency-Key':crypto.randomUUID()});assert.equal(out.status,200);
    }
  const rt=(await request(29453,'/rooms/setup_room/runtime'));const body={action:'new-epoch',scope:'installation',generation:(await request(29453,'/config/delivery')).body.generation};
  const requestDigest=crypto.createHash('sha256').update('wr-action/v1\nPATCH\nsetup_room\n'+rt.etag+'\n'+JSON.stringify(body)).digest('hex');
  const proof=await request(29453,'/auth/reauth','POST',{password,action:'runtime.new_epoch',targetId:'setup_room',requestDigest},{'X-CSRF-Token':csrf});assert.equal(proof.status,200);
  const headers={'X-CSRF-Token':csrf,'X-Reauth-Token':proof.body.reauthToken,'If-Match':rt.etag,'Idempotency-Key':crypto.randomUUID()};
  const changed=await request(29453,'/rooms/setup_room/runtime','PATCH',body,headers);assert.equal(changed.status,200);assert.equal(changed.body.epoch,cycle+1);
  const replay=await request(29453,'/rooms/setup_room/runtime','PATCH',body,headers);assert.equal(replay.status,200);assert.deepEqual(replay.body,changed.body);assert.equal(replay.headers['idempotency-replayed'],'true');
  const began=Date.now();
  await until(async()=>{const d=(await request(29453,'/config/delivery')).body;const m=d.nodes.find(n=>n.id==='coordinator')?.rooms[0];untilTime=m?.recoveryUntil;return d.state==='applied'&&m?.epoch===cycle+1&&m.mode==='RECOVERY_HOLD'&&m.recoveryReason==='epoch_reset';});
  ackTimes.push(Date.now()-began);assert.ok(untilTime-Date.now()>3620000);console.log('PASS: Admin reauth/new epoch exact retry, both mTLS ACK epoch '+(cycle+1)+', latency '+ackTimes.at(-1)+'ms, actual safety deadline '+new Date(untilTime).toISOString());
  }
  console.log('PASS: '+cycles+' epoch cycles; maximum ACK latency '+Math.max(...ackTimes)+'ms');
  assert.notEqual((await dataRequest('GET',old.statusUrl,undefined,{Authorization:'Bearer '+old.ticketToken})).status(),202);
  let observations=0;
  while(Date.now()<untilTime+1500){
    const response=await dataRequest('POST','/_wr/v1/tickets',{target:'/shop'},{'Idempotency-Key':crypto.randomUUID()});
    if(Date.now()<untilTime){
      assert.equal(response.status(),503,'admission stayed closed throughout real safety window');
      assert.equal((await response.json()).code,'QUEUE_UNAVAILABLE','safety hold must not conceal an unrelated configuration failure');
    }
    observations++;
    await delay(Math.min(30000,Math.max(1000,untilTime-Date.now()+1500)));
  }
  // Public probes do not extend the administrator's 30-minute idle session.
  // Authenticate again after the real one-hour wait; never weaken session TTLs.
  const login=await request(29453,'/auth/login','POST',{username:'traffic_docker',password},{Cookie:'','X-WR-Auth':'1'});assert.equal(login.status,200,'fresh admin login after safety wait');
  cookie=login.headers['set-cookie'].map(v=>v.split(';')[0]).join('; ');csrf=login.body.csrfToken;
  await until(async()=>{const out=await request(29453,'/config/delivery');assert.equal(out.status,200,'authenticated post-wait delivery read');const d=out.body;return d.state==='applied'&&d.nodes.find(n=>n.id==='coordinator')?.rooms[0]?.mode==='HOLD';});
  const queued=await dataRequest('POST','/_wr/v1/tickets',{target:'/shop'},{'Idempotency-Key':crypto.randomUUID()});assert.equal(queued.status(),202);const ticket=await queued.json();
  const current=await request(29453,'/rooms/setup_room/runtime');const auto=await request(29453,'/rooms/setup_room/runtime','PATCH',{action:'auto'},{'X-CSRF-Token':csrf,'If-Match':current.etag,'Idempotency-Key':crypto.randomUUID()});assert.equal(auto.status,200);
  await until(async()=>{const out=await dataRequest('GET',ticket.statusUrl,undefined,{Authorization:'Bearer '+ticket.ticketToken});return [200,202].includes(out.status())&&(await out.json()).state==='ready';});
  const claimed=await dataRequest('POST','/_wr/v1/rooms/'+room.publicId+'/admissions',undefined,{Authorization:'Bearer '+ticket.ticketToken});assert.equal(claimed.status(),200);const admission=await claimed.json();
  const accepted=await dataRequest('GET','/shop',undefined,{'X-Waiting-Room-Admission':admission.admissionToken});assert.equal(accepted.status(),200);
  console.log('PASS: '+observations+' real-clock safety observations, bounded validation returns HOLD, explicit AUTO then current-epoch claim reaches actual mTLS origin');
  console.log('Fixture: '+project+'; private state retained at '+temp);
}catch(error){try{for(const line of docker('logs','--no-color','--tail','200','gateway','coordinator').split('\n'))if(/runtime_sync node=(gateway|coordinator) state=(pending|recovered) |public_guard state=unavailable code=/.test(line))console.log(line);}catch{/* Preserve original failure. */}throw error;}finally{if(browser)await browser.close();for(const socket of sockets)socket.destroy();for(const child of children)child.kill('SIGTERM');if(server)await new Promise(resolve=>server.close(resolve));docker('down');}
