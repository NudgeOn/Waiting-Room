// SPDX-License-Identifier: Apache-2.0
// Dedicated release-runner fixture only. No saved user installation is selected.
import assert from 'node:assert/strict';
import crypto from 'node:crypto';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import https from 'node:https';
import {spawn,spawnSync} from 'node:child_process';
import {newRoom} from '../../apps/admin/src/control-api.js';

const image=process.env.WR_RELEASE_IMAGE;
assert.match(image??'',/^ghcr\.io\/nudgeon\/waiting-room@sha256:[a-f0-9]{64}$/);
assert.equal(process.platform,'linux','Release fixture requires a dedicated Linux runner');
const root=process.cwd(),temp=fs.mkdtempSync(path.join(os.tmpdir(),'wr-release-smoke-')),dir=path.join(temp,'installation'),bin=path.join(temp,'wrctl');
const result=spawnSync('go',['build','-trimpath','-ldflags',`-X waiting-room/internal/installer.DefaultImage=${image}`,'-o',bin,'./cmd/wrctl'],{encoding:'utf8'});
assert.equal(result.status,0,'CLI build');
function run(command,...args){const result=spawnSync(bin,[command,...args,'--directory',dir],{cwd:temp,encoding:'utf8',timeout:360000,maxBuffer:1024*1024});assert.equal(result.status,0,`wrctl ${command} succeeds; diagnostic output suppressed to protect fixture credentials`);return result.stdout;}
function state(){const result=JSON.parse(run('status','--json'));assert.equal(result.phase,'ready');for(const service of ['postgres','valkey','control','gateway','coordinator','demo-origin']){const item=result.services.find(item=>item.service===service);assert.ok(item,service+' exists');assert.equal(item.state,'running');assert.equal(item.health,'healthy');}return result;}
function secretDigests(){return fs.readdirSync(path.join(dir,'secrets')).sort().map(name=>crypto.createHash('sha256').update(fs.readFileSync(path.join(dir,'secrets',name))).digest('hex'));}
let cookie='',csrf='',setupToken='',tunnel;
function request(port,url,method='GET',body,headers={}){return new Promise((resolve,reject)=>{
  const data=body===undefined?undefined:JSON.stringify(body);
  const req=https.request({hostname:'127.0.0.1',port,path:url,method,rejectUnauthorized:false,headers:{Origin:`https://127.0.0.1:${port}`,...(data?{'Content-Type':'application/json','Content-Length':Buffer.byteLength(data)}:{}),...(cookie?{Cookie:cookie}:{}),...headers},timeout:15000},response=>{let text='';response.on('data',part=>{text+=part;});response.on('end',()=>{if(response.headers['set-cookie'])cookie=response.headers['set-cookie'].map(value=>value.split(';')[0]).join('; ');let json;try{json=JSON.parse(text);}catch{reject(Error('Expected fixture JSON response'));return;}resolve({status:response.statusCode,json,etag:response.headers.etag});});});
  req.on('error',()=>reject(Error('Local fixture request failed')));req.on('timeout',()=>req.destroy());req.end(data);
});}
async function closeTunnel(){
  if(!tunnel)return;const child=tunnel;tunnel=null;
  if(child.exitCode!==null||child.signalCode!==null||!child.pid)return;
  await new Promise(resolve=>{let timer;const done=()=>{clearTimeout(timer);resolve();};child.once('exit',done);child.kill('SIGTERM');timer=setTimeout(()=>{child.kill('SIGKILL');resolve();},5000);});
}
try{
  run('install','--totp','off');const first=state(),secrets=secretDigests();assert.equal(first.image,image);console.log('PASS: source-free digest-pinned install and six healthy services');
  tunnel=spawn(bin,['setup','--directory',dir],{cwd:temp,stdio:['ignore','pipe','pipe']});
  await new Promise((resolve,reject)=>{let seen='';const timer=setTimeout(()=>reject(Error('Private setup tunnel did not become ready')),30000);tunnel.on('error',()=>{clearTimeout(timer);reject(Error('Setup spawn failed'));});tunnel.once('exit',()=>{clearTimeout(timer);reject(Error('Setup exited before readiness'));});tunnel.stdout.on('data',chunk=>{seen+=chunk;if(seen.includes('Open https://127.0.0.1:19444/setup')){const match=seen.match(/^([A-Za-z0-9_-]{43})$/m);if(!match){clearTimeout(timer);reject(Error('Private setup token missing'));return;}setupToken=match[1];seen='';clearTimeout(timer);resolve();}});tunnel.stderr.on('data',()=>{});});
  const token=setupToken,password=crypto.randomBytes(32).toString('base64url');
  const account=await request(19444,'/api/admin/v1/bootstrap','POST',{username:'release_smoke',password},{'X-WR-Auth':'1','X-Bootstrap-Token':token});assert.equal(account.status,200);assert.equal(account.json.state,'authenticated');csrf=account.json.csrfToken;
  const original=await request(19443,'/api/admin/v1/config/draft');assert.equal(original.status,200);
  const room={...newRoom(),id:'release_smoke',name:'Release persistence check',hostname:'shop.example.test',origin:'https://origin.example.test',healthURL:'https://origin.example.test/health'};
  const saved=await request(19443,'/api/admin/v1/config/draft','PUT',{...original.json,rooms:[room]},{'X-CSRF-Token':csrf,'If-Match':original.etag,'Idempotency-Key':crypto.randomUUID()});assert.equal(saved.status,200);await closeTunnel();
  run('stop');run('up');state();assert.deepEqual(secretDigests(),secrets);console.log('PASS: stop/up retains private installation identity and secrets');
  run('upgrade');const upgraded=state();assert.equal(upgraded.project,first.project);assert.equal(upgraded.image,image);assert.deepEqual(secretDigests(),secrets);
  const restored=await request(19443,'/api/admin/v1/config/draft');assert.equal(restored.status,200);assert.equal(restored.json.rooms[0].name,room.name);assert.equal(restored.json.revision,saved.json.revision);console.log('PASS: same-version upgrade retains account session, Room draft, revision, and secrets');
}finally{await closeTunnel();if(fs.existsSync(path.join(dir,'installation.json')))run('stop');}
