// SPDX-License-Identifier: Apache-2.0
import assert from 'node:assert/strict';
import crypto from 'node:crypto';
import https from 'node:https';
import {setTimeout as delay} from 'node:timers/promises';

// Called only with the owning isolated fixture's Compose wrapper. Exercise
// Control loss while Gateway/Coordinator continue from their signed snapshot.
export async function syncFaultChecks({docker,request,csrf}) {
 const before=(await request(29443,'/config/delivery')).body;
 assert.equal(before.state,'applied');
 let paused=false;
 try {
  docker('pause','control');paused=true;await delay(6000);
  const status=await new Promise((resolve,reject)=>{
   const payload=JSON.stringify({target:'/shop/control-outage'});
   const req=https.request({hostname:'127.0.0.1',port:30443,path:'/_wr/v1/tickets',method:'POST',agent:false,rejectUnauthorized:false,timeout:10000,headers:{Host:'127.0.0.1:20443','Content-Type':'application/json','Content-Length':Buffer.byteLength(payload),'Idempotency-Key':crypto.randomUUID()}},res=>{res.resume();res.once('end',()=>resolve(res.statusCode));});
   req.on('error',()=>reject(Error('fresh public outage probe failed')));req.on('timeout',()=>req.destroy());req.end(payload);
  });
  assert.equal(status,202,'signed LKG retains held queue during Control outage');
 } finally {if(paused)docker('unpause','control');}
 const runtime=await request(29443,'/rooms/setup_room/runtime');assert.equal(runtime.status,200);
 const changed=await request(29443,'/rooms/setup_room/runtime','PATCH',{action:'hold'},{'X-CSRF-Token':csrf,'If-Match':runtime.etag,'Idempotency-Key':crypto.randomUUID()});assert.equal(changed.status,200);
 let applied=false;
 for(let i=0;i<40;i++){
  const out=await request(29443,'/config/delivery');assert.equal(out.status,200);
  if(out.body.state==='applied'&&out.body.generation>before.generation&&out.body.nodes.length===2&&out.body.nodes.every(n=>n.generation===out.body.generation)){applied=true;break;}
  await delay(500);
 }
 assert.equal(applied,true,'both roles resume new signed generation without restart');
 const diagnostic=docker('logs','--no-color','control','gateway','coordinator').split('\n').filter(line=>line.includes('runtime_sync'));
 for(const line of diagnostic)console.log(line);
 console.log('PASS: actual Control process pause exceeds fetch timeout; signed LKG serves a held join; both roles acknowledge a new generation after unpause without restart');
}
