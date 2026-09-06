// SPDX-License-Identifier: Apache-2.0
import test from 'node:test';
import assert from 'node:assert/strict';
import {controlAPI,newRoom} from '../src/control-api.js';
test('new Room remains inactive, FIFO and local built-in template',()=>{
  const a=newRoom(),b=newRoom();assert.equal(a.active,false);assert.equal(a.queuePolicy.kind,'fifo');assert.equal(a.theme.templateId,'calm');assert.match(a.publicId,/^[a-z2-7]{20}$/);assert.notEqual(a.publicId,b.publicId);
});
test('draft write preserves caller replay key and ETag and never reports activation',async()=>{
  const old=globalThis.fetch;let request;globalThis.fetch=async(url,options)=>{request={url,...options};return {ok:true,headers:new Headers({'X-WR-Config-State':'draft-only','ETag':'"config-1"','Idempotency-Replayed':'true'}),json:async()=>({revision:1})};};
  try{const out=await controlAPI('/config/draft',{method:'PUT',body:{rooms:[]},csrf:'csrf',etag:'"config-0"',key:'same-retry-key'});assert.equal(request.headers['If-Match'],'"config-0"');assert.equal(request.headers['Idempotency-Key'],'same-retry-key');assert.equal(request.redirect,'error');assert.equal(out.replay,true);assert.equal(out.etag,'"config-1"');}finally{globalThis.fetch=old;}
});
test('revision conflict is explicit and raw errors are not shown',async()=>{
  const old=globalThis.fetch;globalThis.fetch=async()=>({ok:false,status:412});try{await assert.rejects(controlAPI('/config/draft'),e=>e.status===412&&e.message.includes('최신 초안'));}finally{globalThis.fetch=old;}
});
