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
import {populationRecovery} from './population-recovery.mjs';
import {valkeyObserver} from './valkey-observer.mjs';

assert.equal(process.env.WR_TEST_TRAFFIC_DOCKER,'local');
const guarded=process.env.WR_TEST_PUBLIC_POPULATION==='1';
if(process.env.WR_TEST_COLD_POPULATION==='1')assert.equal(guarded,true,'cold population recovery requires the current guarded candidate');
const candidateImage=process.env.WR_TEST_CANDIDATE_IMAGE||'waiting-room-recovery-test:local';
assert.match(candidateImage,/^waiting-room-[a-z0-9]+(?:[._-][a-z0-9]+)*:local$/);
if(guarded)assert.ok(process.env.WR_TEST_CANDIDATE_IMAGE,'public population must select its exact candidate');
const project='waiting-room-beta-tiers-'+crypto.randomBytes(4).toString('hex'),temp=fs.mkdtempSync(path.join(os.tmpdir(),project+'-')),file=path.join(temp,'compose.yaml');
const observer=process.env.WR_TEST_VALKEY_DIAGNOSTICS==='1'?valkeyObserver(project,candidateImage):null;
const compose=YAML.parse(fs.readFileSync('deploy/compose/local-beta.yaml','utf8'));
if(observer)compose.services.valkey.command.push('--latency-monitor-threshold','100');
delete compose.name;
for(const service of Object.values(compose.services)){if(service.image==='waiting-room-local-control:dev')service.image=candidateImage;delete service.build;}
compose.services.control.ports=['127.0.0.1:29463:19443'];compose.services.gateway.ports=['127.0.0.1:30463:20443'];
for(const [name,secret] of Object.entries(compose.secrets)){secret.file=path.join(temp,name);fs.writeFileSync(secret.file,crypto.randomBytes(32).toString('hex'),{mode:0o600});}
fs.writeFileSync(file,YAML.stringify(compose));
function docker(...args){
  const out=spawnSync('docker',['compose','-p',project,'-f',file,...args],{encoding:'utf8',timeout:180000,maxBuffer:1024*1024});
  if(out.status!==0){
    // Preserve a useful failure category without printing raw Compose output,
    // environment values, mounted credentials or bootstrap responses.
    const stderr=out.stderr??'';
    const reason=/all predefined address pools have been fully subnetted/i.test(stderr)?'address_pool_exhausted'
      :/port is already allocated|address already in use/i.test(stderr)?'port_unavailable'
      :/mounts denied|bind source path does not exist/i.test(stderr)?'bind_mount_unavailable'
      :/unhealthy/i.test(stderr)?'service_unhealthy':'unclassified';
    console.error('Compose startup diagnostic: '+JSON.stringify({operation:args[0],exit:out.status,signal:out.signal??null,timeout:out.error?.code==='ETIMEDOUT',reason}));
  }
  assert.equal(out.status,0,'isolated Compose '+args[0]+' succeeded; credential output suppressed');return out.stdout.trim();
}
let dataAgent;const children=new Set(),sockets=new Set();let server,browser,cookie='',csrf='';
const heartbeatStop=new AbortController();let heartbeatTask,heartbeatError,throttled=0,heartbeats=0;
function request(port,url,method='GET',body,headers={}){return new Promise((resolve,reject)=>{const data=body===undefined?undefined:JSON.stringify(body),logical=port===29464?19444:19443;const requestHeaders={Host:`127.0.0.1:${logical}`,Origin:`https://127.0.0.1:${logical}`,...(data?{'Content-Type':'application/json','Content-Length':Buffer.byteLength(data)}:{}),...(cookie?{Cookie:cookie}:{}),...headers};if(requestHeaders.Cookie==='')delete requestHeaders.Cookie;const req=https.request({hostname:'127.0.0.1',port,path:'/api/admin/v1'+url,method,agent:false,rejectUnauthorized:false,timeout:30000,headers:requestHeaders},res=>{let raw='';res.on('data',part=>raw+=part);res.on('end',()=>{if(res.headers['set-cookie']&&!Object.hasOwn(headers,'Cookie'))cookie=res.headers['set-cookie'].map(v=>v.split(';')[0]).join('; ');let body;try{body=JSON.parse(raw);}catch{reject(Error('expected JSON fixture response'));return;}try{if(process.env.WR_OPERATIONS_CHECK==='1')adminResponse(method,url,res.statusCode,res.headers,body);}catch(error){reject(error);return;}resolve({status:res.statusCode,body,etag:res.headers.etag,headers:res.headers});});});req.on('error',()=>reject(Error('isolated fixture connection failed')));req.on('timeout',()=>req.destroy());req.end(data);});}
async function until(check){for(let i=0;i<40;i++){if(await check())return;await delay(500);}throw Error('isolated runtime did not apply setup');}
async function startApplications(){
  docker('up','-d','control','gateway','coordinator','demo-origin');
  await waitForRuntime(()=>docker('ps','--all','--format','json'),{timeout:240000,interval:2000});
}
try{
  docker('up','-d','--wait','postgres');docker('run','--rm','initialize','init','on');
  if(observer)console.log(docker('run','--rm','--entrypoint','/probe','--volume',path.resolve('build/beta8-valkey-observer/probe')+':/probe:ro','initialize','provision'));
  docker('up','-d','--wait','valkey');docker('run','--rm','queue-initialize');docker('up','-d','--wait','control','coordinator','gateway','demo-origin');docker('run','--rm','bootstrap');
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
  await observer?.start();

  dataAgent=new https.Agent({keepAlive:true,maxSockets:32,maxFreeSockets:32,rejectUnauthorized:false});
  async function rawDataRequest(method,url,body,headers={}) {
    return new Promise((resolve,reject)=>{
      const started=performance.now();let socketAt,timeout=false;
      const phase=url==='/_wr/v1/tickets'?'join':url.includes('/status')?'status':url.endsWith('/heartbeat')?'heartbeat':url.endsWith('/admissions')?'claim':'origin';
      const data=body===undefined?undefined:JSON.stringify(body);
      const req=https.request({hostname:'127.0.0.1',port:30463,path:url,method,agent:dataAgent,rejectUnauthorized:false,timeout:10000,headers:{Host:'127.0.0.1:20443',...(data?{'Content-Type':'application/json','Content-Length':Buffer.byteLength(data)}:{}),...headers}},res=>{
        let raw='';res.on('data',chunk=>raw+=chunk);
        res.on('aborted',()=>failed('response_aborted'));
        res.on('error',error=>failed(error.code));
        res.on('end',()=>{let body;try{body=JSON.parse(raw);}catch{body=null;}resolve({status:res.statusCode,body,raw,headers:res.headers});});
      });
      function failed(code) {
        // Whitelisted transport facts only. Never include URLs, headers,
        // response bodies, bearer tokens or the raw underlying error message.
        const error=new Error('isolated tier transport failed');
        error.diagnostic={phase,method,observedAt:new Date().toISOString(),code:['ECONNRESET','ECONNREFUSED','ETIMEDOUT','EPIPE','response_aborted'].includes(code)?code:'transport_unavailable',timeout,reusedSocket:Boolean(req.reusedSocket),socketAssigned:socketAt!==undefined,queueWaitMs:socketAt===undefined?null:Math.round(socketAt-started),elapsedMs:Math.round(performance.now()-started)};
        reject(error);
      }
      req.once('socket',()=>{socketAt=performance.now();});
      req.on('error',error=>failed(error.code));
      req.on('timeout',()=>{timeout=true;req.destroy();});
      req.end(data);
    });
  }
  // Public-mode retries obey the real response deadline. Only explicit 429 is
  // retryable here; any 503 or transport loss remains an observable test failure.
  async function dataRequest(method,url,body,headers={},signal){
    for(let attempt=0;attempt<12;attempt++){
      if(signal?.aborted)throw signal.reason;
      const out=await rawDataRequest(method,url,body,headers);
      if(!guarded||out.status!==429)return out;
      assert.equal(out.body?.code,'API_RATE_LIMITED');
      const seconds=Number(out.headers['retry-after']);
      assert.ok(Number.isInteger(seconds)&&seconds>=1&&seconds<=60,'bounded Retry-After');
      throttled++;
      if(throttled===1||throttled%100===0)console.log('PUBLIC QUOTA: obeyed 429 responses='+throttled+'; successful heartbeats='+heartbeats);
      await delay(seconds*1000+25,undefined,{signal});
    }
    throw Error('public quota retry budget exhausted');
  }
  async function parallel(from,to,fn,workers=32){let next=from;await Promise.all(Array.from({length:workers},async()=>{for(;;){const i=next++;if(i>=to)return;await fn(i);}}));}
  const tickets=[],keys=[],failures=[];let previous=0;
  if(guarded)heartbeatTask=(async()=>{
    for(;;){
      await delay(120000,undefined,{signal:heartbeatStop.signal});
      const retained=tickets.filter(Boolean);
      await parallel(0,retained.length,async i=>{
        const out=await dataRequest('POST','/_wr/v1/rooms/'+room.publicId+'/heartbeat',undefined,{Authorization:'Bearer '+retained[i].ticketToken},heartbeatStop.signal);
        if(out.status!==204) {const error=new Error('live visitor heartbeat did not retain its existing ticket');error.diagnostic={phase:'heartbeat',status:out.status,code:out.body?.code,observedAt:new Date().toISOString()};throw error;}heartbeats++;
      });
      console.log('PASS: real HTTP heartbeat cycle retained '+retained.length+' visitors; total='+heartbeats);
    }
  })().catch(error=>{if(!heartbeatStop.signal.aborted){heartbeatError=error;console.log('Heartbeat failed: '+JSON.stringify({observedAt:new Date().toISOString(),diagnostic:error.diagnostic??null}));}});

  for(const size of [1000,2000,5000,10000]){
    const started=performance.now(),latency=[];
    async function join(i){keys[i]=crypto.randomUUID();const start=performance.now();const out=await dataRequest('POST','/_wr/v1/tickets',{target:'/shop/cart'},{'Idempotency-Key':keys[i]});latency.push(performance.now()-start);if(out.status!==202||!out.body?.ticketToken){if(failures.length<20)failures.push({phase:'join',index:i,status:out.status,code:out.body?.code,observedAt:new Date().toISOString()});return;}tickets[i]=out.body;}
    // Seven sequential original visitors let the final public admission assertion
    // establish FIFO without guessing the arrival order of concurrent sockets.
    let from=previous;
    if(guarded&&previous===0){for(let i=0;i<7;i++)await join(i);from=7;}
    await parallel(from,size,join);
    assert.deepEqual(failures,[],'real v5 joins without unexpected 503');
    // Waiting poll timers may overlap; the shared Agent still caps real TLS sockets at 32.
    await parallel(0,size,async i=>{const out=await dataRequest('GET',tickets[i].statusUrl,undefined,{Authorization:'Bearer '+tickets[i].ticketToken});if((out.status!==202||out.body?.state!=='queued')&&failures.length<20)failures.push({phase:'status',index:i,status:out.status,code:out.body?.code,observedAt:new Date().toISOString()});},guarded?1024:32);assert.deepEqual(failures,[],'real v5 read-only status');
    for(let i=guarded?size-100:0;i<size;i+=guarded?1:Math.max(1,Math.floor(size/100))){const out=await dataRequest('POST','/_wr/v1/tickets',{target:'/shop/cart'},{'Idempotency-Key':keys[i]});assert.equal(out.status,202);assert.ok(JSON.stringify(out.body)===JSON.stringify(tickets[i]),'exact retained join response; credential output suppressed');}
    await until(async()=>{const d=(await request(29463,'/config/delivery')).body;return d.state==='applied'&&d.nodes.find(n=>n.id==='coordinator')?.rooms[0]?.waiting===size;});
    if(heartbeatError)throw heartbeatError;
    latency.sort((a,b)=>a-b);console.log('PASS: '+(guarded?'guarded candidate':'legacy v5')+' real Docker HTTPS '+size+' visitors, '+size+' status reads, 100 '+(guarded?'recent ':'')+'exact retries; '+(guarded?'client wall p99 including quota waits=':'join p99=')+latency[Math.floor(latency.length*.99)].toFixed(1)+'ms; elapsed='+((performance.now()-started)/1000).toFixed(1)+'s');previous=size;
  }
  const excess=await dataRequest('POST','/_wr/v1/tickets',{target:'/shop/cart'},{'Idempotency-Key':crypto.randomUUID()});assert.equal(excess.status,503);assert.equal(excess.body.code,'QUEUE_CAPACITY_EXCEEDED');
  assert.equal((await rawDataRequest('GET','/shop')).status,429);assert.equal((await rawDataRequest('POST','/shop',{unsafe:true})).status,429);
  heartbeatStop.abort();await heartbeatTask;if(heartbeatError)throw heartbeatError;
  if(process.env.WR_TEST_COLD_POPULATION==='1')await populationRecovery({docker,request,dataRequest,startApplications,tickets,keys});
  const rt=await request(29463,'/rooms/setup_room/runtime');const auto=await request(29463,'/rooms/setup_room/runtime','PATCH',{action:'auto'},{'X-CSRF-Token':csrf,'If-Match':rt.etag,'Idempotency-Key':crypto.randomUUID()});assert.equal(auto.status,200);
  await until(async()=>{const d=(await request(29463,'/config/delivery')).body;return d.state==='applied'&&d.nodes.find(n=>n.id==='coordinator')?.rooms[0]?.ready===7;});
  if(process.env.WR_TEST_WAIT_PROGRESS==='1'){
    const progress=await dataRequest('GET',tickets[7].statusUrl,undefined,{Authorization:'Bearer '+tickets[7].ticketToken});
    assert.equal(progress.status,202);
    assert.equal(progress.body.state,'queued');
    assert.ok(Number.isInteger(progress.body.usersAhead)&&progress.body.usersAhead>=0&&progress.body.usersAhead<9993);
    assert.equal(progress.body.admissionPaused,false);
    const estimate=progress.body.estimatedWaitSeconds;
    assert.ok(Number.isInteger(estimate?.min)&&estimate.min>=1);
    assert.ok(Number.isInteger(estimate?.max)&&estimate.max>=estimate.min);
    console.log('PASS: real 10K HTTPS waiting status reports approximate position and a positive time range after AUTO promotions');
  }
  let ready=0;await parallel(0,guarded?7:10000,async i=>{const out=await dataRequest('GET',tickets[i].statusUrl,undefined,{Authorization:'Bearer '+tickets[i].ticketToken});if(out.body?.state==='ready'){ready++;const claim=await dataRequest('POST','/_wr/v1/rooms/'+room.publicId+'/admissions',undefined,{Authorization:'Bearer '+tickets[i].ticketToken});assert.equal(claim.status,200);const origin=await dataRequest('GET','/shop/cart',undefined,{'X-Waiting-Room-Admission':claim.body.admissionToken});assert.equal(origin.status,200);}});assert.equal(ready,7);
  console.log('PASS: 10K physical queue retains exact cap rejection, protected GET/POST stay blocked, AUTO grants exactly seven configured leases and those claims reach mTLS origin');
  if(heartbeatError)throw heartbeatError;
  console.log('Fixture: '+project+'; image='+candidateImage+'; guarded='+guarded+'; observed 429='+throttled+'; successful heartbeats='+heartbeats+'; local population/boundary validation only; 10K sustained qualification NOT_RUN');
}catch(error){
  console.log('Failed fixture: '+project+'; image='+candidateImage+'; private state retained at '+temp);
  if(error.diagnostic)console.log('First client failure: '+JSON.stringify(error.diagnostic));
  else console.log('Failure kind: '+(error.code==='ERR_ASSERTION'?'assertion':'fixture_or_transport'));
  console.log('Population failure: explicit 429='+throttled+'; successful heartbeats='+heartbeats);
  try{
    const delivery=(await request(29463,'/config/delivery')).body;
    console.log('Population delivery: '+JSON.stringify({state:delivery.state,generation:delivery.generation,nodes:delivery.nodes?.map(n=>({id:n.id,generation:n.generation,rooms:n.rooms?.map(r=>({epoch:r.epoch,mode:r.mode,waiting:r.waiting,ready:r.ready,activeAdmissionLeases:r.activeAdmissionLeases,recoveryFence:r.recoveryFence,recoveryReason:r.recoveryReason,recoveryUntil:r.recoveryUntil}))}))}));
    for(const line of docker('logs','--no-color','--tail','200','gateway','coordinator').split('\n'))if(/runtime_sync node=(gateway|coordinator) state=(pending|recovered) |public_guard state=unavailable code=|queue_call state=uncertain operation=/.test(line))console.log(line);
  }catch{/* Preserve the original test failure. */}
  throw error;
}finally{heartbeatStop.abort();await heartbeatTask;dataAgent?.destroy();if(browser)await browser.close();for(const socket of sockets)socket.destroy();for(const child of children)child.kill('SIGTERM');if(server)await new Promise(resolve=>server.close(resolve));try{observer?.finish();}finally{docker('down');}}
