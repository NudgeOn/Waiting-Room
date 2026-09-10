// SPDX-License-Identifier: Apache-2.0
// Reference flow for a native app/server-side integration, not a browser SDK.
import {randomUUID} from 'node:crypto';
import {setTimeout as delay} from 'node:timers/promises';

export class QueueError extends Error {
  constructor(code,status=0,retryAfterMs=0) {
    super('Waiting Room: '+code);this.name='QueueError';
    this.code=code;this.status=status;this.retryAfterMs=retryAfterMs;
  }
}
const codes=new Set(['INVALID_REQUEST','UNAUTHENTICATED','FORBIDDEN','NOT_FOUND','IDEMPOTENCY_CONFLICT','TICKET_EXPIRED','WAITING_ROOM_REQUIRED','API_RATE_LIMITED','QUEUE_DRAINING','QUEUE_CAPACITY_EXCEEDED','CONFIG_UNAVAILABLE','QUEUE_UNAVAILABLE','TICKET_NOT_READY']);
const positive=n=>Number.isSafeInteger(n)&&n>0;
export const newJoinIntent=target=>({target,joinKey:randomUUID(),joinCreatedAt:Date.now()});

// Persist the key and target together before calling this function if the app
// needs to survive a lost join response. Never regenerate the key on a retry.
export async function waitForAdmission({origin,target,joinKey,joinCreatedAt,signal,onState=()=>{},fetchImpl=fetch,
  now=()=>performance.now(),sleep=(ms,signal)=>delay(ms,undefined,{signal}),jitter=()=>Math.floor(Math.random()*251)}) {
  let base;
  try {base=new URL(origin);}catch{throw new QueueError('INVALID_INPUT');}
  if(base.protocol!=='https:'||base.username||base.password||base.pathname!=='/'||base.search||base.hash||
    typeof target!=='string'||target.length>2048||!target.startsWith('/')||target.startsWith('//')||/[\\\x00-\x20\x7f]/.test(target)||
    new URL(target,base).origin!==base.origin||new URL(target,base).hash||
    typeof joinKey!=='string'||!/^[A-Za-z0-9_-]{16,128}$/.test(joinKey)||!positive(joinCreatedAt))throw new QueueError('INVALID_INPUT');
  const initialAge=Date.now()-joinCreatedAt;
  if(initialAge<0||initialAge>=9*60000)throw new QueueError('JOIN_RETRY_WINDOW');
  const started=now();let ticket,room,nextPoll=0,nextHeartbeat=Infinity,heartbeatInterval=Infinity,failures=0;
  const announce=body=>onState({state:body.state,usersAhead:body.usersAhead??null,estimatedWaitSeconds:body.estimatedWaitSeconds??null});
  const pause=ms=>sleep(ms,signal);
  async function call(method,path,body,join=false) {
    signal?.throwIfAborted();
    let response;
    try {
      response=await fetchImpl(new URL(path,base),{method,redirect:'error',credentials:'omit',cache:'no-store',
        signal:signal?AbortSignal.any([signal,AbortSignal.timeout(10000)]):AbortSignal.timeout(10000),
        headers:{Accept:'application/json',...(body?{'Content-Type':'application/json'}:{}),...(join?{'Idempotency-Key':joinKey}:{}),...(ticket?{Authorization:'Bearer '+ticket}:{})},
        ...(body?{body:JSON.stringify(body)}:{})});
    } catch {
      signal?.throwIfAborted();return {retry:1000,code:'TRANSPORT_UNAVAILABLE',status:0};
    }
    let data;
    try {data=response.status===204?null:await response.json();}catch{throw new QueueError('INVALID_RESPONSE',response.status);}
    if(response.status===429||response.status===503) {
      const header=response.headers.get('Retry-After');
      const seconds=header===null?1:Number(header);
      if(!positive(seconds)||seconds>3600)throw new QueueError('INVALID_RETRY_AFTER',response.status);
      return {retry:seconds*1000,code:codes.has(data?.code)?data.code:'UNAVAILABLE',status:response.status};
    }
    if(response.status>=400)throw new QueueError(codes.has(data?.code)?data.code:'REQUEST_REJECTED',response.status);
    return {status:response.status,data};
  }
  function retry(out) {
    if(++failures>8)throw new QueueError(out.code,out.status,out.retry);
    return out.retry+jitter();
  }
  function admitted(body,status) {
    if(status!==200||body?.apiVersion!=='v1'||body.state!=='admitted'||!/^[a-z2-7]{20}$/.test(body.roomId)||(room&&body.roomId!==room)||typeof body.admissionToken!=='string'||!body.admissionToken||!Number.isFinite(Date.parse(body.expiresAt)))throw new QueueError('INVALID_RESPONSE',status);
    announce(body);
    return {state:'admitted',admissionToken:body.admissionToken,expiresAt:body.expiresAt};
  }
  function queued(body,status) {
    if(status!==202||body?.apiVersion!=='v1'||body.state!=='queued'||body.roomId!==room||!positive(body.pollAfterMs)||body.pollAfterMs<3000||body.pollAfterMs>20000||!positive(body.heartbeatAfterMs)||body.heartbeatAfterMs>300000)throw new QueueError('INVALID_RESPONSE',status);
    nextPoll=now()+body.pollAfterMs+jitter();
    heartbeatInterval=body.heartbeatAfterMs;
    // Polling must never keep postponing a heartbeat. A shorter new interval
    // may bring it forward; only a successful heartbeat moves it later.
    nextHeartbeat=Math.min(nextHeartbeat,now()+heartbeatInterval);
    announce(body);
  }
  for(;;) {
    const out=await call('POST','/_wr/v1/tickets',{target},true);
    if(out.retry) {
      const wait=retry(out);
      // Server join replay retention is ten minutes. Do not silently turn an
      // uncertain old request into a new visitor after that window.
      if(initialAge+now()+wait-started>=9*60000)throw new QueueError('JOIN_RETRY_WINDOW',out.status,wait);
      await pause(wait);continue;
    }
    const body=out.data;
    if(out.status===200&&body?.apiVersion==='v1'&&body.state==='pass'){announce(body);return {state:'pass'};}
    if(body?.state==='admitted')return admitted(body,out.status);
    if(!/^[a-z2-7]{20}$/.test(body?.roomId)||typeof body?.ticketToken!=='string'||!body.ticketToken)throw new QueueError('INVALID_RESPONSE',out.status);
    room=body.roomId;ticket=body.ticketToken;queued(body,out.status);failures=0;break;
  }
  const route='/_wr/v1/rooms/'+room;let claim=false;
  for(;;) {
    signal?.throwIfAborted();
    const heartbeat=!claim&&nextHeartbeat<=nextPoll;
    await pause(Math.max(0,(claim?nextPoll:Math.min(nextPoll,nextHeartbeat))-now()));
    const out=await call(heartbeat?'POST':claim?'POST':'GET',route+(heartbeat?'/heartbeat':claim?'/admissions':'/status'));
    if(out.retry) {
      const next=now()+retry(out);
      if(heartbeat)nextHeartbeat=next;else nextPoll=next;
      continue;
    }
    failures=0;
    if(heartbeat) {if(out.status!==204)throw new QueueError('INVALID_RESPONSE',out.status);nextHeartbeat=now()+heartbeatInterval;continue;}
    if(claim)return admitted(out.data,out.status);
    const body=out.data;
    if(body?.apiVersion!=='v1'||body.roomId!==room)throw new QueueError('INVALID_RESPONSE',out.status);
    if(out.status===200&&['ready','admitted'].includes(body.state)) {claim=true;nextPoll=now();continue;}
    queued(body,out.status);
  }
}
