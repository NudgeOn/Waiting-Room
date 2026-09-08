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
const project='waiting-room-beta-tiers-'+crypto.randomBytes(4).toString('hex'),temp=fs.mkdtempSync(path.join(os.tmpdir(),project+'-')),file=path.join(temp,'compose.yaml');
const compose=YAML.parse(fs.readFileSync('deploy/compose/local-beta.yaml','utf8'));
delete compose.name;
for(const service of Object.values(compose.services)){if(service.image==='waiting-room-local-control:dev')service.image='waiting-room-recovery-test:local';delete service.build;}
compose.services.control.ports=['127.0.0.1:29463:19443'];compose.services.gateway.ports=['127.0.0.1:30463:20443'];
for(const [name,secret] of Object.entries(compose.secrets)){secret.file=path.join(temp,name);fs.writeFileSync(secret.file,crypto.randomBytes(32).toString('hex'),{mode:0o600});}
fs.writeFileSync(file,YAML.stringify(compose));
function docker(...args){const out=spawnSync('docker',['compose','-p',project,'-f',file,...args],{encoding:'utf8',timeout:180000,maxBuffer:1024*1024});assert.equal(out.status,0,'isolated Compose '+args[0]+' succeeded; credential output suppressed');return out.stdout.trim();}
let dataAgent;const children=new Set(),sockets=new Set();let server,browser,cookie='',csrf='';
function request(port,url,method='GET',body,headers={}){return new Promise((resolve,reject)=>{const data=body===undefined?undefined:JSON.stringify(body),logical=port===29464?19444:19443;const requestHeaders={Host:`127.0.0.1:${logical}`,Origin:`https://127.0.0.1:${logical}`,...(data?{'Content-Type':'application/json','Content-Length':Buffer.byteLength(data)}:{}),...(cookie?{Cookie:cookie}:{}),...headers};if(requestHeaders.Cookie==='')delete requestHeaders.Cookie;const req=https.request({hostname:'127.0.0.1',port,path:'/api/admin/v1'+url,method,agent:false,rejectUnauthorized:false,timeout:30000,headers:requestHeaders},res=>{let raw='';res.on('data',part=>raw+=part);res.on('end',()=>{if(res.headers['set-cookie']&&!Object.hasOwn(headers,'Cookie'))cookie=res.headers['set-cookie'].map(v=>v.split(';')[0]).join('; ');let body;try{body=JSON.parse(raw);}catch{reject(Error('expected JSON fixture response'));return;}try{if(process.env.WR_OPERATIONS_CHECK==='1')adminResponse(method,url,res.statusCode,res.headers,body);}catch(error){reject(error);return;}resolve({status:res.statusCode,body,etag:res.headers.etag,headers:res.headers});});});req.on('error',()=>reject(Error('isolated fixture connection failed')));req.on('timeout',()=>req.destroy());req.end(data);});}
async function until(check){for(let i=0;i<40;i++){if(await check())return;await delay(500);}throw Error('isolated runtime did not apply setup');}
async function startApplications(){
  docker('up','-d','control','gateway','coordinator','demo-origin');
  await waitForRuntime(()=>docker('ps','--all','--format','json'),{timeout:240000,interval:2000});
}
try{
  docker('up','-d','--wait','postgres');docker('run','--rm','initialize','init','on');docker('up','-d','--wait','valkey');docker('run','--rm','queue-initialize');docker('up','-d','--wait','control','coordinator','gateway','demo-origin');docker('run','--rm','bootstrap');
  const token=docker('exec','-T','control','/wr-control','token');assert.equal(token.length,43);
  server=net.createServer(socket=>{sockets.add(socket);const child=spawn('docker',['compose','-p',project,'-f',file,'exec','-T','control','/wr-control','tunnel'],{stdio:['pipe','pipe','ignore']});children.add(child);socket.pipe(child.stdin);child.stdout.pipe(socket);socket.on('error',()=>{});child.stdin.on('error',()=>socket.destroy());child.on('error',()=>socket.destroy());child.on('exit',()=>{children.delete(child);socket.destroy();});socket.on('close',()=>{sockets.delete(socket);child.kill('SIGTERM');});});
  await new Promise((resolve,reject)=>{server.once('error',reject);server.listen(29464,'127.0.0.1',resolve);});
  const auth={'X-WR-Auth':'1','X-Bootstrap-Token':token};
  async function setup(step,body={}){const out=await request(29464,'/setup/'+step,'POST',body,auth);assert.equal(out.status,200,'setup '+step);return out.body;}
  const inspected=await setup('inspect'),measured=await setup('calibrate');assert.equal(measured.calibration.targetMet,true);
  const input={...inspected.input,regionId:'docker-traffic',limits:{maxActiveAdmissionLeases:7,admissionsPerMinute:23,admissionTtlSeconds:60},totp:{mode:'configurable',enabled:false}};
  const review=await setup('plan',input),applied=await setup('apply',{input,planDigest:review.planDigest,calibrationDigest:review.calibrationDigest});
  assert.equal(applied.environment.os,'linux');assert.equal(applied.plan.input.regionId,input.regionId);
  console.log('PASS: six-role Docker installation, actual Linux Control calibration, reviewed policy/region/defaults apply');
  docker('restart','postgres','control');docker('up','-d','--wait','control');assert.deepEqual((await setup('inspect')).report,applied);
  console.log('PASS: PostgreSQL and Control restart preserve exact setup report and calibrated parameters before bootstrap');
  const password=crypto.randomBytes(32).toString('base64url');const account=await request(29464,'/bootstrap','POST',{username:'traffic_docker',password},auth);assert.equal(account.status,200);assert.equal(account.body.state,'authenticated');csrf=account.body.csrfToken;
  const draft=await request(29463,'/config/draft');assert.equal(draft.status,200);assert.equal(draft.body.regionId,'docker-traffic');
  const room={...newRoom(),id:'setup_room',name:'Traffic isolation verification',hostname:'127.0.0.1',origin:'https://demo-origin:20445',healthURL:'https://demo-origin:20445/health',limits:applied.plan.input.limits,active:true};
  const saved=await request(29463,'/config/draft','PUT',{...draft.body,rooms:[room]},{'X-CSRF-Token':csrf,'If-Match':draft.etag,'Idempotency-Key':crypto.randomUUID()});assert.equal(saved.status,200);
  const published=await request(29463,'/config/publish','POST',{}, {'X-CSRF-Token':csrf,'If-Match':saved.etag,'Idempotency-Key':crypto.randomUUID()});assert.equal(published.status,202);
  await until(async()=>{const out=await request(29463,'/config/delivery');return out.status===200&&out.body.state==='applied'&&out.body.config.regionId===input.regionId&&out.body.nodes.length===2&&out.body.nodes.every(n=>n.generation===out.body.generation);});
  console.log('PASS: applied installation region and Room defaults reach signed runtime with both Gateway/Coordinator ACK');

  dataAgent=new https.Agent({keepAlive:true,maxSockets:32,maxFreeSockets:32,rejectUnauthorized:false});
  async function dataRequest(method,url,body,headers={}){return new Promise((resolve,reject)=>{const data=body===undefined?undefined:JSON.stringify(body);const req=https.request({hostname:'127.0.0.1',port:30463,path:url,method,agent:dataAgent,rejectUnauthorized:false,timeout:10000,headers:{Host:'127.0.0.1:20443',...(data?{'Content-Type':'application/json','Content-Length':Buffer.byteLength(data)}:{}),...headers}},res=>{let raw='';res.on('data',chunk=>raw+=chunk);res.on('end',()=>{let body;try{body=JSON.parse(raw);}catch{body=null;}resolve({status:res.statusCode,body,raw});});});req.on('error',()=>reject(Error('isolated tier transport failed')));req.on('timeout',()=>req.destroy());req.end(data);});}
  async function parallel(from,to,fn){let next=from;await Promise.all(Array.from({length:32},async()=>{for(;;){const i=next++;if(i>=to)return;await fn(i);}}));}
  const tickets=[],keys=[],failures=[];let previous=0;
  for(const size of [1000,2000,5000,10000]){
    const started=performance.now(),latency=[];
    await parallel(previous,size,async i=>{keys[i]=crypto.randomUUID();const start=performance.now();const out=await dataRequest('POST','/_wr/v1/tickets',{target:'/shop/cart'},{'Idempotency-Key':keys[i]});latency.push(performance.now()-start);if(out.status!==202||!out.body?.ticketToken){failures.push({phase:'join',status:out.status,code:out.body?.code});return;}tickets[i]=out.body;});
    assert.deepEqual(failures,[],'real v5 joins without unexpected 503');
    await parallel(0,size,async i=>{const out=await dataRequest('GET',tickets[i].statusUrl,undefined,{Authorization:'Bearer '+tickets[i].ticketToken});if(out.status!==202||out.body?.state!=='queued')failures.push({phase:'status',status:out.status,code:out.body?.code});});assert.deepEqual(failures,[],'real v5 read-only status');
    for(let i=0;i<size;i+=Math.max(1,Math.floor(size/100))){const out=await dataRequest('POST','/_wr/v1/tickets',{target:'/shop/cart'},{'Idempotency-Key':keys[i]});assert.equal(out.status,202);assert.deepEqual(out.body,tickets[i]);}
    await until(async()=>{const d=(await request(29463,'/config/delivery')).body;return d.state==='applied'&&d.nodes.find(n=>n.id==='coordinator')?.rooms[0]?.waiting===size;});
    latency.sort((a,b)=>a-b);console.log('PASS: current v5 real Docker HTTPS '+size+' visitors, '+size+' status reads, 100 exact retries; join p99='+latency[Math.floor(latency.length*.99)].toFixed(1)+'ms; elapsed='+((performance.now()-started)/1000).toFixed(1)+'s');previous=size;
  }
  const excess=await dataRequest('POST','/_wr/v1/tickets',{target:'/shop/cart'},{'Idempotency-Key':crypto.randomUUID()});assert.equal(excess.status,503);assert.equal(excess.body.code,'QUEUE_CAPACITY_EXCEEDED');
  assert.equal((await dataRequest('GET','/shop')).status,429);assert.equal((await dataRequest('POST','/shop',{unsafe:true})).status,429);
  const rt=await request(29463,'/rooms/setup_room/runtime');const auto=await request(29463,'/rooms/setup_room/runtime','PATCH',{action:'auto'},{'X-CSRF-Token':csrf,'If-Match':rt.etag,'Idempotency-Key':crypto.randomUUID()});assert.equal(auto.status,200);
  await until(async()=>{const d=(await request(29463,'/config/delivery')).body;return d.state==='applied'&&d.nodes.find(n=>n.id==='coordinator')?.rooms[0]?.ready===7;});
  let ready=0;await parallel(0,10000,async i=>{const out=await dataRequest('GET',tickets[i].statusUrl,undefined,{Authorization:'Bearer '+tickets[i].ticketToken});if(out.body?.state==='ready'){ready++;const claim=await dataRequest('POST','/_wr/v1/rooms/'+room.publicId+'/admissions',undefined,{Authorization:'Bearer '+tickets[i].ticketToken});assert.equal(claim.status,200);const origin=await dataRequest('GET','/shop/cart',undefined,{'X-Waiting-Room-Admission':claim.body.admissionToken});assert.equal(origin.status,200);}});assert.equal(ready,7);
  console.log('PASS: 10K physical queue retains exact cap rejection, protected GET/POST stay blocked, AUTO grants exactly seven configured leases and those claims reach mTLS origin');
  console.log('Fixture: '+project+'; local population/boundary validation only; 10K sustained qualification NOT_RUN');
}finally{dataAgent?.destroy();if(browser)await browser.close();for(const socket of sockets)socket.destroy();for(const child of children)child.kill('SIGTERM');if(server)await new Promise(resolve=>server.close(resolve));docker('down');}
