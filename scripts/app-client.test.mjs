// SPDX-License-Identifier: Apache-2.0
import assert from 'node:assert/strict';
import test from 'node:test';
import {waitForAdmission,QueueError} from '../examples/app-client/client.mjs';

const room='abcdefghijklmnopqrst',key='retained-client-key-1234',token='private-ticket';
const queued={apiVersion:'v1',state:'queued',roomId:room,ticketToken:token,pollAfterMs:3000,heartbeatAfterMs:6000,expiresAt:'2030-01-01T00:00:00Z',statusUrl:'https://evil.invalid/leak'};
const admission={apiVersion:'v1',state:'admitted',roomId:room,admissionToken:'private-admission',expiresAt:'2030-01-01T00:00:00Z'};
const response=(status,body,headers={})=>new Response(status===204?null:JSON.stringify(body),{status,headers:{'Content-Type':'application/json',...headers}});
function fixture(handler,extra={}) {
  let time=0;const calls=[],states=[],waits=[];
  const run=()=>waitForAdmission({origin:'https://waiting.example.test',target:'/shop?item=1',joinKey:key,joinCreatedAt:Date.now(),jitter:()=>0,now:()=>time,
    sleep:async ms=>{waits.push(ms);time+=ms;},onState:s=>states.push(s),
    fetchImpl:async(url,options)=>{const call={url,options,at:time};calls.push(call);return handler(call,calls);},...extra});
  return {run,calls,states,waits};
}
test('lost join response reuses exact key/body and never follows response URLs or replays customer writes',async()=>{
  let joins=0;
  const f=fixture(({url,options})=>{
    assert.equal(url.origin,'https://waiting.example.test');assert.equal(options.redirect,'error');assert.equal(options.credentials,'omit');
    assert.equal(options.headers.Cookie,undefined);
    if(url.pathname==='/_wr/v1/tickets') {if(++joins===1)throw Error('private transport details');return response(202,queued);}
    assert.equal(options.headers.Authorization,'Bearer '+token);
    if(url.pathname.endsWith('/status'))return response(200,{apiVersion:'v1',state:'ready',roomId:room,claimUrl:'https://evil.invalid/claim'});
    if(url.pathname.endsWith('/admissions'))return response(200,admission);
    assert.fail('unexpected operation');
  });
  assert.equal((await f.run()).admissionToken,admission.admissionToken);
  assert.equal(joins,2);assert.deepEqual(f.calls[0].options.headers,f.calls[1].options.headers);assert.equal(f.calls[0].options.body,f.calls[1].options.body);
  assert.ok(f.calls.every(c=>c.url.pathname.startsWith('/_wr/')));
  assert.equal(JSON.stringify(f.states).includes('private-'),false);
});
test('status polls and temporary status failures cannot postpone independent heartbeats',async()=>{
  let reads=0,beats=0;
  const f=fixture(({url,at})=>{
    if(url.pathname.endsWith('/tickets'))return response(202,queued);
    if(url.pathname.endsWith('/heartbeat')){beats++;assert.equal(at,6000);return response(204);}
    if(url.pathname.endsWith('/status')) {
      if(++reads===1)return response(429,{code:'API_RATE_LIMITED'},{'Retry-After':'5'});
      if(reads===2)return response(202,queued);
      return response(200,{apiVersion:'v1',state:'ready',roomId:room});
    }
    return response(200,admission);
  });
  await f.run();assert.equal(beats,1);
  assert.deepEqual(f.calls.filter(c=>c.url.pathname.endsWith('/status')).map(c=>c.at),[3000,8000,11000]);
});
test('lost claim reply retries same ticket after Retry-After',async()=>{
  let claims=0;
  const f=fixture(({url})=>{
    if(url.pathname.endsWith('/tickets'))return response(202,queued);
    if(url.pathname.endsWith('/status'))return response(200,{apiVersion:'v1',state:'admitted',roomId:room});
    if(++claims===1)return response(503,{code:'QUEUE_UNAVAILABLE'},{'Retry-After':'4'});
    return response(200,admission);
  });
  await f.run();const calls=f.calls.filter(c=>c.url.pathname.endsWith('/admissions'));
  assert.equal(calls[1].at-calls[0].at,4000);assert.deepEqual(calls[0].options.headers,calls[1].options.headers);
});
test('expired ticket is terminal and does not allocate a replacement',async()=>{
  const f=fixture(({url})=>url.pathname.endsWith('/tickets')?response(202,queued):response(410,{code:'TICKET_EXPIRED',detail:'private data'}));
  await assert.rejects(f.run(),e=>e instanceof QueueError&&e.code==='TICKET_EXPIRED'&&!e.message.includes('private'));
  assert.equal(f.calls.filter(c=>c.url.pathname.endsWith('/tickets')).length,1);
});
test('OFF pass never manufactures an admission or forwards a customer request',async()=>{
  const f=fixture(()=>response(200,{apiVersion:'v1',state:'pass'}));
  assert.deepEqual(await f.run(),{state:'pass'});assert.equal(f.calls.length,1);
});
test('uncertain join stops before replay window and never changes its key',async()=>{
  const f=fixture(()=>response(503,{code:'CONFIG_UNAVAILABLE'},{'Retry-After':'120'}));
  await assert.rejects(f.run(),e=>e.code==='JOIN_RETRY_WINDOW');
  assert.ok(f.calls.length>1);assert.equal(f.calls.at(-1).at,480000);
  assert.ok(f.calls.every(c=>c.options.headers['Idempotency-Key']===key));
});
test('cancellation, invalid authority and invalid poll metadata never grant access',async()=>{
  for(const origin of ['http://waiting.example.test','https://user:secret@waiting.example.test','https://waiting.example.test/path']) {
    const f=fixture(()=>assert.fail('invalid input performed request'),{origin});await assert.rejects(f.run(),e=>e.code==='INVALID_INPUT');
  }
  const controller=new AbortController();controller.abort();
  await assert.rejects(fixture(()=>assert.fail('aborted request'),{signal:controller.signal}).run(),e=>e.name==='AbortError');
  await assert.rejects(fixture(()=>response(202,{...queued,pollAfterMs:0})).run(),e=>e.code==='INVALID_RESPONSE');
});
test('a persisted old or future-dated join intent is not silently reused as a new visitor',async()=>{
  for(const joinCreatedAt of [Date.now()-10*60000,Date.now()+10000]) {
    await assert.rejects(fixture(()=>assert.fail('stale intent performed request'),{joinCreatedAt}).run(),e=>e.code==='JOIN_RETRY_WINDOW');
  }
});
